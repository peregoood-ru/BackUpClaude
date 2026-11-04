package profile

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
)

// Row statuses of a snapshot check.
const (
	RowOK      = "ok"
	RowMissing = "missing"
	RowAbsent  = "absent_on_disk"
)

// Row is one saved item and what the snapshot holds for it.
type Row struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Files  int64  `json:"files"`
	Bytes  int64  `json:"bytes"`
}

// Forbidden is something that must not be in a snapshot and is.
type Forbidden struct {
	Path string `json:"path"`
}

// Check is the result of reading a snapshot's file tree. Nothing in it comes
// from the settings: every number is counted from the snapshot itself.
type Check struct {
	Profile   string      `json:"profile"`
	Snapshot  string      `json:"snapshot"`
	Files     int64       `json:"files"`
	Bytes     int64       `json:"bytes"`
	Rows      []Row       `json:"rows"`
	Forbidden []Forbidden `json:"forbidden"`
	// Sought counts the names searched for at any depth.
	Sought map[string]int64 `json:"sought"`
	OK     bool             `json:"ok"`
}

const maxForbiddenListed = 100

// expectedRows lists what the snapshot must hold: every item directly inside a
// Claude folder that the rules do not exclude, and every project or own folder
// whole. It looks at the disk, so something a new Claude version added counts.
func (p *Plan) expectedRows() []Row {
	m := p.rules.matcher()
	var rows []Row
	for _, item := range p.Items {
		info, err := os.Stat(item.Path)
		if err != nil {
			rows = append(rows, Row{Path: item.Path, Status: RowAbsent})
			continue
		}
		if !info.IsDir() || p.Profile != Claude {
			rows = append(rows, Row{Path: item.Path, Status: RowMissing})
			continue
		}
		entries, err := os.ReadDir(item.Path)
		if err != nil {
			rows = append(rows, Row{Path: item.Path, Status: RowMissing})
			continue
		}
		parent := key(item.Path)
		for _, entry := range entries {
			if !m.excluded(parent, entry.Name()) {
				rows = append(rows, Row{Path: filepath.Join(item.Path, entry.Name()), Status: RowMissing})
			}
		}
	}
	return rows
}

// Verify reads a snapshot through ls and compares it with the plan. Any item
// named in find counts against the snapshot wherever it turns up.
func (p *Plan) Verify(ctx context.Context, snapshot string, find []string, ls func(context.Context, string, []string, func(engine.Node)) error) (*Check, error) {
	check := &Check{Profile: p.Profile, Snapshot: snapshot, Rows: p.expectedRows(), Forbidden: []Forbidden{}, Sought: map[string]int64{}}
	index := make(map[string]int, len(check.Rows))
	for i := range check.Rows {
		index[key(check.Rows[i].Path)] = i
	}
	sought := map[string]string{}
	for _, name := range p.searchedNames(find) {
		if !strings.Contains(name, "*") {
			sought[strings.ToLower(name)] = name
			check.Sought[name] = 0
		}
	}
	rules := p.rules.matcher()
	err := ls(ctx, snapshot, nil, func(node engine.Node) {
		lower := strings.ToLower(node.Path)
		if node.Type == engine.NodeFile {
			check.Files++
			check.Bytes += node.Size
		}
		if i, ok := rowFor(index, lower); ok {
			check.Rows[i].Status = RowOK
			if node.Type == engine.NodeFile {
				check.Rows[i].Files++
				check.Rows[i].Bytes += node.Size
			}
		}
		name, found := sought[strings.ToLower(node.Name)]
		if found {
			check.Sought[name]++
		}
		parent := lower[:max(strings.LastIndexByte(lower, '/'), 0)]
		if (found || rules.excluded(parent, node.Name)) && len(check.Forbidden) < maxForbiddenListed {
			check.Forbidden = append(check.Forbidden, Forbidden{Path: node.Path})
		}
	})
	if err != nil {
		return nil, err
	}
	check.OK = len(check.Forbidden) == 0
	for _, row := range check.Rows {
		if row.Status == RowMissing {
			check.OK = false
		}
	}
	sort.SliceStable(check.Rows, func(i, j int) bool { return check.Rows[i].Bytes > check.Rows[j].Bytes })
	return check, nil
}

// rowFor finds the row a snapshot path belongs to by climbing its parents.
func rowFor(index map[string]int, path string) (int, bool) {
	for path != "" {
		if i, ok := index[path]; ok {
			return i, true
		}
		cut := strings.LastIndexByte(path, '/')
		if cut < 0 {
			break
		}
		path = path[:cut]
	}
	return 0, false
}

// searchedNames are names that must not appear at any depth, joined with what
// the caller asks to look for.
func (p *Plan) searchedNames(find []string) []string {
	names := append(append([]string{}, p.manifest.Never...), find...)
	if p.Profile == Projects {
		names = append(names, p.manifest.Projects.Exclude...)
	}
	return names
}
