package restore

import (
	"bufio"
	"context"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// DrillOptions limit a trial restore.
type DrillOptions struct {
	MaxFiles int
	MaxBytes int64
	// Seed picks the sample; a different one each run tests other files.
	Seed uint64
	// WorkDir receives the include lists; the restored files go to Target.
	WorkDir string
	Target  string
}

// DrillResult is the outcome of a trial restore of one snapshot.
type DrillResult struct {
	Snapshot string `json:"snapshot"`
	Profile  string `json:"profile"`
	// Candidates are the files whose copy on disk is unchanged since the
	// snapshot: only for them can the restored content be compared.
	Candidates int   `json:"candidates"`
	Sampled    int   `json:"sampled"`
	Restored   int   `json:"restored"`
	Matched    int   `json:"matched"`
	Bytes      int64 `json:"bytes"`
	// Changed counts files that changed on disk while the test ran; they are
	// not compared.
	Changed    int          `json:"changed,omitempty"`
	Mismatches []Difference `json:"mismatches,omitempty"`
}

// OK reports whether every sampled file came back with the content it has on
// disk.
func (r *DrillResult) OK() bool {
	return r.Restored > 0 && len(r.Mismatches) == 0
}

type sampleFile struct {
	node  engine.Node
	local string
	root  string
}

// Drill proves that a snapshot can be restored: it picks files whose copy on
// disk has not changed since the snapshot (same size and time), restores them
// into an empty folder with restic reading every file back, and compares the
// SHA-256 of each restored file with the file on disk.
func Drill(ctx context.Context, repo Repo, snapshot engine.Snapshot, profile string, opts DrillOptions) (*DrillResult, error) {
	result := &DrillResult{Snapshot: snapshot.ShortID, Profile: profile}
	roots := RootsOf(snapshot)
	var candidates []sampleFile
	err := repo.Nodes(ctx, snapshot.ID, roots, true, func(node engine.Node) {
		// A line break cannot be written to an include list.
		if node.Type != engine.NodeFile || strings.ContainsAny(node.Path, "\r\n") {
			return
		}
		local := LocalPath(node.Path)
		info, err := os.Lstat(local)
		if err != nil || !info.Mode().IsRegular() || !sameFile(node, info) {
			return
		}
		candidates = append(candidates, sampleFile{node: node, local: local, root: rootOf(roots, node.Path)})
	})
	if err != nil {
		return nil, err
	}
	result.Candidates = len(candidates)
	picked := pick(candidates, opts)
	result.Sampled = len(picked)
	if len(picked) == 0 {
		return result, nil
	}
	if err := restoreSample(ctx, repo, snapshot, picked, opts); err != nil {
		return result, err
	}
	for _, file := range picked {
		compareSample(file, opts.Target, result)
	}
	return result, nil
}

func sameFile(node engine.Node, info os.FileInfo) bool {
	if node.Size != info.Size() {
		return false
	}
	difference := node.Mtime.Sub(info.ModTime())
	return difference < time.Second && difference > -time.Second
}

// rootOf returns the saved path that holds p; for a saved file that is the
// folder around it, because restic restores folders only.
func rootOf(roots []string, p string) string {
	best := ""
	for _, root := range roots {
		if (p == root || strings.HasPrefix(p, root+"/")) && len(root) > len(best) {
			best = root
		}
	}
	if best == p {
		return path.Dir(p)
	}
	return best
}

func pick(candidates []sampleFile, opts DrillOptions) []sampleFile {
	shuffled := append([]sampleFile(nil), candidates...)
	random := rand.New(rand.NewPCG(opts.Seed, opts.Seed^0x9e3779b97f4a7c15))
	random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	var picked []sampleFile
	var total int64
	for _, file := range shuffled {
		if len(picked) >= opts.MaxFiles {
			break
		}
		if total+file.node.Size > opts.MaxBytes {
			continue
		}
		picked = append(picked, file)
		total += file.node.Size
	}
	return picked
}

// restoreSample runs one restore per saved folder, with the chosen files in an
// include list: one restic run each, however many files.
func restoreSample(ctx context.Context, repo Repo, snapshot engine.Snapshot, picked []sampleFile, opts DrillOptions) error {
	byRoot := map[string][]string{}
	for _, file := range picked {
		byRoot[file.root] = append(byRoot[file.root], "/"+EscapePattern(strings.TrimPrefix(file.node.Path, file.root+"/")))
	}
	roots := make([]string, 0, len(byRoot))
	for root := range byRoot {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	if err := os.MkdirAll(opts.WorkDir, 0o700); err != nil {
		return i18n.E("restore.target_failed", opts.WorkDir).Because(err)
	}
	for _, root := range roots {
		list, err := os.CreateTemp(opts.WorkDir, "restore-test-*.txt")
		if err != nil {
			return i18n.E("restore.target_failed", opts.WorkDir).Because(err)
		}
		writer := bufio.NewWriter(list)
		for _, pattern := range byRoot[root] {
			_, _ = writer.WriteString(pattern + "\n")
		}
		flushErr := writer.Flush()
		closeErr := list.Close()
		defer os.Remove(list.Name())
		if flushErr != nil || closeErr != nil {
			return i18n.E("restore.target_failed", list.Name())
		}
		dest := StagedPath(opts.Target, root)
		if err := os.MkdirAll(dest, 0o700); err != nil {
			return i18n.E("restore.target_failed", dest).Because(err)
		}
		if _, err := repo.Restore(ctx, engine.RestoreOptions{
			Snapshot: snapshot.ID, Subfolder: root, Target: dest, IncludeFile: list.Name(), Verify: true,
		}); err != nil {
			return err
		}
	}
	return nil
}

func compareSample(file sampleFile, target string, result *DrillResult) {
	staged := StagedPath(target, file.node.Path)
	got, size, err := hashFile(staged)
	if err != nil {
		result.Mismatches = append(result.Mismatches, Difference{Path: file.local, Reason: ReasonMissing})
		return
	}
	result.Restored++
	result.Bytes += size
	want, _, err := hashFile(file.local)
	info, statErr := os.Lstat(file.local)
	if err != nil || statErr != nil || !sameFile(file.node, info) {
		result.Changed++ // changed or gone while the test ran: nothing to compare with
		return
	}
	if got != want {
		result.Mismatches = append(result.Mismatches, Difference{Path: file.local, Reason: ReasonChanged})
		return
	}
	result.Matched++
}

// DrillFolder is the empty folder of a trial restore, next to the repository.
func DrillFolder(base string, now time.Time) string {
	return filepath.Join(base, "restore-test-"+now.Format("2006-01-02_150405"))
}
