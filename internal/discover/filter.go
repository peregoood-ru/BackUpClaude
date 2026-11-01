package discover

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Reasons why a working folder is not a project.
const (
	SkipHome    = "home"
	SkipRoot    = "root"
	SkipSystem  = "not_project"
	SkipForeign = "foreign"
)

// Filter decides which working folders are projects.
type Filter struct {
	home      string
	trees     []string
	worktrees [][]string
}

// NewFilter prepares the checks from the manifest.
func NewFilter(manifest *manifests.Manifest, env Env) *Filter {
	filter := &Filter{}
	if home, ok := normalizePath(env.Home); ok {
		filter.home = pathKey(home)
	}
	for _, path := range env.ExpandAll(manifest.Projects.NotProjects) {
		if tree, ok := normalizePath(path); ok {
			filter.trees = append(filter.trees, pathKey(tree))
		}
	}
	for _, dir := range manifest.Projects.WorktreeDirs {
		filter.worktrees = append(filter.worktrees, strings.Split(dir, "/"))
	}
	return filter
}

// Classify returns the project behind a working folder, or the reason it is not
// one. A worktree is not a project of its own but it does point at one: a
// project opened only through worktrees would otherwise never be found.
func (f *Filter) Classify(raw string) (project, skip string) {
	path, ok := normalizePath(raw)
	if !ok {
		return "", SkipForeign
	}
	path = mainWorktree(path, f.worktrees)
	key := pathKey(path)
	switch {
	case filepath.Dir(path) == path:
		return "", SkipRoot
	case key == f.home:
		return "", SkipHome
	case f.home != "" && isInside(f.home, key):
		// A folder above home would bring the whole home folder with it.
		return "", SkipRoot
	}
	for _, tree := range f.trees {
		if key == tree || isInside(key, tree) {
			return "", SkipSystem
		}
	}
	return path, ""
}

// isInside reports whether the key path lies below the key tree.
func isInside(path, tree string) bool {
	return strings.HasPrefix(path, strings.TrimSuffix(tree, string(filepath.Separator))+string(filepath.Separator))
}

// mainWorktree returns the project a worktree belongs to, or path itself.
func mainWorktree(path string, worktreeDirs [][]string) string {
	for _, dir := range worktreeDirs {
		if before, ok := cutAtComponents(path, dir); ok {
			return before
		}
	}
	return linkedWorktreeOwner(path)
}

// linkedWorktreeOwner reads the .git file of a linked worktree. It holds
// "gitdir: <main repository>/.git/worktrees/<name>".
func linkedWorktreeOwner(path string) string {
	data, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return path
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return path
	}
	gitDir = filepath.FromSlash(strings.TrimSpace(gitDir))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(path, gitDir)
	}
	owner, ok := cutAtComponents(gitDir, []string{".git", "worktrees"})
	if !ok {
		return path
	}
	if normalized, ok := normalizePath(owner); ok {
		return normalized
	}
	return path
}

// cutAtComponents returns the part of path before a run of folder names.
func cutAtComponents(path string, names []string) (string, bool) {
	parts := strings.Split(path, string(filepath.Separator))
	for start := 1; start+len(names) <= len(parts); start++ {
		matched := true
		for i, name := range names {
			if !strings.EqualFold(parts[start+i], name) {
				matched = false
				break
			}
		}
		if matched {
			return filepath.Clean(strings.Join(parts[:start], string(filepath.Separator)) + string(filepath.Separator)), true
		}
	}
	return "", false
}
