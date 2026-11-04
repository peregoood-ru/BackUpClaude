package profile

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/restore"
)

// rules is what a profile leaves out. Every pattern is anchored to a folder that
// is being saved. restic matches a bare name against every part of the full
// path, so a bare "build" would drop a whole project that sits in D:\build\app.
//
// The same rules drive the restic patterns, the size measurement and the check
// of a finished snapshot. All three compare in the form restic stores, in lower
// case, because the patterns are case-insensitive.
type rules struct {
	// roots are the folders being saved.
	roots []string
	// everywhere are names left out at any depth below a root.
	everywhere []string
	// anchored are names left out directly inside one folder.
	anchored []anchoredName
	// literal are exact paths left out.
	literal []string
	// repos are git repositories; repoNames are left out at any depth in them.
	repos     []string
	repoNames []string
	// scoped are names left out at any depth below one folder only. The claude
	// profile uses them for the user's own folders, so that "build" or
	// "node_modules" are dropped there and never inside Claude's own data.
	scoped []scopedNames
}

type scopedNames struct {
	dir   string
	names []string
}

type anchoredName struct {
	dir  string
	name string
}

func key(path string) string {
	return strings.ToLower(restore.SnapshotPath(filepath.Clean(path)))
}

// escapeLiteral stops restic reading a real path as a pattern. Windows has no
// escape character in patterns, so a bracket goes into a character class.
func escapeLiteral(path string) string {
	return strings.ReplaceAll(path, "[", "[[]")
}

// patterns renders the rules for restic's --iexclude-file.
func (r *rules) patterns() []string {
	var patterns []string
	for _, root := range r.roots {
		for _, name := range r.everywhere {
			patterns = append(patterns, filepath.Join(escapeLiteral(root), "**", name))
		}
	}
	for _, item := range r.anchored {
		patterns = append(patterns, filepath.Join(escapeLiteral(item.dir), item.name))
	}
	for _, item := range r.literal {
		patterns = append(patterns, escapeLiteral(item))
	}
	for _, repo := range r.repos {
		for _, name := range r.repoNames {
			patterns = append(patterns, filepath.Join(escapeLiteral(repo), "**", name))
		}
	}
	for _, scope := range r.scoped {
		for _, name := range scope.names {
			patterns = append(patterns, filepath.Join(escapeLiteral(scope.dir), "**", name))
		}
	}
	return patterns
}

// matcher is rules prepared for checking many paths.
type matcher struct {
	everywhere []string
	repoNames  []string
	anchored   map[string][]string
	literal    map[string]bool
	repos      []string
	scoped     []scopedKey
}

type scopedKey struct {
	dir   string
	names []string
}

func (r *rules) matcher() *matcher {
	m := &matcher{
		everywhere: lowered(r.everywhere),
		repoNames:  lowered(r.repoNames),
		anchored:   map[string][]string{},
		literal:    map[string]bool{},
	}
	for _, item := range r.anchored {
		dir := key(item.dir)
		m.anchored[dir] = append(m.anchored[dir], strings.ToLower(item.name))
	}
	for _, item := range r.literal {
		m.literal[key(item)] = true
	}
	for _, repo := range r.repos {
		m.repos = append(m.repos, key(repo))
	}
	for _, scope := range r.scoped {
		m.scoped = append(m.scoped, scopedKey{dir: key(scope.dir), names: lowered(scope.names)})
	}
	return m
}

func lowered(items []string) []string {
	result := make([]string, len(items))
	for i, item := range items {
		result[i] = strings.ToLower(item)
	}
	return result
}

// excluded reports whether an item in the folder parent is left out. Both come
// in key form. An excluded folder hides what lies below it, so callers stop at
// the first match on the way down.
func (m *matcher) excluded(parent, name string) bool {
	lower := strings.ToLower(name)
	if m.literal[parent+"/"+lower] || nameMatches(m.everywhere, lower) || nameMatches(m.anchored[parent], lower) {
		return true
	}
	for _, repo := range m.repos {
		if (parent == repo || strings.HasPrefix(parent, repo+"/")) && nameMatches(m.repoNames, lower) {
			return true
		}
	}
	for _, scope := range m.scoped {
		if (parent == scope.dir || strings.HasPrefix(parent, scope.dir+"/")) && nameMatches(scope.names, lower) {
			return true
		}
	}
	return false
}

func nameMatches(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if pattern == name {
			return true
		}
		if ok, err := path.Match(pattern, name); ok && err == nil {
			return true
		}
	}
	return false
}
