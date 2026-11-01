package profile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

func inputs(t *testing.T, home string) Inputs {
	t.Helper()
	manifest, err := manifests.Load("windows")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Repositories.Local = filepath.Join(home, "backup", "repo")
	return Inputs{
		Manifest: manifest,
		Env:      discover.Env{Home: home, Lookup: fakehome.Lookup(home)},
		Config:   &cfg,
		Layout:   config.Layout{Root: filepath.Join(home, "data")},
		Now:      time.Unix(1000, 0).UTC(),
	}
}

func build(t *testing.T, name string, in Inputs) *Plan {
	t.Helper()
	plan, err := Build(name, in)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// survivors applies the rules to a fixture folder the way the backup walks it:
// an excluded folder hides everything below it.
func survivors(t *testing.T, plan *Plan, root string) []string {
	t.Helper()
	m := plan.rules.matcher()
	var kept []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if m.excluded(key(filepath.Dir(path)), entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			kept = append(kept, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return kept
}

func hasWarning(plan *Plan, code string) bool {
	for _, warning := range plan.Warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

func TestClaudePlanForEveryInstallation(t *testing.T) {
	cases := []struct {
		name    string
		install fakehome.Install
		roots   []string
	}{
		{"classic", fakehome.Classic, []string{fakehome.ClassicRoot}},
		{"store", fakehome.Store, []string{fakehome.StoreRoot}},
		{"both", fakehome.Both, []string{fakehome.StoreRoot, fakehome.ClassicRoot}},
		{"no desktop application", fakehome.NoDesktop, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := fakehome.Copy(t, tc.install)
			plan := build(t, Claude, inputs(t, home))
			paths := plan.Paths()
			for _, want := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json")} {
				if !slices.Contains(paths, want) {
					t.Errorf("%s is missing from %v", want, paths)
				}
			}
			for _, root := range tc.roots {
				if !slices.Contains(paths, filepath.Join(home, filepath.FromSlash(root))) {
					t.Errorf("%s is missing from %v", root, paths)
				}
			}
			if got := len(paths); got != 2+len(tc.roots) {
				t.Errorf("paths = %v", paths)
			}
			if hasWarning(plan, WarnDesktopAbsent) != (len(tc.roots) == 0) {
				t.Errorf("warnings = %+v", plan.Warnings)
			}
		})
	}
}

func TestClaudeHomeRules(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	plan := build(t, Claude, inputs(t, home))
	kept := survivors(t, plan, filepath.Join(home, ".claude"))
	for _, want := range []string{
		"settings.json", "settings.local.json", "history.jsonl", "file-history/session/abc@v1",
		"skills/demo/SKILL.md", "plugins/known_marketplaces.json", "projects/-work-demo-project/memory/MEMORY.md",
		"brand-new-folder/from-a-future-version.txt", "plugins/demo/cache/stays.txt",
	} {
		if !slices.Contains(kept, want) {
			t.Errorf("%s is lost", want)
		}
	}
	for _, kept := range kept {
		for _, banned := range []string{".credentials.json", "cache/changelog", "telemetry/", "sessions/1", "session-env/", "shell-snapshots/", "daemon.log", "node_modules"} {
			if strings.Contains(kept, banned) && !strings.HasPrefix(kept, "plugins/demo/cache") {
				t.Errorf("%s must stay out", kept)
			}
		}
	}
}

func TestDesktopRules(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Classic)
	plan := build(t, Claude, inputs(t, home))
	root := filepath.Join(home, filepath.FromSlash(fakehome.ClassicRoot))
	kept := strings.Join(survivors(t, plan, root), "\n")
	for _, want := range []string{"claude-code-sessions/", "local-agent-mode-sessions/", "Local Storage/leveldb/", "IndexedDB/", "claude_desktop_config.json"} {
		if !strings.Contains(kept, want) {
			t.Errorf("%s is lost:\n%s", want, kept)
		}
	}
	for _, banned := range []string{"vm_bundles", "Partitions", "Cache", "Code Cache", "claude-code/", "logs", "dxt-install"} {
		if strings.Contains(kept, banned) {
			t.Errorf("%s must stay out:\n%s", banned, kept)
		}
	}
}

func TestEveryPatternIsAnchored(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Both)
	in := inputs(t, home)
	fakehome.Transcript(t, home, "x", "a", filepath.Join(home, "Мои проекты"))
	in.Config.Sources.Exclude = []string{"secret-name"}
	for _, name := range Names() {
		plan := build(t, name, in)
		if len(plan.Excludes) == 0 {
			t.Fatalf("%s has no patterns", name)
		}
		for _, pattern := range plan.Excludes {
			if !filepath.IsAbs(pattern) {
				t.Errorf("a bare name would also match the folders above a project: %q", pattern)
			}
		}
	}
}

func projectsPlan(t *testing.T, home string, change func(Inputs), cwds ...string) *Plan {
	t.Helper()
	for i, cwd := range cwds {
		fakehome.Transcript(t, home, "folder", "session-"+string(rune('a'+i)), cwd)
	}
	in := inputs(t, home)
	if change != nil {
		change(in)
	}
	return build(t, Projects, in)
}

func TestProjectsPlanWithCyrillicAndSpaces(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	repository := filepath.Join(home, filepath.FromSlash(fakehome.Repository))
	plain := filepath.Join(home, filepath.FromSlash(fakehome.PlainFolder))
	plan := projectsPlan(t, home, nil, repository, plain)
	if !slices.Contains(plan.Paths(), repository) || !slices.Contains(plan.Paths(), plain) {
		t.Fatalf("paths = %v", plan.Paths())
	}

	kept := survivors(t, plan, repository)
	if !slices.Contains(kept, "index.html") || !slices.Contains(kept, ".git/HEAD") || !slices.Contains(kept, ".env") {
		t.Errorf("repository lost files: %v", kept)
	}
	if slices.Contains(kept, "dist/bundle.js") || slices.Contains(kept, "node_modules/dep/index.js") {
		t.Errorf("rebuildable folders stayed: %v", kept)
	}

	kept = survivors(t, plan, plain)
	if !slices.Contains(kept, "build/единственная копия.txt") {
		t.Errorf("without git a build folder may be the only copy: %v", kept)
	}
	if slices.Contains(kept, "venv/lib.py") || slices.Contains(kept, ".credentials.json") {
		t.Errorf("banned files stayed: %v", kept)
	}
}

func TestNestedProjectsCollapse(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	outer := filepath.Join(home, "Мои проекты")
	inner := filepath.Join(outer, "Без git")
	plan := projectsPlan(t, home, nil, inner, outer)
	if got := plan.Paths(); len(got) != 1 || got[0] != outer {
		t.Fatalf("paths = %v", got)
	}
	if len(plan.Left) != 1 || plan.Left[0].Path != inner || plan.Left[0].Inside != outer {
		t.Errorf("left = %+v", plan.Left)
	}
}

func TestExtraFolders(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	own := filepath.Join(home, "мои настройки")
	fakehome.Write(t, filepath.Join(own, "config.txt"), "x")
	fakehome.Write(t, filepath.Join(own, "venv", "lib.py"), "x")
	plan := projectsPlan(t, home, func(in Inputs) {
		in.Config.Sources.Extra = []string{"~/мои настройки", filepath.Join(home, "нет такой папки")}
	})
	if got := plan.Paths(); len(got) != 1 || got[0] != own || plan.Items[0].Source != SourceExtra {
		t.Fatalf("items = %+v", plan.Items)
	}
	if !hasWarning(plan, WarnExtraMissing) {
		t.Errorf("warnings = %+v", plan.Warnings)
	}
	if kept := survivors(t, plan, own); len(kept) != 1 || kept[0] != "config.txt" {
		t.Errorf("kept = %v", kept)
	}
}

func TestExtraInsideHomeIsNotFiltered(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	secrets := filepath.Join(home, ".ssh")
	fakehome.Write(t, filepath.Join(secrets, "config"), "x")
	plan := projectsPlan(t, home, func(in Inputs) { in.Config.Sources.Extra = []string{"~/.ssh"} })
	if got := plan.Paths(); len(got) != 1 || got[0] != secrets {
		t.Fatalf("paths = %v", got)
	}
}

func TestProjectWhoseFolderIsGoneIsReportedNotDropped(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	gone := filepath.Join(home, "удалённый проект")
	plan := projectsPlan(t, home, nil, gone)
	if len(plan.Paths()) != 0 || !hasWarning(plan, WarnProjectMissing) {
		t.Fatalf("plan = %+v", plan)
	}
	if known := plan.Known(); len(known) != 1 || known[0].Path != gone {
		t.Errorf("known = %+v", known)
	}
}

func TestUserExcludes(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	plain := filepath.Join(home, filepath.FromSlash(fakehome.PlainFolder))
	heavy := filepath.Join(plain, "Видео с праздника")
	plan := projectsPlan(t, home, func(in Inputs) {
		in.Config.Sources.Exclude = []string{heavy, "заметки.md", filepath.Join(plain, "bui*")}
	}, plain)
	kept := survivors(t, plan, plain)
	for _, banned := range []string{"Видео с праздника/ролик.mp4", "заметки.md", "build/единственная копия.txt"} {
		if slices.Contains(kept, banned) {
			t.Errorf("%s stayed: %v", banned, kept)
		}
	}
}

func TestExcludingAProjectTakesItOffTheSources(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	plain := filepath.Join(home, filepath.FromSlash(fakehome.PlainFolder))
	plan := projectsPlan(t, home, func(in Inputs) { in.Config.Sources.Exclude = []string{plain} }, plain)
	if len(plan.Paths()) != 0 || len(plan.Left) != 1 || plan.Left[0].Inside != "" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestBadExcludeIsReported(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	plan := projectsPlan(t, home, func(in Inputs) {
		in.Config.Sources.Exclude = []string{"a/b", filepath.Join(home, "*", "x")}
	})
	if !hasWarning(plan, WarnBadExclude) {
		t.Fatalf("warnings = %+v", plan.Warnings)
	}
}

func TestRepositoryIsNeverSavedIntoItself(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	folder := filepath.Join(home, "всё сразу")
	in := inputs(t, home)
	in.Config.Repositories.Local = filepath.Join(folder, "repo")
	fakehome.Write(t, filepath.Join(folder, "repo", "config"), "x")
	fakehome.Write(t, filepath.Join(folder, "work.txt"), "x")
	fakehome.Transcript(t, home, "x", "a", folder)
	plan := build(t, Projects, in)
	if kept := survivors(t, plan, folder); len(kept) != 1 || kept[0] != "work.txt" {
		t.Errorf("kept = %v", kept)
	}
}

func TestProjectInsideExcludedName(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	project := filepath.Join(home, "build", "приложение")
	fakehome.Write(t, filepath.Join(project, "main.go"), "x")
	plan := projectsPlan(t, home, nil, project)
	if kept := survivors(t, plan, project); len(kept) != 1 {
		t.Errorf("kept = %v", kept)
	}
	for _, pattern := range plan.Excludes {
		if strings.HasSuffix(pattern, "build") && !strings.Contains(pattern, "**") {
			t.Errorf("pattern hits the project: %s", pattern)
		}
	}
}

func TestSpecialCharactersInPatterns(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "work", "проект [2026]")
	r := rules{roots: []string{root}, everywhere: []string{"node_modules"}, literal: []string{filepath.Join(root, "тяжёлое")}}
	patterns := r.patterns()
	if !strings.Contains(patterns[0], "[[]2026]") || !strings.Contains(patterns[1], "[[]2026]") {
		t.Errorf("patterns = %v", patterns)
	}
}

func TestExcludeFileDoublesDollar(t *testing.T) {
	plan := &Plan{Excludes: []string{"/a/$HOME/b"}}
	path, err := plan.WriteExcludeFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != "/a/$$HOME/b" {
		t.Errorf("got %q, %v", data, err)
	}
}

func TestOptions(t *testing.T) {
	plan := &Plan{Profile: Claude, Items: []Item{{Path: "/a"}}}
	options := plan.Options("/excludes", true)
	if options.ExcludeFile != "/excludes" || !options.DryRun || !options.GroupByTags ||
		!slices.Equal(options.Tags, []string{Claude}) || !slices.Equal(options.Paths, []string{"/a"}) {
		t.Errorf("options = %+v", options)
	}
}

func TestUnknownProfile(t *testing.T) {
	if _, err := Build("everything", Inputs{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStateIsWrittenOnlyOnRequest(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	project := filepath.Join(home, "проект")
	in := inputs(t, home)
	fakehome.Transcript(t, home, "x", "a", project)
	plan := build(t, Projects, in)
	if _, err := os.Stat(in.Layout.ProjectsFile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a plan wrote the state: %v", err)
	}
	if err := plan.SaveState(in); err != nil {
		t.Fatal(err)
	}
	state, _, err := discover.LoadState(in.Layout.ProjectsFile())
	if err != nil || len(state.Projects) != 1 {
		t.Fatalf("state = %+v, %v", state, err)
	}
}

func TestDamagedStateBecomesAWarning(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	in := inputs(t, home)
	fakehome.Write(t, in.Layout.ProjectsFile(), "{broken")
	if plan := build(t, Projects, in); !hasWarning(plan, WarnStateRecovered) {
		t.Fatalf("warnings = %+v", plan.Warnings)
	}
}

func TestLockedFilesAreSortedByTheManifest(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Classic)
	plan := build(t, Claude, inputs(t, home))
	root := filepath.Join(home, filepath.FromSlash(fakehome.ClassicRoot))
	allowed := filepath.Join(root, "Local Storage", "leveldb", "LOCK")
	gap := filepath.Join(home, ".claude", "projects", "x", "a.jsonl")
	locked, skipped := plan.SortSkipped([]engine.ItemError{{Item: allowed}, {Item: gap}})
	if len(locked) != 1 || locked[0].Item != allowed || len(skipped) != 1 || skipped[0].Item != gap {
		t.Errorf("locked=%+v skipped=%+v", locked, skipped)
	}
}

func TestMeasureFollowsTheRulesAndFindsHeavyFolders(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	plain := filepath.Join(home, filepath.FromSlash(fakehome.PlainFolder))
	repository := filepath.Join(home, filepath.FromSlash(fakehome.Repository))
	fakehome.Write(t, filepath.Join(repository, "assets", "promo.mp4"), strings.Repeat("v", 5000))
	fakehome.Write(t, filepath.Join(plain, "корень.mp4"), strings.Repeat("v", 500))
	plan := projectsPlan(t, home, nil, plain, repository)
	result, err := plan.Measure(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	var heavy []string
	for _, folder := range result.Heavy {
		heavy = append(heavy, filepath.Base(folder.Path))
		if folder.Path == plain || folder.Path == repository {
			t.Errorf("a project root must not be offered: %+v", folder)
		}
	}
	if !slices.Contains(heavy, "Видео с праздника") {
		t.Errorf("heavy = %v", heavy)
	}
	if slices.Contains(heavy, "venv") || slices.Contains(heavy, "node_modules") {
		t.Errorf("excluded folders counted: %v", heavy)
	}
	var plainSize RootSize
	for _, root := range result.Roots {
		if root.Path == plain {
			plainSize = root
		}
	}
	// заметки, ролик, build, корень. venv and .credentials.json are out.
	if plainSize.Files != 4 {
		t.Errorf("files = %d in %+v", plainSize.Files, result.Roots)
	}
	if plainSize.LooseMedia != 500 {
		t.Errorf("loose media = %d", plainSize.LooseMedia)
	}
}

// Media that is not the larger part of a folder must still be shown: it was the
// way 7 GB of video stayed out of the list.
func TestMeasureReportsMixedFolders(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	outer := filepath.Join(home, "папка")
	fakehome.Write(t, filepath.Join(outer, "смешанная", "запись.mxf"), strings.Repeat("v", 1000))
	fakehome.Write(t, filepath.Join(outer, "смешанная", "архив.zip"), strings.Repeat("a", 5000))
	plan := projectsPlan(t, home, nil, outer)
	result, err := plan.Measure(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Heavy) != 1 || !result.Heavy[0].Mixed || filepath.Base(result.Heavy[0].Path) != "смешанная" {
		t.Fatalf("heavy = %+v", result.Heavy)
	}
}

func TestMeasureOffersNothingAroundARepository(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	outer := filepath.Join(home, "папка")
	fakehome.Write(t, filepath.Join(outer, "вложенный", "repo", ".git", "HEAD"), "x")
	fakehome.Write(t, filepath.Join(outer, "вложенный", "repo", "demo.mp4"), strings.Repeat("v", 5000))
	plan := projectsPlan(t, home, nil, outer)
	result, err := plan.Measure(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Heavy) != 0 {
		t.Errorf("excluding these would drop a repository: %+v", result.Heavy)
	}
}

func fakeLs(nodes []engine.Node) func(context.Context, string, []string, func(engine.Node)) error {
	return func(_ context.Context, _ string, _ []string, onNode func(engine.Node)) error {
		for _, node := range nodes {
			onNode(node)
		}
		return nil
	}
}

func snapshotOf(t *testing.T, home string) []engine.Node {
	t.Helper()
	var nodes []engine.Node
	for _, file := range fakehome.Tree(t, home) {
		path := filepath.Join(home, filepath.FromSlash(file))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, engine.Node{Name: filepath.Base(path), Type: engine.NodeFile, Path: restore.SnapshotPath(path), Size: info.Size()})
	}
	return nodes
}

func nodesWithout(nodes []engine.Node, drop func(string) bool) []engine.Node {
	var kept []engine.Node
	for _, node := range nodes {
		if !drop(node.Path) {
			kept = append(kept, node)
		}
	}
	return kept
}

func TestVerifyPassesWhenTheSnapshotMatches(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Store)
	plan := build(t, Claude, inputs(t, home))
	nodes := nodesWithout(snapshotOf(t, home), func(path string) bool {
		for _, banned := range []string{"/.credentials.json", "/node_modules/", "/vm_bundles/", "/.claude/cache/", "/telemetry/", "/sessions/", "/session-env/", "/shell-snapshots/", "/daemon.log", "/Cache/"} {
			if strings.Contains(path, banned) {
				return true
			}
		}
		return false
	})
	check, err := plan.Verify(context.Background(), "abc", []string{"vm_bundles"}, fakeLs(nodes))
	if err != nil {
		t.Fatal(err)
	}
	if !check.OK {
		t.Fatalf("check = %+v", check)
	}
}

func TestVerifyFindsWhatIsMissing(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	plan := build(t, Claude, inputs(t, home))
	nodes := nodesWithout(snapshotOf(t, home), func(path string) bool {
		return strings.Contains(path, "/.credentials.json") || strings.Contains(path, "/brand-new-folder/") ||
			strings.Contains(path, "/node_modules/")
	})
	check, err := plan.Verify(context.Background(), "abc", nil, fakeLs(nodes))
	if err != nil {
		t.Fatal(err)
	}
	if check.OK {
		t.Fatal("a folder on disk that is absent from the snapshot must fail the check")
	}
	missing := false
	for _, row := range check.Rows {
		if row.Status == RowMissing && strings.HasSuffix(row.Path, "brand-new-folder") {
			missing = true
		}
	}
	if !missing {
		t.Errorf("rows = %+v", check.Rows)
	}
}

func TestVerifyFindsWhatMustNotBeThere(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	plan := build(t, Claude, inputs(t, home))
	check, err := plan.Verify(context.Background(), "abc", []string{"vm_bundles"}, fakeLs(snapshotOf(t, home)))
	if err != nil {
		t.Fatal(err)
	}
	if check.OK || check.Sought[".credentials.json"] == 0 || len(check.Forbidden) == 0 {
		t.Fatalf("check = %+v", check)
	}
	if _, asked := check.Sought["vm_bundles"]; !asked {
		t.Errorf("sought = %+v", check.Sought)
	}
}

func TestSnapshotPaths(t *testing.T) {
	paths := SnapshotPaths([]string{filepath.FromSlash("/a/b")})
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/a/b") {
		t.Errorf("paths = %v", paths)
	}
}

func TestWarningCodesAreUnique(t *testing.T) {
	codes := WarningCodes()
	slices.Sort(codes)
	if len(slices.Compact(codes)) != len(WarningCodes()) {
		t.Errorf("codes = %v", codes)
	}
}
