// Package profile turns the manifest, the settings and discovered projects into
// what a backup run actually saves.
package profile

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Profile names. They double as snapshot tags.
const (
	Claude   = "claude"
	Projects = "projects"
)

// Names lists the profiles in the order a full run takes them.
func Names() []string { return []string{Claude, Projects} }

// Warning codes. Each has a locale key plan.warning.<code>.
const (
	WarnRequiredMissing = "required_missing"
	WarnClaudeMissing   = "claude_missing"
	WarnDesktopAbsent   = "desktop_absent"
	WarnProjectMissing  = "project_missing"
	WarnExtraMissing    = "extra_missing"
	WarnUnreadable      = "unreadable"
	WarnStateRecovered  = "state_recovered"
	WarnNoCwd           = "no_cwd"
	WarnBadExclude      = "bad_exclude"
)

// WarningCodes lists every warning, for the locale tests.
func WarningCodes() []string {
	return []string{
		WarnRequiredMissing, WarnClaudeMissing, WarnDesktopAbsent, WarnProjectMissing, WarnExtraMissing,
		WarnUnreadable, WarnStateRecovered, WarnNoCwd, WarnBadExclude,
	}
}

// Warning is something the user should know about a plan.
type Warning struct {
	Code string `json:"code"`
	Path string `json:"path,omitempty"`
}

// Item is one folder or file of a plan, with why it is there.
type Item struct {
	Path string `json:"path"`
	// Source is "manifest", "project" or "extra".
	Source string `json:"source"`
}

// Item sources.
const (
	SourceManifest = "manifest"
	SourceProject  = "project"
	SourceExtra    = "extra"
)

// Left is a project that gets no path of its own, with the reason.
type Left struct {
	Path string `json:"path"`
	// Inside names the saved folder that already holds it. It is empty when the
	// settings exclude the project.
	Inside string `json:"inside,omitempty"`
}

// Plan is what one run of a profile saves.
type Plan struct {
	Profile  string             `json:"profile"`
	Items    []Item             `json:"items"`
	Excludes []string           `json:"excludes"`
	Warnings []Warning          `json:"warnings,omitempty"`
	Skipped  []discover.Skipped `json:"skipped,omitempty"`
	Left     []Left             `json:"left,omitempty"`

	rules    rules
	manifest *manifests.Manifest
	state    *discover.State
	lockedOK []string
}

// Paths returns what restic gets as sources.
func (p *Plan) Paths() []string {
	paths := make([]string, len(p.Items))
	for i, item := range p.Items {
		paths[i] = item.Path
	}
	return paths
}

// Known returns the remembered projects after discovery.
func (p *Plan) Known() []discover.Project {
	if p.state == nil {
		return nil
	}
	return p.state.Projects
}

// TranscriptsInSnapshot returns the transcripts folder as a snapshot spells it.
func TranscriptsInSnapshot(manifest *manifests.Manifest, found discover.Claude) string {
	return restore.SnapshotPath(filepath.Join(found.Home.Path, filepath.FromSlash(manifest.Claude.Transcripts)))
}

// Inputs is everything a plan depends on.
type Inputs struct {
	Manifest *manifests.Manifest
	Env      discover.Env
	Config   *config.Config
	Layout   config.Layout
	Now      time.Time
}

// Build makes the plan of a profile. It changes nothing on disk except that a
// damaged list of projects is set aside; see SaveState.
func Build(name string, in Inputs) (*Plan, error) {
	switch name {
	case Claude:
		return buildClaude(in), nil
	case Projects:
		return buildProjects(in)
	}
	return nil, i18n.E("backup.unknown_profile", name, strings.Join(Names(), ", ")).Hint("backup.unknown_profile.hint").Exit(2)
}

func newPlan(name string, in Inputs) *Plan {
	return &Plan{Profile: name, manifest: in.Manifest}
}

func (p *Plan) warn(code, path string) {
	p.Warnings = append(p.Warnings, Warning{Code: code, Path: path})
}

func (p *Plan) addRoot(path, source string) {
	p.Items = append(p.Items, Item{Path: path, Source: source})
	p.rules.roots = append(p.rules.roots, path)
}

func (p *Plan) finish() {
	p.Excludes = p.rules.patterns()
}

func buildClaude(in Inputs) *Plan {
	plan := newPlan(Claude, in)
	manifest := in.Manifest
	plan.rules.everywhere = append(plan.rules.everywhere, manifest.Never...)
	found := discover.Locate(manifest, in.Env)

	if found.Home.Exists {
		plan.addRoot(found.Home.Path, SourceManifest)
		for _, name := range manifest.Claude.Exclude {
			plan.rules.anchored = append(plan.rules.anchored, anchoredName{found.Home.Path, name})
		}
		for _, name := range manifest.Claude.Required {
			path := filepath.Join(found.Home.Path, filepath.FromSlash(name))
			if _, err := os.Lstat(path); err != nil {
				plan.warn(WarnRequiredMissing, path)
			}
		}
	} else {
		plan.warn(WarnClaudeMissing, found.Home.Path)
	}
	for _, path := range in.Env.ExpandAll(manifest.Claude.Files) {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			plan.Items = append(plan.Items, Item{Path: path, Source: SourceManifest})
		}
	}
	if len(found.Desktop) == 0 {
		plan.warn(WarnDesktopAbsent, "")
	}
	for _, root := range found.Desktop {
		plan.addRoot(root.Path, SourceManifest)
		for _, name := range manifest.Desktop.Exclude {
			plan.rules.anchored = append(plan.rules.anchored, anchoredName{root.Path, name})
		}
		for _, name := range manifest.Desktop.LockedOK {
			plan.lockedOK = append(plan.lockedOK, key(filepath.Join(root.Path, filepath.FromSlash(name))))
		}
	}
	plan.finish()
	return plan
}

func buildProjects(in Inputs) (*Plan, error) {
	plan := newPlan(Projects, in)
	manifest := in.Manifest
	state, recovered, err := discover.LoadState(in.Layout.ProjectsFile())
	if err != nil {
		return nil, err
	}
	if recovered != "" {
		plan.warn(WarnStateRecovered, recovered)
	}
	sessions, skipped := discover.ScanProjects(manifest, in.Env, state, in.Now)
	plan.state = state
	plan.Skipped = skipped
	for _, path := range sessions.Unreadable {
		plan.warn(WarnUnreadable, path)
	}
	if sessions.NoCwd > 0 {
		plan.warn(WarnNoCwd, "")
	}

	excludes := splitExcludes(plan, in)
	candidates := plan.projectCandidates(state, in, excludes)
	plan.addCandidates(candidates)

	plan.rules.everywhere = append(append(plan.rules.everywhere, manifest.Never...), manifest.Projects.Exclude...)
	plan.rules.everywhere = append(plan.rules.everywhere, excludes.names...)
	plan.rules.literal = append(plan.rules.literal, excludes.paths...)
	plan.rules.anchored = append(plan.rules.anchored, excludes.anchored...)
	plan.rules.literal = append(plan.rules.literal, in.Config.Repositories.Local, in.Layout.Root)
	plan.rules.repoNames = manifest.Projects.ExcludeInGit
	matcher := plan.rules.matcher()
	for _, root := range plan.rules.roots {
		plan.rules.repos = append(plan.rules.repos, findRepos(root, matcher)...)
	}
	plan.finish()
	return plan, nil
}

type candidate struct {
	path   string
	source string
}

// projectCandidates joins remembered projects and the user's own folders.
func (p *Plan) projectCandidates(state *discover.State, in Inputs, excludes userExcludes) []candidate {
	filter := discover.NewFilter(in.Manifest, in.Env)
	var candidates []candidate
	for _, project := range state.Projects {
		path, reason := filter.Classify(project.Path)
		switch {
		case reason != "":
			continue
		case excludes.covers(path):
			p.Left = append(p.Left, Left{Path: path})
		case !isThere(path):
			p.warn(WarnProjectMissing, path)
		default:
			candidates = append(candidates, candidate{path, SourceProject})
		}
	}
	for _, extra := range in.Config.Sources.Extra {
		path, ok := in.Env.Expand(extra)
		if !ok || !isThere(path) {
			p.warn(WarnExtraMissing, extra)
			continue
		}
		candidates = append(candidates, candidate{filepath.Clean(path), SourceExtra})
	}
	return candidates
}

func isThere(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// addCandidates keeps the outermost folders: restic must not walk a project
// twice because it sits inside another one.
func (p *Plan) addCandidates(candidates []candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		return strings.ToLower(candidates[i].path) < strings.ToLower(candidates[j].path)
	})
	var kept []candidate
	for _, item := range candidates {
		if outer := outerFolder(kept, item.path); outer != "" {
			if !discover.Covers(item.path, outer) {
				p.Left = append(p.Left, Left{Path: item.path, Inside: outer})
			}
			continue
		}
		kept = append(kept, item)
		if info, err := os.Stat(item.path); err == nil && info.IsDir() {
			p.addRoot(item.path, item.source)
		} else {
			p.Items = append(p.Items, Item{Path: item.path, Source: item.source})
		}
	}
}

func outerFolder(kept []candidate, path string) string {
	for _, item := range kept {
		if discover.Covers(item.path, path) {
			return item.path
		}
	}
	return ""
}

// userExcludes sorts sources.exclude by what each entry means.
type userExcludes struct {
	paths    []string
	names    []string
	anchored []anchoredName
}

// splitExcludes reads sources.exclude: a full path or ~/path is that folder or
// file, a * in its last part is a mask there, anything else is a name at any
// depth. Anything else, such as a mask in the middle of a path, is reported and
// ignored: a size measurement and a snapshot check could not follow it.
func splitExcludes(plan *Plan, in Inputs) userExcludes {
	var excludes userExcludes
	for _, pattern := range in.Config.Sources.Exclude {
		path, ok := in.Env.Expand(pattern)
		if !ok {
			plan.warn(WarnBadExclude, pattern)
			continue
		}
		switch {
		case !filepath.IsAbs(path):
			if strings.ContainsAny(path, `/\`) {
				plan.warn(WarnBadExclude, pattern)
				continue
			}
			excludes.names = append(excludes.names, path)
		case strings.ContainsAny(filepath.Dir(path), "*?"):
			plan.warn(WarnBadExclude, pattern)
		case strings.ContainsAny(filepath.Base(path), "*?"):
			excludes.anchored = append(excludes.anchored, anchoredName{filepath.Dir(path), filepath.Base(path)})
		default:
			excludes.paths = append(excludes.paths, filepath.Clean(path))
		}
	}
	return excludes
}

func (e userExcludes) covers(path string) bool {
	for _, excluded := range e.paths {
		if discover.Covers(excluded, path) {
			return true
		}
	}
	return false
}

const gitFolder = ".git"

// repoSearchDepth is how far below a saved folder a git repository is looked
// for. Going deeper would mean a second full walk of a slow disk; a repository
// that is missed keeps its build folders in the backup, which costs only space.
const repoSearchDepth = 3

func findRepos(root string, m *matcher) []string {
	var repos []string
	var visit func(dir string, depth int)
	visit = func(dir string, depth int) {
		if _, err := os.Stat(filepath.Join(dir, gitFolder)); err == nil {
			repos = append(repos, dir)
		}
		if depth == repoSearchDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		parent := key(dir)
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() != gitFolder && !m.excluded(parent, entry.Name()) {
				visit(filepath.Join(dir, entry.Name()), depth+1)
			}
		}
	}
	visit(root, 0)
	return repos
}

// SaveState remembers the projects found. Dry runs do not call it.
func (p *Plan) SaveState(in Inputs) error {
	if p.state == nil {
		return nil
	}
	return p.state.Save(in.Layout.ProjectsFile())
}

// WriteExcludeFile stores the patterns for restic. Environment variables are
// expanded in such files, so a dollar in a path has to be doubled.
func (p *Plan) WriteExcludeFile(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", i18n.E("backup.exclude_file_failed", dir).Because(err)
	}
	file, err := os.CreateTemp(dir, "excludes-*.txt")
	if err != nil {
		return "", i18n.E("backup.exclude_file_failed", dir).Because(err)
	}
	writer := bufio.NewWriter(file)
	for _, pattern := range p.Excludes {
		_, _ = writer.WriteString(strings.ReplaceAll(pattern, "$", "$$") + "\n")
	}
	flushErr := writer.Flush()
	closeErr := file.Close()
	if flushErr != nil || closeErr != nil {
		_ = os.Remove(file.Name())
		return "", i18n.E("backup.exclude_file_failed", dir)
	}
	return file.Name(), nil
}

// Options renders the plan for restic.
func (p *Plan) Options(excludeFile string, dryRun bool) engine.BackupOptions {
	return engine.BackupOptions{
		Paths:       p.Paths(),
		Tags:        []string{p.Profile},
		ExcludeFile: excludeFile,
		GroupByTags: true,
		DryRun:      dryRun,
	}
}

// SortSkipped splits files restic could not read into those the manifest allows
// to be locked and the rest. The rest mean a real gap in the snapshot.
func (p *Plan) SortSkipped(items []engine.ItemError) (locked, skipped []engine.ItemError) {
	for _, item := range items {
		if p.lockedAllowed(item.Item) {
			locked = append(locked, item)
		} else {
			skipped = append(skipped, item)
		}
	}
	return locked, skipped
}

func (p *Plan) lockedAllowed(path string) bool {
	itemKey := key(path)
	for _, allowed := range p.lockedOK {
		if itemKey == allowed || strings.HasPrefix(itemKey, allowed+"/") {
			return true
		}
	}
	return false
}
