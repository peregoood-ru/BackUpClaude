package restore

import (
	"context"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Repo is what a restore needs from a repository. *engine.Restic has it; the
// tests give a fake.
type Repo interface {
	Snapshots(ctx context.Context, tag string) ([]engine.Snapshot, error)
	Nodes(ctx context.Context, snapshot string, paths []string, recursive bool, onNode func(engine.Node)) error
	Find(ctx context.Context, patterns []string, tag string, snapshots []string) ([]engine.FindResult, error)
	Restore(ctx context.Context, opts engine.RestoreOptions) (*engine.RestoreSummary, error)
}

// Profile tags, as the backup writes them.
const (
	TagClaude   = "claude"
	TagProjects = "projects"
)

// Modes of a restore.
const (
	ModeSession = "session"
	ModeProject = "project"
	ModeAll     = "all"
)

// Piece is one part of a snapshot to bring back: the folder Root (a path in
// restic form), whole, or only the entries of it listed in Names.
type Piece struct {
	Snapshot string   `json:"snapshot"`
	Profile  string   `json:"profile"`
	Root     string   `json:"root"`
	Names    []string `json:"names,omitempty"`
}

// Origin says where Claude's data lived on the computer that made a snapshot.
// All paths are in restic form.
type Origin struct {
	Host string `json:"host"`
	// Home is the user's home folder, Claude the ~/.claude folder in it.
	Home   string `json:"home"`
	Claude string `json:"claude"`
	// Desktop lists the data folders of the desktop application.
	Desktop []string `json:"desktop,omitempty"`
}

// Transcripts is the folder of Claude Code's transcripts.
func (o Origin) Transcripts(manifest *manifests.Manifest) string {
	if o.Claude == "" {
		return ""
	}
	return path.Join(o.Claude, manifest.Claude.Transcripts)
}

// OriginOf reads the origin from the paths of a claude snapshot: ~/.claude is
// the folder with the manifest's name, the home folder holds it, and the other
// folders are the desktop application's.
func OriginOf(manifest *manifests.Manifest, snapshot engine.Snapshot) Origin {
	origin := Origin{Host: snapshot.Hostname}
	claudeName := path.Base(filepath.ToSlash(manifest.Claude.Home))
	var others []string
	for _, source := range snapshot.Paths {
		p := SnapshotPath(source)
		if strings.EqualFold(path.Base(p), claudeName) && origin.Claude == "" {
			origin.Claude = p
			origin.Home = path.Dir(p)
			continue
		}
		others = append(others, p)
	}
	files := map[string]bool{}
	for _, file := range manifest.Claude.Files {
		files[strings.ToLower(path.Base(filepath.ToSlash(file)))] = true
	}
	for _, p := range others {
		if origin.Home != "" && files[strings.ToLower(path.Base(p))] && fold(path.Dir(p)) == fold(origin.Home) {
			continue
		}
		origin.Desktop = append(origin.Desktop, p)
	}
	return origin
}

// Latest returns the newest snapshot with the tag, or nil.
func Latest(snapshots []engine.Snapshot, tag string) *engine.Snapshot {
	var latest *engine.Snapshot
	for i := range snapshots {
		if slices.Contains(snapshots[i].Tags, tag) && (latest == nil || snapshots[i].Time.After(latest.Time)) {
			latest = &snapshots[i]
		}
	}
	return latest
}

// Pick finds the snapshot a person named, by its short or full id, or the
// newest one with the tag when the name is empty.
func Pick(snapshots []engine.Snapshot, id, tag string) (*engine.Snapshot, error) {
	if id == "" || id == "latest" {
		if latest := Latest(snapshots, tag); latest != nil {
			return latest, nil
		}
		return nil, i18n.E("restore.no_snapshots", tag).Hint("restore.no_snapshots.hint")
	}
	var found *engine.Snapshot
	for i := range snapshots {
		if strings.HasPrefix(snapshots[i].ID, strings.ToLower(id)) {
			if found != nil {
				return nil, i18n.E("restore.snapshot_ambiguous", id).Hint("restore.snapshot_unknown.hint")
			}
			found = &snapshots[i]
		}
	}
	if found == nil {
		return nil, i18n.E("restore.snapshot_unknown", id).Hint("restore.snapshot_unknown.hint")
	}
	return found, nil
}

// sessionID is what a session id or the beginning of one may contain.
var sessionID = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z-]{5,63}$`)

const descriptionPrefix = "local_"

// SessionMatch is a session found in a snapshot.
type SessionMatch struct {
	Snapshot engine.Snapshot `json:"-"`
	// IDs are the full ids that matched. More than one means the beginning the
	// person typed is not enough.
	IDs    []string `json:"ids"`
	Pieces []Piece  `json:"pieces"`
	// Descriptions are the restic paths of the application's descriptions of
	// the session; they may name the Claude Code transcript behind it.
	Descriptions []string `json:"descriptions,omitempty"`
}

// FindSession looks for a session in the claude snapshots, newest first, in a
// single run of restic find: a session deleted long ago is still in an older
// snapshot. The parts of a session are its transcript and the folder of the same
// name (subagents, tool output), its file history, and the application's
// description of it with the folder of an agent session.
func FindSession(ctx context.Context, repo Repo, manifest *manifests.Manifest, snapshots []engine.Snapshot, id, snapshotID string) (*SessionMatch, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !sessionID.MatchString(id) {
		return nil, i18n.E("restore.session_bad_id", id).Hint("restore.session_bad_id.hint").Exit(2)
	}
	var only []string
	if snapshotID != "" {
		picked, err := Pick(snapshots, snapshotID, TagClaude)
		if err != nil {
			return nil, err
		}
		only = []string{picked.ID}
	}
	results, err := repo.Find(ctx, []string{id + "*", descriptionPrefix + id + "*"}, TagClaude, only)
	if err != nil {
		return nil, err
	}
	byID := map[string]engine.Snapshot{}
	for _, snapshot := range snapshots {
		byID[snapshot.ID] = snapshot
	}
	var best *SessionMatch
	for _, result := range results {
		snapshot, ok := byID[result.Snapshot]
		if !ok {
			continue
		}
		match := matchSession(manifest, snapshot, result.Matches, id)
		if len(match.IDs) == 0 {
			continue
		}
		if best == nil || snapshot.Time.After(best.Snapshot.Time) {
			best = match
		}
	}
	if best == nil {
		return nil, i18n.E("restore.session_not_found", id).Hint("restore.session_not_found.hint")
	}
	if len(best.IDs) > 1 {
		return nil, i18n.E("restore.session_ambiguous", id, strings.Join(best.IDs, ", ")).Hint("restore.session_ambiguous.hint").Exit(2)
	}
	return best, nil
}

func matchSession(manifest *manifests.Manifest, snapshot engine.Snapshot, nodes []engine.Node, prefix string) *SessionMatch {
	origin := OriginOf(manifest, snapshot)
	match := &SessionMatch{Snapshot: snapshot}
	transcripts := origin.Transcripts(manifest)
	history := ""
	if origin.Claude != "" {
		history = path.Join(origin.Claude, "file-history")
	}
	var sessionDirs []string
	for _, root := range origin.Desktop {
		for _, dir := range manifest.Desktop.SessionDirs {
			sessionDirs = append(sessionDirs, path.Join(root, dir))
		}
	}
	pieces := map[string]*Piece{}
	ids := map[string]bool{}
	add := func(node engine.Node, id string) {
		ids[id] = true
		parent := path.Dir(node.Path)
		piece := pieces[parent]
		if piece == nil {
			piece = &Piece{Snapshot: snapshot.ShortID, Profile: TagClaude, Root: parent}
			pieces[parent] = piece
		}
		if !slices.Contains(piece.Names, node.Name) {
			piece.Names = append(piece.Names, node.Name)
		}
	}
	for _, node := range nodes {
		name := strings.ToLower(node.Name)
		parent := path.Dir(node.Path)
		switch {
		case transcripts != "" && path.Dir(parent) == transcripts && node.Type == engine.NodeFile && strings.HasSuffix(name, ".jsonl"):
			if id := strings.TrimSuffix(name, ".jsonl"); strings.HasPrefix(id, prefix) {
				add(node, id)
			}
		case transcripts != "" && path.Dir(parent) == transcripts && node.Type == engine.NodeDir && strings.HasPrefix(name, prefix):
			add(node, name)
		case history != "" && parent == history && node.Type == engine.NodeDir && strings.HasPrefix(name, prefix):
			add(node, name)
		case strings.HasPrefix(name, descriptionPrefix+prefix) && insideAny(sessionDirs, node.Path):
			id := strings.TrimSuffix(strings.TrimPrefix(name, descriptionPrefix), ".json")
			add(node, id)
			if node.Type == engine.NodeFile {
				match.Descriptions = append(match.Descriptions, node.Path)
			}
		}
	}
	for id := range ids {
		match.IDs = append(match.IDs, id)
	}
	sort.Strings(match.IDs)
	for _, piece := range pieces {
		sort.Strings(piece.Names)
		match.Pieces = append(match.Pieces, *piece)
	}
	sort.Slice(match.Pieces, func(i, j int) bool { return match.Pieces[i].Root < match.Pieces[j].Root })
	return match
}

func insideAny(roots []string, p string) bool {
	for _, root := range roots {
		if strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

// FindProject picks a saved folder of a projects snapshot by what the person
// typed: a full path (also a folder inside a saved one), or a name, first the
// exact folder name and then a part of it. Case does not matter.
func FindProject(snapshot engine.Snapshot, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", i18n.E("restore.project_not_found", query).Hint("restore.project_not_found.hint").Exit(2)
	}
	if filepath.IsAbs(query) {
		wanted := SnapshotPath(filepath.Clean(query))
		for _, source := range snapshot.Paths {
			if Within(SnapshotPath(source), wanted) {
				return wanted, nil
			}
		}
		return "", i18n.E("restore.project_not_found", query).Hint("restore.project_not_found.hint")
	}
	var exact, partial []string
	needle := strings.ToLower(query)
	for _, source := range snapshot.Paths {
		p := SnapshotPath(source)
		base := strings.ToLower(path.Base(p))
		switch {
		case base == needle:
			exact = append(exact, p)
		case strings.Contains(base, needle):
			partial = append(partial, p)
		}
	}
	candidates := exact
	if len(candidates) == 0 {
		candidates = partial
	}
	switch len(candidates) {
	case 0:
		return "", i18n.E("restore.project_not_found", query).Hint("restore.project_not_found.hint")
	case 1:
		return candidates[0], nil
	}
	local := make([]string, len(candidates))
	for i, candidate := range candidates {
		local[i] = LocalPath(candidate)
	}
	return "", i18n.E("restore.project_ambiguous", query, strings.Join(local, "; ")).Hint("restore.project_ambiguous.hint").Exit(2)
}

// PiecesOf turns saved paths of a snapshot into pieces. A folder is restored
// whole; a file through its parent folder, because restic restores only
// folders as a subtree.
func PiecesOf(ctx context.Context, repo Repo, snapshot engine.Snapshot, profile string, roots []string) ([]Piece, error) {
	types := map[string]string{}
	err := repo.Nodes(ctx, snapshot.ID, roots, false, func(node engine.Node) {
		if _, seen := types[node.Path]; !seen {
			types[node.Path] = node.Type
		}
	})
	if err != nil {
		return nil, err
	}
	var pieces []Piece
	files := map[string]int{}
	for _, root := range roots {
		kind, ok := types[root]
		switch {
		case !ok:
			continue // not in this snapshot
		case kind == engine.NodeDir:
			pieces = append(pieces, Piece{Snapshot: snapshot.ShortID, Profile: profile, Root: root})
		default:
			parent := path.Dir(root)
			index, seen := files[parent]
			if !seen {
				index = len(pieces)
				files[parent] = index
				pieces = append(pieces, Piece{Snapshot: snapshot.ShortID, Profile: profile, Root: parent})
			}
			pieces[index].Names = append(pieces[index].Names, path.Base(root))
		}
	}
	return pieces, nil
}

// RootsOf returns the saved paths of a snapshot in restic form.
func RootsOf(snapshot engine.Snapshot) []string {
	roots := make([]string, 0, len(snapshot.Paths))
	for _, source := range snapshot.Paths {
		roots = append(roots, SnapshotPath(source))
	}
	return roots
}
