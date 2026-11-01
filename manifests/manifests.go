// Package manifests embeds what BackUpClaude knows about Claude's data on each
// OS. Paths of Claude's data live here and nowhere else in the code.
package manifests

import (
	"bytes"
	"embed"
	"encoding/json"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

//go:embed windows.json darwin.json linux.json
var files embed.FS

const schemaVersion = 2

// Manifest lists Claude's data locations for one OS. Paths use forward slashes
// and may start with ~/ or contain %VAR%.
//
// Both Claude folders are saved whole except for their exclude lists, so a
// folder a future Claude version adds is saved rather than silently lost.
// Include names what the snapshot check looks for; it is not a filter.
type Manifest struct {
	SchemaVersion int      `json:"schema_version"`
	OS            string   `json:"os"`
	Claude        Code     `json:"claude"`
	Desktop       Desktop  `json:"desktop"`
	Never         []string `json:"never"`
	Projects      Projects `json:"projects"`
}

// Code describes Claude Code's data.
type Code struct {
	Home string `json:"home"`
	// Transcripts is the transcripts folder, relative to Home.
	Transcripts string `json:"transcripts"`
	// Files are optional files outside Home.
	Files    []string `json:"files"`
	Include  []string `json:"include"`
	Required []string `json:"required"`
	Exclude  []string `json:"exclude"`
}

// Desktop describes the desktop application's data.
type Desktop struct {
	// Roots are candidate folders, the most stable path first. An entry may
	// contain * in place of one folder name.
	Roots []string `json:"roots"`
	// Markers tell a data folder from an unrelated folder of the same name: at
	// least one of them must exist inside.
	Markers []string `json:"markers"`
	// SessionDirs hold session descriptions, relative to a root.
	SessionDirs []string `json:"session_dirs"`
	// CoworkDir is the SessionDirs entry with agent mode sessions.
	CoworkDir string   `json:"cowork_dir"`
	Include   []string `json:"include"`
	Exclude   []string `json:"exclude"`
	// LockedOK names items whose locked files are a warning, not a failure.
	LockedOK []string `json:"locked_ok"`
}

// Projects says what counts as a project and what to leave out of one.
type Projects struct {
	// NotProjects are folders whose whole tree is never a project.
	NotProjects []string `json:"not_projects"`
	// WorktreeDirs are folders, relative to a project, that hold its worktrees.
	WorktreeDirs []string `json:"worktree_dirs"`
	Exclude      []string `json:"exclude"`
	// ExcludeInGit is left out only inside a git repository: elsewhere a folder
	// such as build may be the only copy of the work.
	ExcludeInGit    []string `json:"exclude_in_git"`
	MediaExtensions []string `json:"media_extensions"`
}

// Load returns the embedded manifest for a GOOS value.
func Load(goos string) (*Manifest, error) {
	data, err := files.ReadFile(goos + ".json")
	if err != nil {
		return nil, i18n.E("manifest.unsupported_os", goos)
	}
	var manifest Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return nil, i18n.E("manifest.invalid", goos).Because(err)
	}
	if manifest.SchemaVersion != schemaVersion || manifest.OS != goos ||
		manifest.Claude.Home == "" || manifest.Claude.Transcripts == "" {
		return nil, i18n.E("manifest.invalid", goos)
	}
	return &manifest, nil
}
