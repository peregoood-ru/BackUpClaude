package restore

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Fetched is what a restore wrote to its folder.
type Fetched struct {
	Target string `json:"target"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	// Digest is the hash of the whole restored tree (see HashTree): two
	// restores of the same thing give the same digest.
	Digest string `json:"tree_sha256"`
	// Verified is true when restic read every restored file back and found it
	// equal to the repository.
	Verified bool `json:"verified"`
}

// Fetch restores the pieces into target, each where its path says (see
// StagedPath). Nothing in target is ever overwritten, and restic reads every
// file back after writing it.
func Fetch(ctx context.Context, repo Repo, pieces []Piece, target string) (*Fetched, error) {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return nil, i18n.E("restore.target_failed", target).Because(err)
	}
	for _, piece := range pieces {
		dest := StagedPath(target, piece.Root)
		if err := os.MkdirAll(dest, 0o700); err != nil {
			return nil, i18n.E("restore.target_failed", dest).Because(err)
		}
		opts := engine.RestoreOptions{Snapshot: piece.Snapshot, Subfolder: piece.Root, Target: dest, Verify: true}
		for _, name := range piece.Names {
			opts.Includes = append(opts.Includes, "/"+EscapePattern(name))
		}
		if _, err := repo.Restore(ctx, opts); err != nil {
			return nil, err
		}
	}
	fetched := &Fetched{Target: target, Verified: true}
	tree, err := HashTree(target)
	if err != nil {
		return nil, err
	}
	fetched.Files, fetched.Bytes, fetched.Digest = len(tree.Files), tree.Bytes, tree.Digest
	return fetched, nil
}

// EscapePattern makes a file name a literal restic pattern: restic patterns
// treat *, ? and [ as wildcards, and a class of one character matches the
// character itself.
func EscapePattern(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '*', '?', '[':
			b.WriteByte('[')
			b.WriteRune(r)
			b.WriteByte(']')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DefaultTarget is the folder a restore writes to when the person names none:
// next to a local repository, in a folder named after the moment, so that two
// restores never mix.
func DefaultTarget(base, stamp string) string {
	return filepath.Join(base, "restored", stamp)
}
