package restore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Reasons a file is not put back.
const (
	SkipExists         = "exists"
	SkipDesktopMissing = "desktop_missing"
	SkipDriveMissing   = "drive_missing"
	SkipNotFile        = "not_a_file"
)

// rewriteLimit is the largest file whose paths are rewritten. Settings and
// descriptions are small; anything bigger is copied as it is.
const rewriteLimit = 32 << 20

// rewriteExtensions are the files whose text holds paths worth moving.
var rewriteExtensions = map[string]bool{".json": true, ".md": true}

// Placement says where the restored data belongs on this computer.
type Placement struct {
	Manifest *manifests.Manifest
	// Origin is where Claude's data lived when the snapshot was made.
	Origin Origin
	// Mapper moves the paths of the origin to this computer.
	Mapper *Mapper
}

// Copy is one file to put back.
type Copy struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Locked marks a file of a database the desktop application keeps open.
	Locked bool `json:"locked,omitempty"`
	// Rewrite is true when paths inside the file are moved.
	Rewrite bool  `json:"rewrite,omitempty"`
	Size    int64 `json:"size"`
}

// Skipped is a file that is not put back, with the reason.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	// Differs is true for an existing file whose content is not the one in
	// the copy. It stays as it is; the restored one is in the restore folder.
	Differs bool `json:"differs,omitempty"`
}

// ApplyPlan is what putting back would do.
type ApplyPlan struct {
	Copies  []Copy    `json:"copies"`
	Skipped []Skipped `json:"skipped"`
}

// Locked reports whether the plan writes into a database of the desktop
// application.
func (p *ApplyPlan) Locked() bool {
	for _, item := range p.Copies {
		if item.Locked {
			return true
		}
	}
	return false
}

// PlanApply walks what Fetch wrote and decides, for every file, where it goes
// and whether it may go there. Only missing files are put back: an existing
// file is never replaced, whatever its content. That rule is Claude Vault's (see
// NOTICE).
func PlanApply(target string, pieces []Piece, place Placement) (*ApplyPlan, error) {
	plan := &ApplyPlan{}
	seen := map[string]bool{}
	for _, piece := range pieces {
		root := StagedPath(target, piece.Root)
		starts := []string{root}
		if len(piece.Names) > 0 {
			starts = starts[:0]
			for _, name := range piece.Names {
				starts = append(starts, filepath.Join(root, name))
			}
		}
		for _, start := range starts {
			if err := plan.walk(target, start, place, seen); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(plan.Copies, func(i, j int) bool { return plan.Copies[i].To < plan.Copies[j].To })
	return plan, nil
}

func (p *ApplyPlan) walk(target, start string, place Placement, seen map[string]bool) error {
	return filepath.WalkDir(start, func(staged string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && staged == start {
			return nil // an included name that the snapshot did not hold
		}
		if err != nil {
			return i18n.E("restore.read_failed", staged).Because(err)
		}
		if entry.IsDir() || seen[staged] {
			return nil
		}
		seen[staged] = true
		rel, err := filepath.Rel(target, staged)
		if err != nil {
			return i18n.E("restore.read_failed", staged).Because(err)
		}
		snapshotPath := "/" + filepath.ToSlash(rel)
		if !entry.Type().IsRegular() {
			p.Skipped = append(p.Skipped, Skipped{Path: LocalPath(snapshotPath), Reason: SkipNotFile})
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return i18n.E("restore.read_failed", staged).Because(err)
		}
		p.place(staged, snapshotPath, info.Size(), place)
		return nil
	})
}

func (p *ApplyPlan) place(staged, snapshotPath string, size int64, place Placement) {
	original := LocalPath(snapshotPath)
	dest, ok := destination(snapshotPath, place)
	if !ok {
		p.Skipped = append(p.Skipped, Skipped{Path: original, Reason: SkipDesktopMissing})
		return
	}
	if volume := filepath.VolumeName(dest); volume != "" {
		if _, err := os.Stat(volume + string(filepath.Separator)); err != nil {
			p.Skipped = append(p.Skipped, Skipped{Path: dest, Reason: SkipDriveMissing})
			return
		}
	}
	if _, err := os.Lstat(dest); err == nil {
		p.Skipped = append(p.Skipped, Skipped{Path: dest, Reason: SkipExists, Differs: differs(staged, dest)})
		return
	}
	item := Copy{From: staged, To: dest, Size: size, Locked: lockedPath(snapshotPath, place)}
	item.Rewrite = !place.Mapper.Identity() && size <= rewriteLimit &&
		rewriteExtensions[strings.ToLower(filepath.Ext(dest))] && claudeData(snapshotPath, place)
	p.Copies = append(p.Copies, item)
}

// destination maps a snapshot path to this computer. A transcript folder is
// renamed after the working folder it belongs to, the way Claude Code names it.
func destination(snapshotPath string, place Placement) (string, bool) {
	transcripts := place.Origin.Transcripts(place.Manifest)
	if transcripts != "" && Within(transcripts, snapshotPath) && fold(snapshotPath) != fold(transcripts) {
		rest := strings.TrimPrefix(snapshotPath[len(transcripts):], "/")
		folder, below, _ := strings.Cut(rest, "/")
		newRoot, ok := place.Mapper.Map(LocalPath(transcripts))
		if !ok {
			return "", false
		}
		parts := []string{newRoot, place.Mapper.ProjectFolder(folder)}
		if below != "" {
			parts = append(parts, filepath.FromSlash(below))
		}
		return filepath.Join(parts...), true
	}
	return place.Mapper.Map(LocalPath(snapshotPath))
}

func lockedPath(snapshotPath string, place Placement) bool {
	for _, root := range place.Origin.Desktop {
		for _, name := range place.Manifest.Desktop.LockedOK {
			if Within(path.Join(root, name), snapshotPath) {
				return true
			}
		}
	}
	return false
}

// claudeData reports whether a file is Claude's own: only those have their
// paths rewritten. The user's project files are copied exactly as they were.
func claudeData(snapshotPath string, place Placement) bool {
	origin := place.Origin
	if origin.Claude != "" && Within(origin.Claude, snapshotPath) {
		return true
	}
	for _, root := range origin.Desktop {
		if Within(root, snapshotPath) {
			return true
		}
	}
	return origin.Home != "" && fold(path.Dir(snapshotPath)) == fold(origin.Home)
}

// differs compares a restored file with the one in place. Size and time settle
// most cases without reading: a restore of everything meets many thousands of
// files that are already there.
func differs(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil || !infoB.Mode().IsRegular() {
		return true
	}
	if infoA.Size() != infoB.Size() {
		return true
	}
	if infoA.ModTime().Equal(infoB.ModTime()) {
		return false
	}
	first, err := fileHash(a)
	if err != nil {
		return true
	}
	second, err := fileHash(b)
	return err != nil || first != second
}

func fileHash(path string) (string, error) {
	sum, _, err := hashFile(path)
	return sum, err
}

// Applied is what putting back did.
type Applied struct {
	Copied    int   `json:"copied"`
	Bytes     int64 `json:"bytes"`
	Rewritten int   `json:"rewritten"`
}

// Apply writes the copies of a plan. A file that appeared in the meantime is
// left alone: the file is created only if it does not exist, in one step. Every
// file is read back and compared with what was meant to be written.
func Apply(plan *ApplyPlan, mapper *Mapper) (*Applied, error) {
	done := &Applied{}
	for _, item := range plan.Copies {
		info, err := os.Stat(item.From)
		if err != nil {
			return done, i18n.E("restore.read_failed", item.From).Because(err)
		}
		var source io.Reader
		closeSource := func() {}
		if item.Rewrite {
			// Small by rule (rewriteLimit), so it may be held in memory.
			data, err := os.ReadFile(item.From)
			if err != nil {
				return done, i18n.E("restore.read_failed", item.From).Because(err)
			}
			if rewritten := mapper.RewriteText(string(data)); rewritten != string(data) {
				data = []byte(rewritten)
				done.Rewritten++
			}
			source = bytes.NewReader(data)
		} else {
			// Streamed: a project can hold files far bigger than the memory.
			file, err := os.Open(item.From)
			if err != nil {
				return done, i18n.E("restore.read_failed", item.From).Because(err)
			}
			source = file
			closeSource = func() { _ = file.Close() }
		}
		size, written, err := createNew(item.To, source, info)
		closeSource()
		if err != nil {
			return done, err
		}
		if written {
			done.Copied++
			done.Bytes += size
		}
	}
	return done, nil
}

// createNew writes a file that must not exist yet and checks it. written is
// false when the file appeared since the plan was made.
func createNew(dest string, source io.Reader, info fs.FileInfo) (size int64, written bool, err error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, false, i18n.E("restore.write_failed", dest).Because(err)
	}
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm()|0o600)
	if errors.Is(err, fs.ErrExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, i18n.E("restore.write_failed", dest).Because(err)
	}
	hash := sha256.New()
	size, err = io.Copy(io.MultiWriter(file, hash), source)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, false, i18n.E("restore.write_failed", dest).Because(err)
	}
	_ = os.Chtimes(dest, info.ModTime(), info.ModTime())
	got, err := fileHash(dest)
	if err != nil || got != hex.EncodeToString(hash.Sum(nil)) {
		return 0, false, i18n.E("restore.verify_failed", dest).Hint("restore.verify_failed.hint")
	}
	return size, true, nil
}
