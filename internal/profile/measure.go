package profile

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HeavyFolder is a folder that holds a lot of media.
type HeavyFolder struct {
	Path string `json:"path"`
	// Media is the size of the media files inside.
	Media int64 `json:"media_bytes"`
	// Bytes and Files count everything inside, media or not: all of it leaves
	// the backup if the folder is excluded.
	Bytes int64 `json:"bytes"`
	Files int64 `json:"files"`
	// Mixed means media is the smaller part: excluding the folder costs more than
	// media.
	Mixed bool `json:"mixed,omitempty"`
}

// RootSize is the size of one saved folder after exclusions.
type RootSize struct {
	Path  string `json:"path"`
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
	Media int64  `json:"media_bytes"`
	// LooseMedia is media no heavy folder accounts for. It stays in the backup.
	LooseMedia int64 `json:"loose_media_bytes"`
}

// Measurement is the size of a plan.
type Measurement struct {
	Files      int64         `json:"files"`
	Bytes      int64         `json:"bytes"`
	Media      int64         `json:"media_bytes"`
	CloudOnly  int64         `json:"cloud_only_files"`
	Unreadable []string      `json:"unreadable,omitempty"`
	Roots      []RootSize    `json:"roots"`
	Heavy      []HeavyFolder `json:"heavy"`
}

const maxUnreadableListed = 50

type folderTotals struct {
	files, bytes, media int64
	hasRepo             bool
	heavy               []HeavyFolder
}

func (t *folderTotals) add(other folderTotals) {
	t.files += other.files
	t.bytes += other.bytes
	t.media += other.media
	t.hasRepo = t.hasRepo || other.hasRepo
	t.heavy = append(t.heavy, other.heavy...)
}

type measurer struct {
	ctx       context.Context
	rules     *matcher
	media     map[string]bool
	threshold int64
	result    *Measurement
}

// Measure walks what the plan saves, applying the same exclusions as the backup.
// A heavy folder is the outermost folder with at least threshold bytes of media
// of which media is the larger part: excluding it loses little else. Where no
// such folder exists, the search goes deeper, and a folder whose media is not
// accounted for by heavy folders below it is reported as mixed. A folder that
// holds a git repository and a project root are never offered; their media shows
// up as loose media.
func (p *Plan) Measure(ctx context.Context, threshold int64) (*Measurement, error) {
	result := &Measurement{Roots: []RootSize{}, Heavy: []HeavyFolder{}}
	m := &measurer{ctx: ctx, rules: p.rules.matcher(), media: map[string]bool{}, threshold: threshold, result: result}
	for _, extension := range p.manifest.Projects.MediaExtensions {
		m.media["."+extension] = true
	}
	for _, item := range p.Items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Lstat(item.Path)
		if err != nil {
			m.note(item.Path)
			continue
		}
		var totals folderTotals
		if info.IsDir() {
			totals = m.walk(item.Path, true)
		} else {
			m.file(item.Path, info, &totals)
		}
		var held int64
		for _, folder := range totals.heavy {
			held += folder.Media
		}
		result.Roots = append(result.Roots, RootSize{
			Path: item.Path, Files: totals.files, Bytes: totals.bytes,
			Media: totals.media, LooseMedia: totals.media - held,
		})
		result.Files += totals.files
		result.Bytes += totals.bytes
		result.Media += totals.media
		result.Heavy = append(result.Heavy, totals.heavy...)
	}
	sort.Slice(result.Heavy, func(i, j int) bool { return result.Heavy[i].Media > result.Heavy[j].Media })
	return result, ctx.Err()
}

func (m *measurer) note(path string) {
	if len(m.result.Unreadable) < maxUnreadableListed {
		m.result.Unreadable = append(m.result.Unreadable, path)
	}
}

func (m *measurer) file(path string, info os.FileInfo, totals *folderTotals) {
	if !info.Mode().IsRegular() {
		return
	}
	totals.files++
	totals.bytes += info.Size()
	if m.media[strings.ToLower(filepath.Ext(path))] {
		totals.media += info.Size()
	}
	if cloudOnly(info) {
		m.result.CloudOnly++
	}
}

func (m *measurer) walk(dir string, root bool) folderTotals {
	var totals folderTotals
	if m.ctx.Err() != nil {
		return totals
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		m.note(dir)
		return totals
	}
	parent := key(dir)
	for _, entry := range entries {
		name := entry.Name()
		if name == gitFolder {
			totals.hasRepo = true
		}
		if m.rules.excluded(parent, name) {
			continue
		}
		path := filepath.Join(dir, name)
		if entry.IsDir() {
			totals.add(m.walk(path, false))
			continue
		}
		info, err := entry.Info()
		if err != nil {
			m.note(path)
			continue
		}
		m.file(path, info, &totals)
	}
	if root || totals.hasRepo {
		return totals
	}
	var held int64
	for _, folder := range totals.heavy {
		held += folder.Media
	}
	mostlyMedia := totals.media >= m.threshold && totals.media*2 >= totals.bytes
	if mostlyMedia || totals.media-held >= m.threshold {
		totals.heavy = []HeavyFolder{{Path: dir, Media: totals.media, Bytes: totals.bytes, Files: totals.files, Mixed: !mostlyMedia}}
	}
	return totals
}
