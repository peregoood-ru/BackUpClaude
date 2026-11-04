package discover

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Kinds of a session.
const (
	KindCode    = "code"
	KindDesktop = "desktop"
	KindCowork  = "cowork"
)

const (
	transcriptExtension = ".jsonl"
	descriptionPrefix   = "local_"
	descriptionSuffix   = ".json"
	maxDescriptionSize  = 32 << 20
)

// Session is one session found on this computer.
type Session struct {
	ID string `json:"id"`
	// DescriptionID is the id in the file name of the application's description
	// of this session, when it differs from ID.
	DescriptionID string    `json:"description_id,omitempty"`
	Kind          string    `json:"kind"`
	Title         string    `json:"title,omitempty"`
	Cwd           string    `json:"cwd,omitempty"`
	Path          string    `json:"path"`
	Size          int64     `json:"size"`
	Modified      time.Time `json:"modified"`
	// Damaged marks a description that exists but cannot be read.
	Damaged bool `json:"damaged,omitempty"`
}

// Sessions is what a pass over Claude's folders finds.
type Sessions struct {
	List []Session
	// Workdirs counts sessions per working folder as written in them.
	Workdirs map[string]int
	// Unreadable names files that could not be read: a project behind one of
	// them may be missing from the list.
	Unreadable []string
	// NoCwd counts transcripts without a working folder.
	NoCwd int
}

// TranscriptsFolder is where Claude Code keeps transcripts.
func TranscriptsFolder(manifest *manifests.Manifest, found Claude) string {
	return filepath.Join(found.Home.Path, filepath.FromSlash(manifest.Claude.Transcripts))
}

// SessionFolder is a folder of session descriptions and the kind they are.
type SessionFolder struct {
	Path string
	Kind string
}

// DescriptionFolders lists where the application describes its sessions.
func DescriptionFolders(manifest *manifests.Manifest, found Claude) []SessionFolder {
	var folders []SessionFolder
	for _, root := range found.Desktop {
		for _, dir := range manifest.Desktop.SessionDirs {
			kind := KindDesktop
			if dir == manifest.Desktop.CoworkDir {
				kind = KindCowork
			}
			folders = append(folders, SessionFolder{filepath.Join(root.Path, filepath.FromSlash(dir)), kind})
		}
	}
	return folders
}

// DescriptionID returns the session id in the name of a description file.
func DescriptionID(name string) (string, bool) {
	if !strings.HasPrefix(name, descriptionPrefix) || !strings.HasSuffix(name, descriptionSuffix) {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(name, descriptionPrefix), descriptionSuffix), true
}

// ReadSessions walks transcripts and the application's session descriptions.
func ReadSessions(manifest *manifests.Manifest, found Claude, filter *Filter) *Sessions {
	sessions := &Sessions{Workdirs: map[string]int{}}
	if found.Home.Exists {
		sessions.readTranscripts(TranscriptsFolder(manifest, found), filter)
	}
	byID := map[string]int{}
	for i, session := range sessions.List {
		byID[session.ID] = i
	}
	for _, folder := range DescriptionFolders(manifest, found) {
		sessions.readDescriptions(folder.Path, folder.Kind, byID)
	}
	sort.Slice(sessions.List, func(i, j int) bool {
		return sessions.List[i].Modified.After(sessions.List[j].Modified)
	})
	return sessions
}

// readTranscripts takes every .jsonl below root. Files right inside a project
// folder are sessions; deeper ones, such as those of subagents, only tell where
// work happened.
func (s *Sessions) readTranscripts(root string, filter *Filter) {
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			s.noteWalkError(path, err)
			return nil
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), transcriptExtension) {
			return nil
		}
		cwd, err := s.scanTranscript(path, filter)
		if err != nil {
			s.Unreadable = append(s.Unreadable, path)
			return nil
		}
		if cwd == "" {
			s.NoCwd++
		}
		if filepath.Dir(filepath.Dir(path)) != root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			s.Unreadable = append(s.Unreadable, path)
			return nil
		}
		s.List = append(s.List, Session{
			ID:   strings.TrimSuffix(entry.Name(), transcriptExtension),
			Kind: KindCode, Cwd: cwd, Path: path, Size: info.Size(), Modified: info.ModTime(),
		})
		return nil
	})
}

// noteWalkError records a folder that could not be walked. A folder that simply
// is not there is normal: not every installation has every folder.
func (s *Sessions) noteWalkError(path string, err error) {
	if !errors.Is(err, fs.ErrNotExist) {
		s.Unreadable = append(s.Unreadable, path)
	}
}

// scanTranscript records where a session worked and returns where it started. A
// session started in a project stops the scan at once, so a big transcript costs
// only its first lines. A session started in the home folder or in Temp is read
// to the end: it often goes on to work in a real project, and such a project may
// have no other trace. Every folder it visits counts.
func (s *Sessions) scanTranscript(path string, filter *Filter) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	first := ""
	seen := map[string]bool{}
	err = ScanCwds(file, func(cwd string) bool {
		if seen[cwd] {
			return false
		}
		seen[cwd] = true
		s.Workdirs[cwd]++
		if first != "" {
			return false
		}
		first = cwd
		_, skip := filter.Classify(cwd)
		return skip == ""
	})
	return first, err
}

// description is the part of a session description we use.
type description struct {
	SessionID string   `json:"sessionId"`
	CLIID     string   `json:"cliSessionId"`
	Title     string   `json:"title"`
	Cwd       string   `json:"cwd"`
	OriginCwd string   `json:"originCwd"`
	Selected  []string `json:"userSelectedFolders"`
}

// readDescriptions takes the application's session files. They name working
// folders too, including folders the user picked for an agent session, and they
// outlive transcripts that Claude Code has cleaned up.
func (s *Sessions) readDescriptions(root, kind string, byID map[string]int) {
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			s.noteWalkError(path, err)
			return nil
		}
		if _, ok := DescriptionID(entry.Name()); entry.IsDir() || !ok {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maxDescriptionSize {
			s.Unreadable = append(s.Unreadable, path)
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			s.Unreadable = append(s.Unreadable, path)
			return nil
		}
		var meta description
		if json.Unmarshal(data, &meta) != nil {
			s.Unreadable = append(s.Unreadable, path)
			id, _ := DescriptionID(entry.Name())
			s.List = append(s.List, Session{ID: id, Kind: kind, Path: path, Size: info.Size(), Modified: info.ModTime(), Damaged: true})
			return nil
		}
		s.addDescription(meta, kind, path, info, byID)
		return nil
	})
}

func (s *Sessions) addDescription(meta description, kind, path string, info fs.FileInfo, byID map[string]int) {
	seen := map[string]bool{}
	for _, cwd := range append([]string{meta.Cwd, meta.OriginCwd}, meta.Selected...) {
		if cwd != "" && !seen[cwd] {
			seen[cwd] = true
			s.Workdirs[cwd]++
		}
	}
	id, _ := DescriptionID(info.Name())
	if index, ok := byID[meta.CLIID]; ok && meta.CLIID != "" {
		s.List[index].Kind = kind
		s.List[index].Title = meta.Title
		s.List[index].DescriptionID = id
		return
	}
	s.List = append(s.List, Session{
		ID: id, Kind: kind, Title: meta.Title, Cwd: meta.Cwd, Path: path, Size: info.Size(), Modified: info.ModTime(),
	})
}
