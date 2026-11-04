// Package watch notices trouble that a backup run does not report by itself:
// sessions that vanished from Claude's folder, a folder of Claude's that moved
// or disappeared after an update, and a job that keeps failing. It keeps a small
// state file between runs and decides what is worth telling the user; sending
// the message is not its business.
package watch

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const stateVersion = 1

// MinBaseline is the smallest earlier count that can raise a deletion alarm. With
// a handful of sessions one deleted file is already a "drop" of a fifth, and the
// alarm would only teach the user to ignore it.
const MinBaseline = 5

// Count is the number of transcripts seen at some moment.
type Count struct {
	Time  time.Time `json:"time"`
	Files int       `json:"files"`
}

// JobState is what is known about a job that failed.
type JobState struct {
	// Failing is true from the first failure until a run succeeds.
	Failing bool `json:"failing"`
	// Told is when the user last heard about this failure.
	Told time.Time `json:"told,omitzero"`
}

// State is the content of the state file.
type State struct {
	Version int `json:"version"`
	// Transcripts is the count at the previous run of the claude job.
	Transcripts *Count `json:"transcripts,omitempty"`
	// Folders are the important folders seen at the previous run.
	Folders []string `json:"folders,omitempty"`
	// Jobs is the failure state of each job.
	Jobs map[string]JobState `json:"jobs,omitempty"`
	// Alerts are the latest alarms of this package, for the status page.
	Alerts []Alert `json:"alerts,omitempty"`
}

// Alert kinds.
const (
	AlertDeletion = "deletion"
	AlertFolder   = "folder"
)

// maxAlerts is how many alarms the state remembers.
const maxAlerts = 20

// Alert is an alarm that was raised, in the words the user got.
type Alert struct {
	Time time.Time `json:"time"`
	Kind string    `json:"kind"`
	Text string    `json:"text"`
}

// AddAlert remembers an alarm.
func (s *State) AddAlert(now time.Time, kind, text string) {
	s.Alerts = append(s.Alerts, Alert{Time: now, Kind: kind, Text: text})
	if len(s.Alerts) > maxAlerts {
		s.Alerts = s.Alerts[len(s.Alerts)-maxAlerts:]
	}
}

// ErrDamaged means the state file could not be read. Load still returns an empty
// state, so that watching goes on from scratch.
var ErrDamaged = errors.New("watch state is damaged")

// Load reads the state. A missing file is an empty state; a damaged one is an
// empty state together with ErrDamaged.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Version: stateVersion}, nil
	}
	if err != nil {
		return &State{Version: stateVersion}, err
	}
	var state State
	if err := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))).Decode(&state); err != nil || state.Version != stateVersion {
		return &State{Version: stateVersion}, ErrDamaged
	}
	return &state, nil
}

// Save writes the state atomically.
func (s *State) Save(path string) error {
	s.Version = stateVersion
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".watch-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Update loads the state, lets change edit it and saves it. Two runs can overlap
// (an hourly one and a daily one), so every change reads the file again right
// before writing instead of holding a copy for the whole run.
func Update(path string, change func(*State)) error {
	state, err := Load(path)
	if err != nil && !errors.Is(err, ErrDamaged) {
		return err
	}
	change(state)
	return state.Save(path)
}

// CountTranscripts counts the .jsonl files below root, subagent transcripts
// included: a vanished folder of sessions takes all of them along.
func CountTranscripts(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil // an unreadable subfolder must not hide the rest
		}
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jsonl") {
			count++
		}
		return nil
	})
	return count, err
}

// Drop is the comparison of the number of transcripts with the previous one.
type Drop struct {
	Previous Count
	Current  int
	// Percent is the share lost, 0 when nothing was lost.
	Percent float64
	// Alarm is true when the share lost is above the threshold.
	Alarm bool
}

// Deletion compares the current count with the one the last run saw, without
// changing the state: the caller commits the new count with SetTranscripts only
// once the alarm, if any, has reached the user.
func (s *State) Deletion(current int, thresholdPercent int) Drop {
	drop := Drop{Current: current}
	if s.Transcripts == nil {
		return drop
	}
	drop.Previous = *s.Transcripts
	if previous := s.Transcripts.Files; previous > 0 && current < previous {
		drop.Percent = float64(previous-current) * 100 / float64(previous)
		drop.Alarm = previous >= MinBaseline && drop.Percent > float64(thresholdPercent)
	}
	return drop
}

// SetTranscripts remembers the current count as the one to compare with next time.
func (s *State) SetTranscripts(now time.Time, files int) {
	s.Transcripts = &Count{Time: now, Files: files}
}

// Vanished lists the folders seen at the previous run that are not there now.
func (s *State) Vanished(present []string) []string {
	var gone []string
	for _, folder := range s.Folders {
		if !slices.Contains(present, folder) {
			gone = append(gone, folder)
		}
	}
	return gone
}

// SetFolders remembers the folders that exist now. A folder reported as vanished
// is dropped here, so that the user hears about it once and not every hour.
func (s *State) SetFolders(present []string) {
	s.Folders = slices.Clone(present)
}

// reminderEvery is how often a job that keeps failing is mentioned again.
const reminderEvery = 24 * time.Hour

// ShouldTellFailure says whether a failure of job is news: the first failure
// after a success is, and a failure that goes on is mentioned again once a day
// instead of every run. It changes nothing; call JobTold after the message went.
func (s *State) ShouldTellFailure(job string, now time.Time) bool {
	state := s.Jobs[job]
	return !state.Failing || now.Sub(state.Told) >= reminderEvery
}

// JobTold records that the user heard about a failure of job.
func (s *State) JobTold(job string, now time.Time) {
	if s.Jobs == nil {
		s.Jobs = map[string]JobState{}
	}
	s.Jobs[job] = JobState{Failing: true, Told: now}
}

// JobSucceeded clears the failure of job.
func (s *State) JobSucceeded(job string) {
	delete(s.Jobs, job)
}
