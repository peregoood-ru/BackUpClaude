// Package discover finds Claude's data and the user's projects on this computer.
package discover

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Env gives access to the home folder and environment variables, so tests can
// point discovery at a fake ~/.claude.
type Env struct {
	Home   string
	Lookup func(name string) string
	// SameDir reports whether two paths are one folder. Nil means asking the OS;
	// tests set it to imitate aliases that need privileges to create.
	SameDir func(a, b string) bool
}

// CurrentEnv returns the environment of this process.
func CurrentEnv() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	return Env{Home: home, Lookup: os.Getenv}, nil
}

var percentVar = regexp.MustCompile(`%([^%]+)%`)

// Expand turns a manifest or settings path into a path of this computer. It
// returns false when the path refers to a variable that is not set.
func (e Env) Expand(path string) (string, bool) {
	ok := true
	expanded := percentVar.ReplaceAllStringFunc(path, func(match string) string {
		value := e.Lookup(strings.Trim(match, "%"))
		if value == "" {
			ok = false
		}
		return value
	})
	if !ok {
		return "", false
	}
	switch {
	case expanded == "~":
		return e.Home, true
	case strings.HasPrefix(expanded, "~/"), strings.HasPrefix(expanded, `~\`):
		expanded = filepath.Join(e.Home, filepath.FromSlash(expanded[2:]))
	}
	return filepath.FromSlash(expanded), true
}

// ExpandAll expands a list, dropping entries whose variables are not set.
func (e Env) ExpandAll(paths []string) []string {
	var expanded []string
	for _, path := range paths {
		if value, ok := e.Expand(path); ok {
			expanded = append(expanded, value)
		}
	}
	return expanded
}

func (e Env) sameDir(a, b string) bool {
	if e.SameDir != nil {
		return e.SameDir(a, b)
	}
	first, err := os.Stat(a)
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(first, second)
}

// Location is a place from the manifest and whether it exists here.
type Location struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// Claude reports where Claude's folders are.
type Claude struct {
	Home Location `json:"home"`
	// Desktop lists the application's data folders, one entry per real folder.
	// Usually there is one, but an old classic installation can sit next to a
	// Store one, and then both hold data.
	Desktop []Location `json:"desktop"`
}

// Locate resolves the manifest's main folders.
func Locate(manifest *manifests.Manifest, env Env) Claude {
	var found Claude
	if path, ok := env.Expand(manifest.Claude.Home); ok {
		found.Home = locate(path)
	}
	for _, pattern := range manifest.Desktop.Roots {
		path, ok := env.Expand(pattern)
		if !ok {
			continue
		}
		for _, candidate := range expandStars(path) {
			if !hasMarker(candidate, manifest.Desktop.Markers) || known(found.Desktop, candidate, env) {
				continue
			}
			found.Desktop = append(found.Desktop, Location{Path: candidate, Exists: true})
		}
	}
	return found
}

func known(locations []Location, path string, env Env) bool {
	for _, location := range locations {
		if env.sameDir(location.Path, path) {
			return true
		}
	}
	return false
}

func hasMarker(dir string, markers []string) bool {
	for _, marker := range markers {
		if _, err := os.Lstat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// expandStars resolves * in place of a whole folder name. The rest of the path
// is taken literally: a user name may hold characters that mean something to
// filepath.Glob.
func expandStars(path string) []string {
	volume := filepath.VolumeName(path)
	parts := strings.Split(strings.TrimPrefix(path, volume), string(filepath.Separator))
	paths := []string{volume}
	for i, part := range parts {
		switch {
		case part == "" && i == 0:
			paths[0] += string(filepath.Separator)
		case part == "":
		case part == "*":
			paths = childrenOf(paths)
		default:
			for j := range paths {
				paths[j] = filepath.Join(paths[j], part)
			}
		}
	}
	return paths
}

func childrenOf(parents []string) []string {
	var children []string
	for _, parent := range parents {
		entries, err := os.ReadDir(parent)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				children = append(children, filepath.Join(parent, entry.Name()))
			}
		}
	}
	sort.Strings(children)
	return children
}

func locate(path string) Location {
	info, err := os.Stat(path)
	return Location{Path: path, Exists: err == nil && info.IsDir()}
}
