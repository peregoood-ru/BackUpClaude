package restore

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// LocalPath turns a path inside a snapshot back into a path of this OS: on
// Windows /C/Users/name becomes C:\Users\name. It is the reverse of
// SnapshotPath.
func LocalPath(snapshotPath string) string {
	return localPath(snapshotPath)
}

// StagedPath is where a snapshot path lands inside a restore folder: the drive
// letter becomes the first folder, so the user sees where each file came from
// and nothing from two drives can collide.
func StagedPath(target, snapshotPath string) string {
	rel := strings.TrimPrefix(path.Clean(snapshotPath), "/")
	if rel == "" || rel == "." {
		return target
	}
	return filepath.Join(target, filepath.FromSlash(rel))
}

// Within reports whether child is parent or lies inside it. Both are restic
// paths; on Windows the comparison ignores case, like the file system.
func Within(parent, child string) bool {
	p, c := fold(strings.TrimSuffix(parent, "/")), fold(child)
	return c == p || strings.HasPrefix(c, p+"/")
}

// WithinLocal is Within for paths of this OS.
func WithinLocal(parent, child string) bool {
	return Within(filepath.ToSlash(filepath.Clean(parent)), filepath.ToSlash(filepath.Clean(child)))
}

var notAlphanumeric = regexp.MustCompile(`[^A-Za-z0-9]`)

// ProjectFolder is the name Claude Code gives the folder of a working folder's
// transcripts: every character that is not an ASCII letter or digit becomes a
// dash, so D:\Work\app gives D--Work-app. The rule is the one documented by
// claude-moove (see NOTICE).
func ProjectFolder(workdir string) string {
	return notAlphanumeric.ReplaceAllString(workdir, "-")
}
