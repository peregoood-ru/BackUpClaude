package restore

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
)

// fakeRepo keeps each snapshot as a folder: the file of snapshot path /C/x/y
// lies at <dir>/<id>/C/x/y. It behaves like restic where restore depends on it:
// a subfolder lands directly in the target, includes are relative to it, and
// an existing file is never overwritten.
type fakeRepo struct {
	dir       string
	snapshots []engine.Snapshot
	restores  []engine.RestoreOptions
}

func newFakeRepo(t *testing.T) *fakeRepo {
	t.Helper()
	return &fakeRepo{dir: t.TempDir()}
}

// snapshot copies the sources into a new snapshot. Times are in order of
// creation.
func (f *fakeRepo) snapshot(t *testing.T, id, tag, host string, sources ...string) engine.Snapshot {
	t.Helper()
	for _, source := range sources {
		dest := filepath.Join(f.dir, id, filepath.FromSlash(strings.TrimPrefix(SnapshotPath(source), "/")))
		info, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		if info.IsDir() {
			if err := os.CopyFS(dest, os.DirFS(source)); err != nil {
				t.Fatal(err)
			}
			// restic keeps the times of files; CopyFS does not.
			err := filepath.WalkDir(source, func(p string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(source, p)
				info, _ := entry.Info()
				return os.Chtimes(filepath.Join(dest, rel), info.ModTime(), info.ModTime())
			})
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		fakehome.Write(t, dest, string(data))
		if err := os.Chtimes(dest, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := engine.Snapshot{
		ID: id + "00000000", ShortID: id, Time: time.Date(2026, 11, 1, len(f.snapshots), 0, 0, 0, time.UTC),
		Hostname: host, Paths: sources, Tags: []string{tag},
	}
	f.snapshots = append(f.snapshots, snapshot)
	return snapshot
}

func (f *fakeRepo) folder(snapshot string) string {
	for _, s := range f.snapshots {
		if s.ID == snapshot || s.ShortID == snapshot {
			return filepath.Join(f.dir, s.ShortID)
		}
	}
	return filepath.Join(f.dir, "missing")
}

func (f *fakeRepo) Snapshots(context.Context, string) ([]engine.Snapshot, error) {
	return f.snapshots, nil
}

func (f *fakeRepo) walk(snapshot string, visit func(node engine.Node)) {
	root := f.folder(snapshot)
	_ = filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		info, _ := entry.Info()
		node := engine.Node{Name: entry.Name(), Path: "/" + filepath.ToSlash(rel), Type: engine.NodeFile, Mtime: info.ModTime()}
		if entry.IsDir() {
			node.Type = engine.NodeDir
		} else {
			node.Size = info.Size()
		}
		visit(node)
		return nil
	})
}

func (f *fakeRepo) Nodes(_ context.Context, snapshot string, paths []string, recursive bool, onNode func(engine.Node)) error {
	f.walk(snapshot, func(node engine.Node) {
		for _, p := range paths {
			direct := path.Dir(node.Path) == p
			if node.Path == p || direct || (recursive && strings.HasPrefix(node.Path, p+"/")) {
				onNode(node)
				return
			}
		}
	})
	return nil
}

func (f *fakeRepo) Find(_ context.Context, patterns []string, _ string, only []string) ([]engine.FindResult, error) {
	var results []engine.FindResult
	for _, snapshot := range f.snapshots {
		if len(only) > 0 && only[0] != snapshot.ID {
			continue
		}
		result := engine.FindResult{Snapshot: snapshot.ID}
		var matched []string
		f.walk(snapshot.ID, func(node engine.Node) {
			for _, prefix := range matched {
				if strings.HasPrefix(node.Path, prefix+"/") {
					result.Matches = append(result.Matches, node)
					return
				}
			}
			for _, pattern := range patterns {
				if ok, _ := path.Match(pattern, node.Name); ok {
					result.Matches = append(result.Matches, node)
					if node.Type == engine.NodeDir {
						matched = append(matched, node.Path)
					}
					return
				}
			}
		})
		if len(result.Matches) > 0 {
			results = append(results, result)
		}
	}
	return results, nil
}

func (f *fakeRepo) Restore(_ context.Context, opts engine.RestoreOptions) (*engine.RestoreSummary, error) {
	f.restores = append(f.restores, opts)
	includes := append([]string(nil), opts.Includes...)
	if opts.IncludeFile != "" {
		file, err := os.Open(opts.IncludeFile)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			includes = append(includes, scanner.Text())
		}
		_ = file.Close()
	}
	source := filepath.Join(f.folder(opts.Snapshot), filepath.FromSlash(strings.TrimPrefix(opts.Subfolder, "/")))
	summary := &engine.RestoreSummary{}
	err := filepath.WalkDir(source, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(source, p)
		rel = "/" + filepath.ToSlash(rel)
		if len(includes) > 0 && !included(includes, rel) {
			return nil
		}
		dest := filepath.Join(opts.Target, filepath.FromSlash(rel))
		if _, err := os.Lstat(dest); err == nil {
			return nil // --overwrite never
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
		info, _ := entry.Info()
		summary.FilesRestored++
		return os.Chtimes(dest, info.ModTime(), info.ModTime())
	})
	return summary, err
}

// included follows restic: a pattern names a file or a folder with everything
// inside it; a class of one character stands for the character.
func included(patterns []string, rel string) bool {
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, rel); ok {
			return true
		}
		parts := strings.Split(rel, "/")
		for i := range parts {
			if ok, _ := path.Match(pattern, strings.Join(parts[:i+1], "/")); ok && i > 0 {
				return true
			}
		}
	}
	return false
}
