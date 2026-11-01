// Package discover finds Claude's data on this computer. Stage 1 locates the
// main folders; stage 2 adds the walk over transcripts and projects.
package discover

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Env gives access to the home folder and environment variables, so tests can
// point discovery at a fake ~/.claude.
type Env struct {
	Home   string
	Lookup func(name string) string
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

// Expand turns a manifest path into a path of this computer. It returns false
// when the path refers to a variable that is not set.
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
	if rest, found := strings.CutPrefix(expanded, "~/"); found {
		expanded = filepath.Join(e.Home, filepath.FromSlash(rest))
	}
	return filepath.FromSlash(expanded), true
}

// Location is a place from the manifest and whether it exists here.
type Location struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// Claude reports where Claude's folders are.
type Claude struct {
	Home    Location   `json:"home"`
	Desktop []Location `json:"desktop"`
}

// Locate resolves the manifest's main folders.
func Locate(manifest *manifests.Manifest, env Env) Claude {
	var found Claude
	if path, ok := env.Expand(manifest.ClaudeHome); ok {
		found.Home = locate(path)
	}
	for _, root := range manifest.DesktopRoots {
		if path, ok := env.Expand(root); ok {
			found.Desktop = append(found.Desktop, locate(path))
		}
	}
	return found
}

func locate(path string) Location {
	info, err := os.Stat(path)
	return Location{Path: path, Exists: err == nil && info.IsDir()}
}
