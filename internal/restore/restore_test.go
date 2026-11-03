package restore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

const (
	sessionCode    = "0fc6092b-f4d7-460e-ab01-acc244289ebe"
	sessionLinked  = "1684e174-de2d-4b9e-bed0-95480fcec2c2"
	sessionDesktop = "9a1b2c3d-0000-4000-8000-00000000d1d1"
)

func manifest(t *testing.T) *manifests.Manifest {
	t.Helper()
	m, err := manifests.Load(runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// world is a home folder with sessions of every kind and a project, and a
// claude snapshot and a projects snapshot of it.
type world struct {
	home, project, store string
	repo                 *fakeRepo
	claude, projects     engine.Snapshot
}

func claudeSources(home string) []string {
	return []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, filepath.FromSlash(fakehome.StoreRoot)),
		filepath.Join(home, filepath.FromSlash(fakehome.ClassicRoot)),
	}
}

func newWorld(t *testing.T) *world {
	t.Helper()
	home := fakehome.Copy(t, fakehome.Both)
	w := &world{home: home, repo: newFakeRepo(t), project: filepath.Join(home, filepath.FromSlash(fakehome.Repository))}
	w.store = filepath.Join(home, filepath.FromSlash(fakehome.StoreRoot))
	folder := ProjectFolder(w.project)
	fakehome.Transcript(t, home, folder, sessionCode, w.project)
	fakehome.Write(t, filepath.Join(home, ".claude", "projects", folder, sessionCode, "subagents", "agent-1.jsonl"), "{}\n")
	fakehome.Write(t, filepath.Join(home, ".claude", "file-history", sessionCode, "abc@v1"), "old text\n")
	fakehome.Transcript(t, home, folder, sessionLinked, w.project)
	description, _ := json.Marshal(map[string]string{"sessionId": "local_" + sessionDesktop, "cliSessionId": sessionLinked, "cwd": w.project})
	fakehome.Write(t, filepath.Join(w.store, "claude-code-sessions", "account", "org", "local_"+sessionDesktop+".json"), string(description))
	fakehome.Write(t, filepath.Join(w.store, "Local Storage", "leveldb", "000003.log"), "level\n")
	// ~/.claude.json lists projects by path, with JSON escaping.
	settings, _ := json.Marshal(map[string]any{"projects": map[string]any{w.project: map[string]bool{"trusted": true}}})
	fakehome.Write(t, filepath.Join(home, ".claude.json"), string(settings))
	fakehome.Write(t, filepath.Join(w.project, "package.json"), `{"path": `+quote(w.project)+`}`)
	w.claude = w.repo.snapshot(t, "aaaa1111", TagClaude, "pc", claudeSources(home)...)
	w.projects = w.repo.snapshot(t, "bbbb2222", TagProjects, "pc", w.project)
	return w
}

func quote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func identity(t *testing.T, w *world) Placement {
	t.Helper()
	return Placement{Manifest: manifest(t), Origin: OriginOf(manifest(t), w.claude), Mapper: NewMapper(nil)}
}

func TestOriginOfReadsTheSnapshot(t *testing.T) {
	w := newWorld(t)
	origin := OriginOf(manifest(t), w.claude)
	if LocalPath(origin.Home) != w.home || LocalPath(origin.Claude) != filepath.Join(w.home, ".claude") {
		t.Errorf("origin = %+v", origin)
	}
	if len(origin.Desktop) != 2 {
		t.Errorf("desktop roots: %v (the .claude.json file is not one)", origin.Desktop)
	}
}

func TestLocalPathReversesSnapshotPath(t *testing.T) {
	for _, local := range []string{filepath.Join(t.TempDir(), "папка с пробелом", "файл.txt"), t.TempDir()} {
		if got := LocalPath(SnapshotPath(local)); got != local {
			t.Errorf("%s came back as %s", local, got)
		}
	}
}

func TestFindSessionCollectsEveryPart(t *testing.T) {
	w := newWorld(t)
	match, err := FindSession(context.Background(), w.repo, manifest(t), w.repo.snapshots, sessionCode[:8], "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(match.IDs, []string{sessionCode}) || match.Snapshot.ShortID != "aaaa1111" {
		t.Fatalf("match = %+v", match)
	}
	var names []string
	for _, piece := range match.Pieces {
		names = append(names, piece.Names...)
	}
	slices.Sort(names)
	if want := []string{sessionCode, sessionCode, sessionCode + ".jsonl"}; !slices.Equal(names, want) {
		t.Errorf("parts = %v, want %v (transcript, its folder, file history)", names, want)
	}
}

func TestFindSessionOfTheDesktopApplication(t *testing.T) {
	w := newWorld(t)
	match, err := FindSession(context.Background(), w.repo, manifest(t), w.repo.snapshots, sessionDesktop, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(match.Descriptions) != 1 || !strings.HasSuffix(match.Descriptions[0], "local_"+sessionDesktop+".json") {
		t.Errorf("descriptions = %v", match.Descriptions)
	}
}

func TestFindSessionLooksIntoOlderSnapshots(t *testing.T) {
	w := newWorld(t)
	transcript := filepath.Join(w.home, ".claude", "projects", ProjectFolder(w.project), sessionCode+".jsonl")
	fakehome.Remove(t, transcript)
	fakehome.Remove(t, filepath.Join(w.home, ".claude", "file-history", sessionCode))
	fakehome.Remove(t, filepath.Join(w.home, ".claude", "projects", ProjectFolder(w.project), sessionCode))
	w.repo.snapshot(t, "cccc3333", TagClaude, "pc", claudeSources(w.home)...)
	match, err := FindSession(context.Background(), w.repo, manifest(t), w.repo.snapshots, sessionCode, "")
	if err != nil {
		t.Fatal(err)
	}
	if match.Snapshot.ShortID != "aaaa1111" {
		t.Errorf("found in %s, want the older snapshot that has it", match.Snapshot.ShortID)
	}
}

func TestFindSessionErrors(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, err := FindSession(ctx, w.repo, manifest(t), w.repo.snapshots, "x", ""); err == nil {
		t.Error("a too short id must be refused")
	}
	if _, err := FindSession(ctx, w.repo, manifest(t), w.repo.snapshots, "ffffffff-0000", ""); err == nil {
		t.Error("an unknown id must be reported")
	}
	// Both code sessions are in one folder; an empty-ish prefix shared by
	// nothing else is fine, but the folder name itself is not a session.
	if _, err := FindSession(ctx, w.repo, manifest(t), w.repo.snapshots, ProjectFolder(w.project)[:8], ""); err == nil {
		t.Error("a project folder name is not a session")
	}
}

// The main promise: a deleted session comes back, and nothing that exists is
// replaced, even a file that differs from the copy.
func TestSessionComesBackAndNothingIsOverwritten(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	folder := filepath.Join(w.home, ".claude", "projects", ProjectFolder(w.project))
	transcript := filepath.Join(folder, sessionCode+".jsonl")
	want, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	fakehome.Remove(t, transcript)
	history := filepath.Join(w.home, ".claude", "file-history", sessionCode, "abc@v1")
	fakehome.Write(t, history, "edited after the snapshot\n")

	match, err := FindSession(ctx, w.repo, manifest(t), w.repo.snapshots, sessionCode, "")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	fetched, err := Fetch(ctx, w.repo, match.Pieces, target)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Files != 3 || fetched.Digest == "" {
		t.Errorf("fetched = %+v", fetched)
	}
	// Nothing in place changed so far.
	if _, err := os.Stat(transcript); err == nil {
		t.Fatal("restoring into a folder must not touch the original place")
	}

	plan, err := PlanApply(target, match.Pieces, identity(t, w))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Copies) != 1 || plan.Copies[0].To != transcript {
		t.Fatalf("copies = %+v", plan.Copies)
	}
	differ := 0
	for _, skipped := range plan.Skipped {
		if skipped.Reason == SkipExists && skipped.Differs {
			differ++
		}
	}
	if differ != 1 {
		t.Errorf("skipped = %+v", plan.Skipped)
	}
	done, err := Apply(plan, NewMapper(nil))
	if err != nil || done.Copied != 1 {
		t.Fatalf("done = %+v, %v", done, err)
	}
	got, err := os.ReadFile(transcript)
	if err != nil || string(got) != string(want) {
		t.Errorf("transcript came back as %q, %v", got, err)
	}
	if data, _ := os.ReadFile(history); string(data) != "edited after the snapshot\n" {
		t.Errorf("an existing file was overwritten: %q", data)
	}
	// A second apply finds everything in place and writes nothing.
	again, err := PlanApply(target, match.Pieces, identity(t, w))
	if err != nil || len(again.Copies) != 0 {
		t.Errorf("second plan = %+v, %v", again, err)
	}
}

func TestApplySkipsAFileThatAppearedMeanwhile(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "from.txt")
	to := filepath.Join(dir, "place", "to.txt")
	fakehome.Write(t, from, "restored")
	plan := &ApplyPlan{Copies: []Copy{{From: from, To: to, Size: 8}}}
	fakehome.Write(t, to, "made by the user in the meantime")
	done, err := Apply(plan, NewMapper(nil))
	if err != nil || done.Copied != 0 {
		t.Fatalf("done = %+v, %v", done, err)
	}
	if data, _ := os.ReadFile(to); string(data) != "made by the user in the meantime" {
		t.Errorf("overwritten: %q", data)
	}
}

func TestLockedDatabasesAreMarked(t *testing.T) {
	w := newWorld(t)
	fakehome.Remove(t, filepath.Join(w.store, "Local Storage"))
	pieces, err := PiecesOf(context.Background(), w.repo, w.claude, TagClaude, RootsOf(w.claude))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err := Fetch(context.Background(), w.repo, pieces, target); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanApply(target, pieces, identity(t, w))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Locked() {
		t.Errorf("Local Storage must be marked: %+v", plan.Copies)
	}
	for _, item := range plan.Copies {
		if !item.Locked {
			t.Errorf("only Local Storage was missing, got %s", item.To)
		}
	}
}

// Another computer: another user name with Cyrillic letters, the project on
// another drive or folder. Claude's paths move with the home folder, the
// transcripts folder is renamed after the new project path, paths inside
// Claude's settings are rewritten, and the user's own files stay byte for byte.
func TestMoveToAnotherComputer(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	newHome := filepath.Join(t.TempDir(), "Новый Пользователь")
	newStore := filepath.Join(newHome, filepath.FromSlash(fakehome.StoreRoot))
	fakehome.Write(t, filepath.Join(newStore, "claude-code-sessions", ".keep"), "")
	newProject := filepath.Join(t.TempDir(), "Work", "Сайт клуба")

	var pieces []Piece
	for _, snapshot := range []engine.Snapshot{w.claude, w.projects} {
		more, err := PiecesOf(ctx, w.repo, snapshot, snapshot.Tags[0], RootsOf(snapshot))
		if err != nil {
			t.Fatal(err)
		}
		pieces = append(pieces, more...)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, err := Fetch(ctx, w.repo, pieces, target); err != nil {
		t.Fatal(err)
	}
	mapper := NewMapper([]Pair{
		{From: w.project, To: newProject},
		{From: w.store, To: newStore},
		{From: w.home, To: newHome},
	})
	mapper.Block(filepath.Join(w.home, filepath.FromSlash(fakehome.ClassicRoot)))
	place := Placement{Manifest: manifest(t), Origin: OriginOf(manifest(t), w.claude), Mapper: mapper}
	plan, err := PlanApply(target, pieces, place)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan, mapper); err != nil {
		t.Fatal(err)
	}

	moved := filepath.Join(newHome, ".claude", "projects", ProjectFolder(newProject), sessionCode+".jsonl")
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("the transcript is not in the renamed folder: %v", err)
	}
	settings, err := os.ReadFile(filepath.Join(newHome, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), quote(newProject)[1:len(quote(newProject))-1]) || strings.Contains(strings.ToLower(string(settings)), strings.ToLower(quote(w.project)[1:len(quote(w.project))-1])) {
		t.Errorf(".claude.json was not rewritten: %s", settings)
	}
	if _, err := os.Stat(filepath.Join(newStore, "claude-code-sessions", "account", "org", "local_"+sessionDesktop+".json")); err != nil {
		t.Errorf("the desktop session is not in the new application folder: %v", err)
	}
	pkg, err := os.ReadFile(filepath.Join(newProject, "package.json"))
	if err != nil || !strings.Contains(string(pkg), quote(w.project)) {
		t.Errorf("a project file must be copied exactly: %s, %v", pkg, err)
	}
	blocked := 0
	for _, skipped := range plan.Skipped {
		if skipped.Reason == SkipDesktopMissing {
			blocked++
		}
	}
	if blocked == 0 {
		t.Error("the classic application folder has no place here and must be skipped")
	}
}

func TestMapperRules(t *testing.T) {
	base := t.TempDir()
	old, fresh := filepath.Join(base, "Users", "Al"), filepath.Join(base, "Users", "Новый")
	m := NewMapper([]Pair{{From: old, To: fresh}, {From: base, To: base}})
	if got, _ := m.Map(filepath.Join(old, ".claude", "x")); got != filepath.Join(fresh, ".claude", "x") {
		t.Errorf("got %s", got)
	}
	if got, _ := m.Map(filepath.Join(base, "Users", "Alex")); got != filepath.Join(base, "Users", "Alex") {
		t.Errorf("a longer name must not match: %s", got)
	}
	if len(m.Pairs()) != 1 {
		t.Errorf("a pair that changes nothing is dropped: %v", m.Pairs())
	}
	text := "a " + old + " b " + quote(filepath.Join(old, "p")) + " c " + filepath.ToSlash(old) + "/q d " + old + "ex"
	got := m.RewriteText(text)
	for _, want := range []string{fresh, quote(filepath.Join(fresh, "p")), filepath.ToSlash(fresh) + "/q", old + "ex"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if name := m.ProjectFolder(ProjectFolder(filepath.Join(old, "app"))); name != ProjectFolder(filepath.Join(fresh, "app")) {
		t.Errorf("project folder = %s", name)
	}
	if name := m.ProjectFolder(ProjectFolder(old) + "ex"); name != ProjectFolder(old)+"ex" {
		t.Errorf("a longer folder name must stay: %s", name)
	}
}

func TestProjectFolderEncoding(t *testing.T) {
	if got := ProjectFolder(`D:\YandexDisk\Лазертаг\app`); got != "D--YandexDisk----------app" {
		t.Errorf("got %s", got)
	}
}

func TestFindProject(t *testing.T) {
	base := t.TempDir()
	one, two := filepath.Join(base, "Сайт клуба"), filepath.Join(base, "work", "сайт клуба старый")
	snapshot := engine.Snapshot{Paths: []string{one, two}}
	if got, err := FindProject(snapshot, "САЙТ КЛУБА"); err != nil || got != SnapshotPath(one) {
		t.Errorf("exact name: %s, %v", got, err)
	}
	if got, err := FindProject(snapshot, "старый"); err != nil || got != SnapshotPath(two) {
		t.Errorf("part of a name: %s, %v", got, err)
	}
	if _, err := FindProject(snapshot, "клуба"); err == nil {
		t.Error("two partial matches must be ambiguous")
	}
	inside := filepath.Join(one, "src")
	if got, err := FindProject(snapshot, inside); err != nil || got != SnapshotPath(inside) {
		t.Errorf("a folder inside a project: %s, %v", got, err)
	}
	if _, err := FindProject(snapshot, filepath.Join(base, "other")); err == nil {
		t.Error("a path outside the snapshot must be refused")
	}
}

func TestDrillComparesRestoredFiles(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	opts := DrillOptions{MaxFiles: 100, MaxBytes: 1 << 30, Seed: 1, WorkDir: t.TempDir(), Target: filepath.Join(t.TempDir(), "test")}
	result, err := Drill(ctx, w.repo, w.projects, TagProjects, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK() || result.Matched == 0 || result.Matched != result.Restored {
		t.Fatalf("result = %+v", result)
	}

	// A damaged copy: same size and time, other content.
	damaged := filepath.Join(w.repo.folder(w.projects.ShortID), filepath.FromSlash(strings.TrimPrefix(SnapshotPath(filepath.Join(w.project, "package.json")), "/")))
	info, err := os.Stat(damaged)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(damaged)
	for i := range data {
		data[i] = 'x'
	}
	fakehome.Write(t, damaged, string(data))
	_ = os.Chtimes(damaged, info.ModTime(), info.ModTime())
	opts.Target = filepath.Join(t.TempDir(), "test")
	result, err = Drill(ctx, w.repo, w.projects, TagProjects, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK() || len(result.Mismatches) != 1 || result.Mismatches[0].Path != filepath.Join(w.project, "package.json") {
		t.Errorf("result = %+v", result)
	}
}

func TestEscapePattern(t *testing.T) {
	if got := EscapePattern("проект [2026]*?.md"); got != "проект [[]2026][*][?].md" {
		t.Errorf("got %s", got)
	}
}
