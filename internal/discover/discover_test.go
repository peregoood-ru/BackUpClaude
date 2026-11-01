package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

func windowsManifest(t *testing.T) *manifests.Manifest {
	t.Helper()
	manifest, err := manifests.Load("windows")
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func fakeEnv(home string) Env {
	return Env{Home: home, Lookup: fakehome.Lookup(home)}
}

func TestExpand(t *testing.T) {
	env := Env{Home: filepath.FromSlash("/home/user"), Lookup: func(name string) string {
		if name == "APPDATA" {
			return "base"
		}
		return ""
	}}
	if got, ok := env.Expand("~/.claude/projects"); !ok || got != filepath.FromSlash("/home/user/.claude/projects") {
		t.Errorf("home: %q %v", got, ok)
	}
	if got, ok := env.Expand(`~\.ssh`); !ok || got != filepath.FromSlash("/home/user/.ssh") {
		t.Errorf("backslash home: %q %v", got, ok)
	}
	if got, ok := env.Expand("~"); !ok || got != env.Home {
		t.Errorf("bare home: %q %v", got, ok)
	}
	if got, ok := env.Expand("%APPDATA%/Claude"); !ok || got != filepath.FromSlash("base/Claude") {
		t.Errorf("variable: %q %v", got, ok)
	}
	if _, ok := env.Expand("%NOT_SET%/Claude"); ok {
		t.Error("unset variable accepted")
	}
}

func desktopPaths(found Claude) []string {
	paths := make([]string, len(found.Desktop))
	for i, location := range found.Desktop {
		paths[i] = location.Path
	}
	return paths
}

func TestLocateFindsEveryInstallation(t *testing.T) {
	cases := []struct {
		name    string
		install fakehome.Install
		want    []string
	}{
		{"classic", fakehome.Classic, []string{fakehome.ClassicRoot}},
		{"store", fakehome.Store, []string{fakehome.StoreRoot}},
		{"both keep their own data", fakehome.Both, []string{fakehome.StoreRoot, fakehome.ClassicRoot}},
		{"no desktop application", fakehome.NoDesktop, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := fakehome.Copy(t, tc.install)
			found := Locate(windowsManifest(t), fakeEnv(home))
			if !found.Home.Exists {
				t.Errorf("fake ~/.claude not found: %+v", found.Home)
			}
			got := desktopPaths(found)
			if len(got) != len(tc.want) {
				t.Fatalf("found %v, want %v", got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != filepath.Join(home, filepath.FromSlash(want)) {
					t.Errorf("%d: %s", i, got[i])
				}
			}
		})
	}
}

// In a Store installation %APPDATA%\Claude and the package folder are one folder
// inside the application's package, while outside it %APPDATA%\Claude does not
// exist. The package path has to win so that snapshots keep one path whatever
// started the backup.
func TestLocateCountsAnAliasOnce(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Both)
	env := fakeEnv(home)
	env.SameDir = func(string, string) bool { return true }
	got := desktopPaths(Locate(windowsManifest(t), env))
	if len(got) != 1 || got[0] != filepath.Join(home, filepath.FromSlash(fakehome.StoreRoot)) {
		t.Errorf("found %v", got)
	}
}

func TestLocateIgnoresFoldersWithoutClaudeData(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	fakehome.Write(t, filepath.Join(home, "AppData", "Local", "Packages", "OtherApp_fixture", "LocalCache", "Roaming", "Claude", "notes.txt"), "x")
	found := Locate(windowsManifest(t), fakeEnv(home))
	if len(found.Desktop) != 0 {
		t.Errorf("found %v", found.Desktop)
	}
}

func TestLocateWithoutClaude(t *testing.T) {
	found := Locate(windowsManifest(t), Env{Home: t.TempDir(), Lookup: func(string) string { return "" }})
	if found.Home.Exists || len(found.Desktop) != 0 {
		t.Errorf("found something in an empty home: %+v", found)
	}
}

func TestExpandStarsTakesTheRestLiterally(t *testing.T) {
	root := t.TempDir()
	fakehome.Write(t, filepath.Join(root, "имя [с] скобками", "a", "x", "f.txt"), "x")
	fakehome.Write(t, filepath.Join(root, "имя [с] скобками", "b", "x", "f.txt"), "x")
	got := expandStars(filepath.Join(root, "имя [с] скобками", "*", "x"))
	if len(got) != 2 || !strings.HasSuffix(got[1], filepath.Join("b", "x")) {
		t.Errorf("got %v", got)
	}
}

func TestFirstCwd(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"plain", `{"cwd":"/work/project"}`, "/work/project"},
		{"after other fields", `{"type":"user","uuid":"1","cwd":"/work/project","x":1}`, "/work/project"},
		{"spaces around the colon", `{"cwd" : "/work/project"}`, "/work/project"},
		{"backslashes", `{"cwd":"D:\\Мои проекты\\сайт"}`, `D:\Мои проекты\сайт`},
		{"unicode escapes", `{"cwd":"\u041f\u0440\u043e\u0435\u043a\u0442"}`, "Проект"},
		{"nested object first", `{"input":{"cwd":"/inside/tool"},"cwd":"/real"}`, "/real"},
		{"nested array first", `{"list":[{"cwd":"/inside"}],"cwd":"/real"}`, "/real"},
		{"inside text", `{"text":"\"cwd\":\"/in/text\"","cwd":"/real"}`, "/real"},
		{"as a value", `{"name":"cwd","cwd":"/real"}`, "/real"},
		{"second line", "{\"type\":\"summary\"}\n{\"cwd\":\"/real\"}\n", "/real"},
		{"windows line ends", "{\"type\":\"summary\"}\r\n{\"cwd\":\"/real\"}\r\n", "/real"},
		{"byte order mark", "\xef\xbb\xbf{\"cwd\":\"/real\"}", "/real"},
		{"damaged line", "{\"text\":\"never closed\n{\"cwd\":\"/real\"}\n", "/real"},
		{"unbalanced line", "{\"a\":[[[[\n{\"cwd\":\"/real\"}\n", "/real"},
		{"not a string", `{"cwd":17}`, ""},
		{"empty value", "{\"cwd\":\"\"}\n{\"cwd\":\"/real\"}\n", "/real"},
		{"none", `{"type":"summary"}`, ""},
		{"nothing", "", ""},
		{"zero bytes of a crash", strings.Repeat("\x00", 1000), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FirstCwd(strings.NewReader(tc.input))
			if err != nil || got != tc.want {
				t.Errorf("got %q, %v", got, err)
			}
		})
	}
}

func TestFirstCwdAcrossReadBuffers(t *testing.T) {
	// The key lands on every position around the end of the read buffer.
	for pad := cwdReadBuffer - 12; pad < cwdReadBuffer+2; pad++ {
		input := `{"text":"` + strings.Repeat("x", pad) + `","cwd":"/real"}`
		if got, err := FirstCwd(strings.NewReader(input)); err != nil || got != "/real" {
			t.Fatalf("pad %d: got %q, %v", pad, got, err)
		}
	}
}

func TestScanCwdsReportsEachChange(t *testing.T) {
	input := strings.Join([]string{fakehome.Line("/a"), fakehome.Line("/a"), fakehome.Line("/b"), fakehome.Line("/a")}, "\n")
	var got []string
	err := ScanCwds(strings.NewReader(input), func(cwd string) bool {
		got = append(got, cwd)
		return false
	})
	if err != nil || strings.Join(got, " ") != "/a /b /a" {
		t.Errorf("got %v, %v", got, err)
	}
}

type repeating struct {
	left int64
	fill byte
}

func (r *repeating) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, os.ErrClosed
	}
	n := min(int64(len(p)), r.left)
	for i := range p[:n] {
		p[i] = r.fill
	}
	r.left -= n
	return int(n), nil
}

// A failure of the source must surface: a silent end would look like a
// transcript without a working folder.
func TestScanCwdsReportsReadErrors(t *testing.T) {
	if _, err := FirstCwd(&repeating{left: 10, fill: 'x'}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestFilter(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Both)
	filter := NewFilter(windowsManifest(t), fakeEnv(home))
	project := filepath.Join(home, "Мои проекты", "Сайт клуба")
	cases := []struct {
		name, cwd, project, skip string
	}{
		{"project", project, project, ""},
		{"inside home is fine", filepath.Join(home, "tools"), filepath.Join(home, "tools"), ""},
		{"trailing separator", project + string(filepath.Separator), project, ""},
		{"home", home, "", SkipHome},
		{"folder above home", filepath.Dir(home), "", SkipRoot},
		{"disk root", filepath.VolumeName(home) + string(filepath.Separator), "", SkipRoot},
		{"temp", filepath.Join(home, "AppData", "Local", "Temp"), "", SkipSystem},
		{"inside temp", filepath.Join(home, "AppData", "Local", "Temp", "claude", "abc"), "", SkipSystem},
		{"claude home", filepath.Join(home, ".claude", "projects"), "", SkipSystem},
		{"application data", filepath.Join(home, "AppData", "Roaming", "Claude", "local-agent-mode-sessions", "1"), "", SkipSystem},
		{"program data", filepath.Join(home, "AppData", "Local", "Some App"), "", SkipSystem},
		{"relative path", "sessions/abc", "", SkipForeign},
		{"empty", "", "", SkipForeign},
		{"worktree points at its project", filepath.Join(project, ".claude", "worktrees", "feature-x"), project, ""},
		{"inside a worktree", filepath.Join(project, ".claude", "worktrees", "feature-x", "src"), project, ""},
		{"worktree of the home folder", filepath.Join(home, ".claude", "worktrees", "x"), "", SkipHome},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotProject, gotSkip := filter.Classify(tc.cwd)
			if gotProject != tc.project || gotSkip != tc.skip {
				t.Errorf("got %q, %q", gotProject, gotSkip)
			}
		})
	}
}

func TestFilterFollowsLinkedWorktree(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Both)
	main := filepath.Join(home, "Мои проекты", "Сайт клуба")
	linked := filepath.Join(home, "ещё одна копия")
	gitDir := filepath.ToSlash(filepath.Join(main, ".git", "worktrees", "copy"))
	fakehome.Write(t, filepath.Join(linked, ".git"), "gitdir: "+gitDir+"\n")
	if project, skip := NewFilter(windowsManifest(t), fakeEnv(home)).Classify(linked); project != main || skip != "" {
		t.Errorf("got %q, %q", project, skip)
	}
}

func TestFilterKeepsASubmodule(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Both)
	sub := filepath.Join(home, "repo", "lib")
	fakehome.Write(t, filepath.Join(sub, ".git"), "gitdir: ../.git/modules/lib\n")
	if project, skip := NewFilter(windowsManifest(t), fakeEnv(home)).Classify(sub); project != sub || skip != "" {
		t.Errorf("got %q, %q", project, skip)
	}
}

func scan(t *testing.T, home string, state *State) (*Sessions, []Skipped) {
	t.Helper()
	return ScanProjects(windowsManifest(t), fakeEnv(home), state, time.Unix(1000, 0))
}

func projectPaths(state *State) []string {
	paths := make([]string, len(state.Projects))
	for i, project := range state.Projects {
		paths[i] = project.Path
	}
	return paths
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestScanFindsProjectsWithCyrillicAndSpaces(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	project := filepath.Join(home, "Мои проекты", "Сайт клуба")
	fakehome.Transcript(t, home, "D--work", "11111111-0000-4000-8000-000000000001", project, filepath.Join(project, "src"))
	state := &State{Version: stateVersion}
	scan(t, home, state)
	if got := projectPaths(state); !contains(got, project) {
		t.Fatalf("projects = %v", got)
	}
}

// A session that starts in a project is not read further: it cannot find more,
// and a transcript may be 120 MB. Moving to another folder later is not a new
// project.
func TestScanStopsAtTheFirstProject(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	first := filepath.Join(home, "first")
	second := filepath.Join(home, "second")
	fakehome.Transcript(t, home, "x", "a", first, second)
	state := &State{Version: stateVersion}
	scan(t, home, state)
	if got := projectPaths(state); !contains(got, first) || contains(got, second) {
		t.Fatalf("projects = %v", got)
	}
}

// The trap in "read to the first cwd and skip the home folder": a session started
// in the home folder that moved into a project would hide that project for good.
func TestScanFollowsSessionsStartedInHome(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	project := filepath.Join(home, "Мои проекты", "Без git")
	temp := filepath.Join(home, "AppData", "Local", "Temp")
	fakehome.Transcript(t, home, "home", "a", home, temp, filepath.Join(project, "docs"), project)
	state := &State{Version: stateVersion}
	_, skipped := scan(t, home, state)
	got := projectPaths(state)
	if !contains(got, project) || !contains(got, filepath.Join(project, "docs")) {
		t.Fatalf("projects = %v", got)
	}
	reasons := map[string]string{}
	for _, item := range skipped {
		reasons[item.Path] = item.Reason
	}
	if reasons[home] != SkipHome || reasons[temp] != SkipSystem {
		t.Errorf("skipped = %+v", skipped)
	}
}

func TestScanReadsSubagentTranscripts(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	project := filepath.Join(home, "работа")
	path := filepath.Join(home, ".claude", "projects", "x", "session-id", "subagents", "agent-1.jsonl")
	fakehome.Write(t, path, fakehome.Line(project)+"\n")
	state := &State{Version: stateVersion}
	sessions, _ := scan(t, home, state)
	if got := projectPaths(state); !contains(got, project) {
		t.Fatalf("projects = %v", got)
	}
	for _, session := range sessions.List {
		if session.ID == "agent-1" {
			t.Error("a subagent is not a session of its own")
		}
	}
}

func TestScanReadsDesktopDescriptions(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Store)
	root := filepath.Join(home, filepath.FromSlash(fakehome.StoreRoot))
	chosen := filepath.Join(home, "выбранная папка")
	worked := filepath.Join(home, "рабочая папка")
	description := `{"sessionId":"local_x","title":"Сессия","cwd":` + quoted(t, worked) + `,"userSelectedFolders":[` + quoted(t, chosen) + `]}`
	fakehome.Write(t, filepath.Join(root, "local-agent-mode-sessions", "a", "b", "local_x.json"), description)
	state := &State{Version: stateVersion}
	sessions, _ := scan(t, home, state)
	got := projectPaths(state)
	if !contains(got, chosen) || !contains(got, worked) {
		t.Fatalf("projects = %v", got)
	}
	found := false
	for _, session := range sessions.List {
		if session.ID == "x" && session.Kind == KindCowork && session.Title == "Сессия" {
			found = true
		}
	}
	if !found {
		t.Errorf("sessions = %+v", sessions.List)
	}
}

func quoted(t *testing.T, value string) string {
	t.Helper()
	line := fakehome.Line(value)
	start := strings.Index(line, `"cwd":`) + len(`"cwd":`)
	return line[start : strings.Index(line[start:], `,"message"`)+start]
}

func TestDesktopTitleJoinsItsTranscript(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Classic)
	state := &State{Version: stateVersion}
	sessions, _ := scan(t, home, state)
	for _, session := range sessions.List {
		if session.ID == "00000000-0000-4000-8000-000000000001" {
			if session.Kind != KindDesktop || session.Title != "Classic session" {
				t.Errorf("%+v", session)
			}
			return
		}
	}
	t.Fatalf("sessions = %+v", sessions.List)
}

// A crash can leave a description full of zero bytes. That must show up, because
// a project behind it may be missing.
func TestDamagedDescriptionIsReported(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Store)
	path := filepath.Join(home, filepath.FromSlash(fakehome.StoreRoot), "claude-code-sessions", "a", "b", "local_broken.json")
	fakehome.Write(t, path, strings.Repeat("\x00", 100))
	state := &State{Version: stateVersion}
	sessions, _ := scan(t, home, state)
	if !contains(sessions.Unreadable, path) {
		t.Errorf("unreadable = %v", sessions.Unreadable)
	}
}

func TestMissingFoldersAreNotProblems(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	fakehome.Remove(t, filepath.Join(home, ".claude", "projects"))
	state := &State{Version: stateVersion}
	sessions, _ := scan(t, home, state)
	if len(sessions.Unreadable) != 0 {
		t.Errorf("unreadable = %v", sessions.Unreadable)
	}
}

// The reason the state file exists: Claude Code deletes old transcripts.
func TestProjectSurvivesTheDeletionOfItsSessions(t *testing.T) {
	home := fakehome.Copy(t, fakehome.NoDesktop)
	stateFile := filepath.Join(t.TempDir(), "projects.json")
	project := filepath.Join(home, "старый проект")
	transcript := fakehome.Transcript(t, home, "old", "a", project)

	state, _, err := LoadState(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	scan(t, home, state)
	if err := state.Save(stateFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}

	state, _, err = LoadState(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	ScanProjects(windowsManifest(t), fakeEnv(home), state, time.Unix(2000, 0))
	found := false
	for _, got := range state.Projects {
		if got.Path == project {
			found = true
			if got.Sessions != 0 || !got.FirstSeen.Equal(time.Unix(1000, 0)) {
				t.Errorf("%+v", got)
			}
		}
	}
	if !found {
		t.Fatalf("projects = %+v", state.Projects)
	}
}

func TestDamagedStateIsKeptAndReported(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "projects.json")
	fakehome.Write(t, stateFile, "not json")
	state, recovered, err := LoadState(stateFile)
	if err != nil || recovered == "" || len(state.Projects) != 0 {
		t.Fatalf("state=%+v recovered=%q err=%v", state, recovered, err)
	}
	if data, err := os.ReadFile(recovered); err != nil || string(data) != "not json" {
		t.Errorf("kept file: %q %v", data, err)
	}
}

func TestCovers(t *testing.T) {
	outer := filepath.Join(string(filepath.Separator), "work", "проект")
	if !Covers(outer, filepath.Join(outer, "src")) || !Covers(outer, outer) {
		t.Error("should cover")
	}
	if Covers(outer, outer+" 2") || Covers(filepath.Join(outer, "src"), outer) {
		t.Error("should not cover")
	}
}
