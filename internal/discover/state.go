package discover

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

const stateVersion = 1

// Project is a working folder remembered from transcripts.
type Project struct {
	Path      string    `json:"path"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Sessions  int       `json:"sessions"`
}

// State is the remembered list of projects. A project stays on it after the
// sessions that named it are gone: Claude Code deletes old transcripts, and that
// must not end the project's backups.
type State struct {
	Version  int       `json:"version"`
	Projects []Project `json:"projects"`
}

// LoadState reads the list. A damaged file is kept under a new name and the list
// starts over, because refusing would stop every backup; recovered then names
// the kept file so the user can be told.
func LoadState(path string) (state *State, recovered string, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Version: stateVersion}, "", nil
	}
	if err != nil {
		return nil, "", i18n.E("projects.state_read_failed", path).Because(err)
	}
	var parsed State
	if json.Unmarshal(data, &parsed) != nil || parsed.Version != stateVersion {
		kept := path + ".damaged-" + time.Now().UTC().Format("20060102-150405")
		if err := os.Rename(path, kept); err != nil {
			return nil, "", i18n.E("projects.state_read_failed", path).Because(err)
		}
		return &State{Version: stateVersion}, kept, nil
	}
	return &parsed, "", nil
}

// Save writes the list atomically.
func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return i18n.E("projects.state_write_failed", path).Because(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return i18n.E("projects.state_write_failed", path).Because(err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".projects-*.tmp")
	if err != nil {
		return i18n.E("projects.state_write_failed", path).Because(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return i18n.E("projects.state_write_failed", path).Because(err)
	}
	if err := tmp.Close(); err != nil {
		return i18n.E("projects.state_write_failed", path).Because(err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return i18n.E("projects.state_write_failed", path).Because(err)
	}
	return nil
}

// Skipped is a working folder that is not a project, with the reason.
type Skipped struct {
	Path     string `json:"path"`
	Reason   string `json:"reason"`
	Sessions int    `json:"sessions"`
}

// Merge adds what sessions name to the list and returns the folders left out.
// It never removes a project. Sessions counts only what exists now.
func (s *State) Merge(workdirs map[string]int, filter *Filter, now time.Time) []Skipped {
	byKey := map[string]int{}
	for i := range s.Projects {
		s.Projects[i].Sessions = 0
		byKey[pathKey(s.Projects[i].Path)] = i
	}
	skipped := map[string]*Skipped{}
	for workdir, count := range workdirs {
		project, reason := filter.Classify(workdir)
		if reason != "" {
			key := reason + "\x00" + pathKey(workdir)
			if skipped[key] == nil {
				skipped[key] = &Skipped{Path: workdir, Reason: reason}
			}
			skipped[key].Sessions += count
			continue
		}
		index, ok := byKey[pathKey(project)]
		if !ok {
			s.Projects = append(s.Projects, Project{Path: project, FirstSeen: now})
			index = len(s.Projects) - 1
			byKey[pathKey(project)] = index
		}
		s.Projects[index].LastSeen = now
		s.Projects[index].Sessions += count
	}
	sort.Slice(s.Projects, func(i, j int) bool { return s.Projects[i].Path < s.Projects[j].Path })
	left := make([]Skipped, 0, len(skipped))
	for _, item := range skipped {
		left = append(left, *item)
	}
	sort.Slice(left, func(i, j int) bool { return left[i].Path < left[j].Path })
	return left
}

// ScanProjects is the whole discovery pass: it merges what the sessions on this
// computer name into a list.
func ScanProjects(manifest *manifests.Manifest, env Env, state *State, now time.Time) (*Sessions, []Skipped) {
	filter := NewFilter(manifest, env)
	sessions := ReadSessions(manifest, Locate(manifest, env), filter)
	return sessions, state.Merge(sessions.Workdirs, filter, now)
}

// Covers reports whether inner is the same folder as outer or lies below it.
func Covers(outer, inner string) bool {
	outerKey, innerKey := pathKey(outer), pathKey(inner)
	return outerKey == innerKey || isInside(innerKey, outerKey)
}
