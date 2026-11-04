package main

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/disk"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
	"github.com/peregoood-ru/BackUpClaude/internal/selfinstall"
	"github.com/peregoood-ru/BackUpClaude/internal/skill"
	"github.com/peregoood-ru/BackUpClaude/internal/syncdir"
)

// fakePath is a PATH that lives in memory.
type fakePath struct {
	dirs          []string
	adds, removes int
	unsupported   bool
}

func (f *fakePath) Supported() bool { return !f.unsupported }

func (f *fakePath) Contains(dir string) (bool, error) { return slices.Contains(f.dirs, dir), nil }

func (f *fakePath) Add(dir string) (bool, error) {
	if slices.Contains(f.dirs, dir) {
		return false, nil
	}
	f.dirs = append(f.dirs, dir)
	f.adds++
	return true, nil
}

func (f *fakePath) Remove(dir string) (bool, error) {
	i := slices.Index(f.dirs, dir)
	if i < 0 {
		return false, nil
	}
	f.dirs = slices.Delete(f.dirs, i, i+1)
	f.removes++
	return true, nil
}

// setupWorld is a computer without settings: no config.json, a fake restic
// that creates its repository on init, a fake scheduler and PATH.
type setupWorld struct {
	*world
	dir string
}

func newSetupWorld(t *testing.T) *setupWorld {
	t.Helper()
	w := newWorld(t)
	w.home = fakehome.Copy(t, fakehome.Both)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w.restic = self
	dir := t.TempDir()
	t.Setenv(envFakeRestic, dir)
	t.Setenv(envFakeMode, "norepo")
	// A program file that can be copied.
	w.exe = filepath.Join(t.TempDir(), "download", selfinstall.Name())
	fakehome.Write(t, w.exe, "program v1")
	return &setupWorld{w, dir}
}

func (w *setupWorld) localRepo(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ClaudeBackup", "repo")
}

func (w *setupWorld) calls(t *testing.T) []string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(w.dir, "calls.txt"))
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (w *setupWorld) report(t *testing.T, r result) setupReport {
	t.Helper()
	var report setupReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil {
		t.Fatalf("%v\n%s", err, r.stdout)
	}
	return report
}

func stepByID(report setupReport, id string) (setupStep, bool) {
	for _, step := range report.Steps {
		if step.ID == id {
			return step, true
		}
	}
	return setupStep{}, false
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestSetupDoesItAllWithoutQuestions(t *testing.T) {
	w := newSetupWorld(t)
	local := w.localRepo(t)
	r := w.run("setup", "--yes", "--json", "--local", local, "--skill")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	report := w.report(t, r)
	for _, step := range report.Steps {
		if step.Status == stepFail {
			t.Errorf("a step failed: %+v", step)
		}
	}
	layout, _ := config.Resolve()
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil || cfg.Repositories.Local != local || cfg.Repositories.Offsite != "" {
		t.Fatalf("%+v %v", cfg, err)
	}
	if report.Rerun || !report.ConfigChanged {
		t.Errorf("%+v", report)
	}
	// The password exists and never appears in the answer.
	password, err := w.store.Get(secrets.RepoPasswordKey)
	if err != nil || password == "" {
		t.Fatalf("no password: %v", err)
	}
	if strings.Contains(r.stdout, password) || strings.Contains(r.stderr, password) {
		t.Error("the password was printed")
	}
	if !report.Password.Created || !report.Password.Stored || report.Password.Shown {
		t.Errorf("%+v", report.Password)
	}
	// Six tasks, pointing to the installed program.
	target := selfinstall.Target(layout.BinDir())
	sched := w.sched.(*fakeScheduler)
	if len(sched.tasks) != len(schedule.JobNames()) || sched.tasks["claude"].Command != target {
		t.Errorf("tasks: %+v", sched.tasks)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "program v1" {
		t.Errorf("the program was not installed: %v", err)
	}
	if fp := w.path.(*fakePath); !slices.Equal(fp.dirs, []string{layout.BinDir()}) {
		t.Errorf("PATH: %v", fp.dirs)
	}
	// The skill is in place and calls the program by its name, since the folder is on the PATH.
	skillFile := filepath.Join(w.home, ".claude", "skills", "backup", "SKILL.md")
	if data, err := os.ReadFile(skillFile); err != nil || !strings.Contains(string(data), "backupclaude status --json") {
		t.Errorf("skill: %v %s", err, data)
	}
	// init, then the first snapshot of the claude profile only.
	calls := strings.Join(w.calls(t), "\n")
	if !strings.Contains(calls, `["init"`) || !strings.Contains(calls, `"claude"`) || strings.Contains(calls, `"projects"`) {
		t.Errorf("calls:\n%s", calls)
	}
	if len(report.Snapshots) != 1 || report.Snapshots[0].SnapshotID != "feedface" || report.Snapshots[0].Plan != nil {
		t.Errorf("%+v", report.Snapshots)
	}
}

func TestSetupHumanOutputGuidesAndShowsNoPassword(t *testing.T) {
	w := newSetupWorld(t)
	r := w.run("setup", "--yes", "--local", w.localRepo(t), "--no-install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"Step 1 of 8", "Step 8 of 8", "Write the password on paper", "backupclaude password --show", "There is no external copy", "Setup is finished."} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the output lacks %q:\n%s", want, r.stdout)
		}
	}
	password, _ := w.store.Get(secrets.RepoPasswordKey)
	if strings.Contains(r.stdout, password) {
		t.Error("the password was printed to a script")
	}
}

func TestSetupInRussian(t *testing.T) {
	w := newSetupWorld(t)
	r := w.run("setup", "--yes", "--local", w.localRepo(t), "--no-install", "--lang", "ru")
	if r.code != 0 || !strings.Contains(r.stdout, "Шаг 1 из 8") || !strings.Contains(r.stdout, "Запишите пароль на бумагу") {
		t.Fatalf("%+v", r)
	}
	layout, _ := config.Resolve()
	if cfg, err := config.Load(layout.ConfigFile()); err != nil || cfg.Language != "ru" {
		t.Errorf("the language was not kept: %+v %v", cfg, err)
	}
}

func TestSetupNeedsATerminalOrYes(t *testing.T) {
	w := newSetupWorld(t)
	for _, args := range [][]string{{"setup"}, {"setup", "--json"}} {
		r := w.run(args...)
		if r.code != exitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if r := w.run("setup", "--yes", "--install", "--no-install"); r.code != exitUsage {
		t.Errorf("contradicting flags: %+v", r)
	}
}

func TestSetupRefusesFoldersOfSyncServices(t *testing.T) {
	root := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	for service, folder := range map[string]string{
		"Yandex Disk":  filepath.Join(root, "Users", "Ivan", "YandexDisk", "backup"),
		"OneDrive":     filepath.Join(root, "Users", "Ivan", "OneDrive", "backup"),
		"Google Drive": filepath.Join(root, "Users", "Ivan", "Google Drive", "backup"),
		"Dropbox":      filepath.Join(root, "Users", "Ivan", "Dropbox", "backup"),
	} {
		w := newSetupWorld(t)
		r := w.run("setup", "--yes", "--json", "--local", folder, "--no-install")
		if r.code == 0 {
			t.Errorf("%s: accepted", service)
			continue
		}
		parsed := r.envelope(t)
		if parsed.OK || parsed.Error == nil || parsed.Error.Code != "setup.local_in_sync" || !strings.Contains(parsed.Error.Message, service) {
			t.Errorf("%s: %+v", service, parsed.Error)
		}
		layout, _ := config.Resolve()
		if _, err := os.Stat(layout.ConfigFile()); err == nil {
			t.Errorf("%s: settings were written", service)
		}
		if _, err := os.Stat(filepath.Join(w.dir, "calls.txt")); err == nil {
			t.Errorf("%s: restic was started", service)
		}
	}
}

func TestSetupSeesSyncFoldersTheOSReports(t *testing.T) {
	w := newSetupWorld(t)
	synced := filepath.Join(t.TempDir(), "Cloud")
	w.syncProbe = func() syncdir.Probe {
		return syncdir.Probe{Roots: []syncdir.Match{{Service: syncdir.Other, Root: synced}}}
	}
	r := w.run("setup", "--yes", "--json", "--local", filepath.Join(synced, "repo"), "--no-install")
	if r.code == 0 || r.envelope(t).Error.Code != "setup.local_in_sync" {
		t.Fatalf("%+v", r)
	}
}

func TestSetupRefusesAFolderWithOtherFiles(t *testing.T) {
	w := newSetupWorld(t)
	busy := filepath.Join(t.TempDir(), "documents")
	fakehome.Write(t, filepath.Join(busy, "report.docx"), "x")
	r := w.run("setup", "--yes", "--json", "--local", busy, "--no-install")
	if r.code == 0 || r.envelope(t).Error.Code != "setup.local_not_empty" {
		t.Fatalf("%+v", r)
	}
}

func TestSetupSuggestsAnotherDisk(t *testing.T) {
	w := newSetupWorld(t)
	spare := t.TempDir()
	w.drives = func() []disk.Drive {
		return []disk.Drive{
			{Root: filepath.Join(t.TempDir(), "system"), Free: 500 << 30, System: true},
			{Root: spare, Free: 200 << 30},
			{Root: filepath.Join(t.TempDir(), "tiny"), Free: 1 << 30},
		}
	}
	r := w.run("setup", "--yes", "--json", "--no-install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if got := w.report(t, r).Local; got != filepath.Join(spare, "ClaudeBackup", "repo") {
		t.Errorf("the default is %s", got)
	}
}

func TestSetupInteractiveAsksAndTakesDefaults(t *testing.T) {
	w := newSetupWorld(t)
	w.terminal = true
	synced := filepath.Join(filepath.VolumeName(t.TempDir())+string(filepath.Separator), "Users", "Ivan", "Dropbox", "repo")
	local := w.localRepo(t)
	// install: Enter (yes); local: a Dropbox folder, refused, then a good one;
	// offsite: Enter (skip); notify: Enter (none); skill: Enter (yes);
	// projects now: Enter (no); show the password: n.
	w.stdin = "\n" + synced + "\n" + local + "\n\n\n\n\nn\n"
	r := w.run("setup")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{
		"Install the program for your user in", "Where to keep the local copy", "lies inside Dropbox",
		"External copy:", "Where to send failure messages?", "Add the /backup skill", "Also save your projects now?",
		"Show the password now", "BackUpClaude setup.",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the dialogue lacks %q:\n%s", want, r.stdout)
		}
	}
	layout, _ := config.Resolve()
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil || cfg.Repositories.Local != local {
		t.Fatalf("%+v %v", cfg, err)
	}
	password, _ := w.store.Get(secrets.RepoPasswordKey)
	if strings.Contains(r.stdout, password) {
		t.Error("the password was shown although the answer was no")
	}
	if _, err := os.Stat(filepath.Join(w.home, ".claude", "skills", "backup", "SKILL.md")); err != nil {
		t.Errorf("the skill was not installed: %v", err)
	}
	if strings.Contains(strings.Join(w.calls(t), "\n"), `"projects"`) {
		t.Error("projects were saved without being asked for")
	}
}

func TestSetupInteractiveCanShowThePasswordToAPerson(t *testing.T) {
	w := newSetupWorld(t)
	w.terminal = true
	// install no, local default, offsite skip, notify none, skill no, projects no, show yes, confirm yes.
	w.stdin = "n\n" + w.localRepo(t) + "\n\n\nn\n\ny\nyes\n"
	r := w.run("setup")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	password, _ := w.store.Get(secrets.RepoPasswordKey)
	if !strings.Contains(r.stdout, password) {
		t.Errorf("the person asked for the password and did not get it:\n%s", r.stdout)
	}
}

func TestSetupSkillIsOnlyInstalledWithConsent(t *testing.T) {
	w := newSetupWorld(t)
	r := w.run("setup", "--yes", "--local", w.localRepo(t), "--no-install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".claude", "skills", "backup")); err == nil {
		t.Error("the skill went in without being asked for")
	}
	if !strings.Contains(r.stdout, "backupclaude skill install") {
		t.Error("the way to add it later is not mentioned")
	}
}

func TestSetupOffsite(t *testing.T) {
	w := newSetupWorld(t)
	local := w.localRepo(t)
	if r := w.run("setup", "--yes", "--json", "--local", local, "--no-install", "--offsite", "nonsense"); r.code == 0 || r.envelope(t).Error.Code != "setup.offsite.invalid" {
		t.Fatalf("a nonsense location: %+v", r)
	}
	r := w.run("setup", "--yes", "--json", "--local", local, "--no-install", "--offsite", "rclone:remote:Backups/claude")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	layout, _ := config.Resolve()
	cfg, _ := config.Load(layout.ConfigFile())
	if cfg.Repositories.Offsite != "rclone:remote:Backups/claude" {
		t.Errorf("%+v", cfg.Repositories)
	}
	step, _ := stepByID(w.report(t, r), "offsite")
	if !strings.Contains(step.Hint, "later version") {
		t.Errorf("the limitation is not stated: %+v", step)
	}
}

func TestSetupTellsPlainlyThereIsNoExternalCopy(t *testing.T) {
	w := newSetupWorld(t)
	r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--no-install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	step, ok := stepByID(w.report(t, r), "offsite")
	if !ok || step.Status != stepWarn || !strings.Contains(step.Message, "copies go with it") {
		t.Errorf("%+v", step)
	}
}

func TestSetupTelegramFromTheInput(t *testing.T) {
	w := newSetupWorld(t)
	token := "123456:test-token-for-unit-tests"
	w.stdin = token + "\n"
	r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--no-install", "--telegram-chat-id", "777", "--telegram-token-stdin")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.stdout, token) || strings.Contains(r.stderr, token) {
		t.Error("the token was printed")
	}
	layout, _ := config.Resolve()
	cfg, _ := config.Load(layout.ConfigFile())
	if cfg.Notify.Telegram == nil || cfg.Notify.Telegram.ChatID != "777" {
		t.Errorf("%+v", cfg.Notify)
	}
	if got, err := w.store.Get("telegram-bot-token"); err != nil || got != token {
		t.Errorf("the token is not in the key store: %v", err)
	}
	if data, _ := os.ReadFile(layout.ConfigFile()); strings.Contains(string(data), token) {
		t.Error("the token is in config.json")
	}
	w2 := newSetupWorld(t)
	w2.stdin = "not a token\n"
	if r := w2.run("setup", "--yes", "--json", "--local", w2.localRepo(t), "--no-install", "--telegram-chat-id", "777", "--telegram-token-stdin"); r.code == 0 || r.envelope(t).Error.Code != "notify.telegram.token_bad" {
		t.Errorf("a bad token: %+v", r)
	}
}

func TestSetupInstallsTheProgramOnceAndPutsItOnThePath(t *testing.T) {
	w := newSetupWorld(t)
	local := w.localRepo(t)
	r := w.run("setup", "--yes", "--json", "--local", local, "--install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	report := w.report(t, r)
	layout, _ := config.Resolve()
	target := selfinstall.Target(layout.BinDir())
	if report.Program == nil || report.Program.Path != target || !report.Program.Placed || !report.Program.PathChanged || !report.Program.InPath {
		t.Fatalf("%+v", report.Program)
	}
	// Running the installed copy again changes nothing.
	w.exe = target
	r = w.run("setup", "--yes", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if again := w.report(t, r); again.Program == nil || again.Program.Placed || again.Program.PathChanged {
		t.Errorf("%+v", again.Program)
	}
	if w.path.(*fakePath).adds != 1 {
		t.Errorf("PATH was written %d times", w.path.(*fakePath).adds)
	}
}

func TestSetupWithoutPathSupportSaysSo(t *testing.T) {
	w := newSetupWorld(t)
	w.path = &fakePath{unsupported: true}
	r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	step, ok := stepByID(w.report(t, r), "path")
	if !ok || step.Status != stepWarn {
		t.Errorf("%+v", step)
	}
}

// existingInstallation is a working installation: settings with personal
// choices, six tasks that match them, snapshots in the repository.
func existingInstallation(t *testing.T, w *setupWorld, program string) (before [32]byte, local string) {
	t.Helper()
	layout, _ := config.Resolve()
	local = filepath.Join(layout.Root, "repo")
	exclude := filepath.Join(w.home, "Big videos")
	extra := filepath.Join(w.home, "bridge")
	cfg := config.Default()
	cfg.Language = "en"
	cfg.Repositories.Local = local
	cfg.Schedule.ClaudeEveryHours = 2
	cfg.Retention.Hourly = 10
	cfg.Sources = config.Sources{Extra: []string{extra, "~/.ssh"}, Exclude: []string{exclude}}
	cfg.Notify.Telegram = &config.Telegram{ChatID: "4242"}
	cfg.Notify.DeletionDropPercent = 30
	if err := config.Save(layout.ConfigFile(), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := w.store.Set(secrets.RepoPasswordKey, "TEST-TEST-TEST-TEST"); err != nil {
		t.Fatal(err)
	}
	if err := w.store.Set("telegram-bot-token", "123456:test-token-for-unit-tests"); err != nil {
		t.Fatal(err)
	}
	sched := w.sched.(*fakeScheduler)
	for _, task := range schedule.Plan(&cfg, program) {
		_ = sched.Install(task)
	}
	sched.installs = 0
	writeSnapshots(t, w.dir, snapshotJSON("aaaa", "claude", time.Now().Add(-time.Hour).Format(time.RFC3339Nano)))
	if err := os.WriteFile(filepath.Join(w.dir, "initialized"), []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fileHash(t, layout.ConfigFile()), local
}

func TestSetupRerunLeavesAWorkingInstallationAlone(t *testing.T) {
	w := newSetupWorld(t)
	program := filepath.Join(t.TempDir(), "elsewhere", "backupclaude.exe")
	fakehome.Write(t, program, "the program the tasks run")
	before, local := existingInstallation(t, w, program)
	layout, _ := config.Resolve()
	infoBefore, _ := os.Stat(layout.ConfigFile())

	r := w.run("setup", "--yes", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	report := w.report(t, r)
	if !report.Rerun || report.ConfigChanged || report.Local != local {
		t.Errorf("%+v", report)
	}
	if fileHash(t, layout.ConfigFile()) != before {
		t.Error("config.json changed")
	}
	if infoAfter, _ := os.Stat(layout.ConfigFile()); !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Error("config.json was rewritten")
	}
	sched := w.sched.(*fakeScheduler)
	if sched.installs != 0 || len(sched.removed) != 0 {
		t.Errorf("the tasks were touched: %d installs, removed %v", sched.installs, sched.removed)
	}
	if sched.tasks["claude"].Command != program {
		t.Errorf("the tasks now run %s", sched.tasks["claude"].Command)
	}
	if step, _ := stepByID(report, "program"); step.Status != stepKept {
		t.Errorf("%+v", step)
	}
	if _, err := os.Stat(selfinstall.Target(layout.BinDir())); err == nil {
		t.Error("the program was copied although the tasks run another one")
	}
	if fp := w.path.(*fakePath); fp.adds != 0 {
		t.Error("PATH was changed on a rerun")
	}
	// A snapshot exists, so no new one is taken.
	if strings.Contains(strings.Join(w.calls(t), "\n"), `["backup"`) {
		t.Error("a snapshot was taken on a rerun")
	}
	if step, _ := stepByID(report, "snapshot"); step.Status != stepKept {
		t.Errorf("%+v", step)
	}
	if password, _ := w.store.Get(secrets.RepoPasswordKey); password != "TEST-TEST-TEST-TEST" {
		t.Error("the password changed")
	}
	if report.Password.Created {
		t.Error("a password was created over the existing one")
	}
}

func TestSetupRerunChangesOnlyWhatIsAnswered(t *testing.T) {
	w := newSetupWorld(t)
	program := filepath.Join(t.TempDir(), "elsewhere", "backupclaude.exe")
	fakehome.Write(t, program, "the program the tasks run")
	_, _ = existingInstallation(t, w, program)
	layout, _ := config.Resolve()
	old, _ := config.Load(layout.ConfigFile())

	r := w.run("setup", "--yes", "--json", "--offsite", "sftp:me@host:/backups")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	changed, err := config.Load(layout.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if changed.Repositories.Offsite != "sftp:me@host:/backups" {
		t.Errorf("%+v", changed.Repositories)
	}
	// Everything else is as it was.
	want := *old
	want.Repositories.Offsite = "sftp:me@host:/backups"
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(changed)
	if string(wantJSON) != string(gotJSON) {
		t.Errorf("more than the answer changed:\nwant %s\ngot  %s", wantJSON, gotJSON)
	}
}

func TestSetupRerunMendsOnlyTheTasksThatDiffer(t *testing.T) {
	w := newSetupWorld(t)
	program := filepath.Join(t.TempDir(), "elsewhere", "backupclaude.exe")
	fakehome.Write(t, program, "the program the tasks run")
	_, _ = existingInstallation(t, w, program)
	sched := w.sched.(*fakeScheduler)
	_ = sched.Remove("check")
	sched.removed = nil
	r := w.run("setup", "--yes", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if sched.installs != 1 || sched.tasks["check"].Command != program {
		t.Errorf("%d installs, check: %+v", sched.installs, sched.tasks["check"])
	}
}

func TestSetupMovesTheProgramOnRequest(t *testing.T) {
	w := newSetupWorld(t)
	program := filepath.Join(t.TempDir(), "elsewhere", "backupclaude.exe")
	fakehome.Write(t, program, "old")
	_, _ = existingInstallation(t, w, program)
	layout, _ := config.Resolve()
	r := w.run("setup", "--yes", "--json", "--install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	target := selfinstall.Target(layout.BinDir())
	sched := w.sched.(*fakeScheduler)
	for name, task := range sched.tasks {
		if task.Command != target {
			t.Errorf("%s still runs %s", name, task.Command)
		}
	}
}

func TestSetupWarnsAboutAnExistingCopyInASyncFolderButGoesOn(t *testing.T) {
	w := newSetupWorld(t)
	program := filepath.Join(t.TempDir(), "elsewhere", "backupclaude.exe")
	fakehome.Write(t, program, "old")
	layout, _ := config.Resolve()
	_, _ = existingInstallation(t, w, program)
	cfg, _ := config.Load(layout.ConfigFile())
	cfg.Repositories.Local = filepath.Join(filepath.VolumeName(layout.Root)+string(filepath.Separator), "OneDrive", "repo")
	if err := config.Save(layout.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	r := w.run("setup", "--yes", "--json")
	if r.code != 0 {
		t.Fatalf("a working installation must not be stopped by it: %+v", r)
	}
	step, ok := stepByID(w.report(t, r), "local")
	if !ok || step.Status != stepWarn || !strings.Contains(step.Message, "OneDrive") {
		t.Errorf("%+v", step)
	}
}

func TestSetupDoesNotOverwriteABrokenConfig(t *testing.T) {
	w := newSetupWorld(t)
	layout, _ := config.Resolve()
	fakehome.Write(t, layout.ConfigFile(), `{"version": 1, "mystery": true}`)
	r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--no-install")
	if r.code == 0 {
		t.Fatalf("%+v", r)
	}
	if data, _ := os.ReadFile(layout.ConfigFile()); string(data) != `{"version": 1, "mystery": true}` {
		t.Errorf("the broken file was replaced: %s", data)
	}
}

func TestSetupReportsAFailedFirstSnapshot(t *testing.T) {
	w := newSetupWorld(t)
	t.Setenv(envFakeMode, "norepo")
	// A home without Claude's data gives nothing to save.
	w.home = t.TempDir()
	r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--no-install")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	parsed := r.envelope(t)
	if parsed.OK {
		t.Error("ok although the snapshot failed")
	}
	step, _ := stepByID(w.report(t, r), "snapshot_claude")
	if step.Status != stepFail {
		t.Errorf("%+v", step)
	}
}

func TestSetupProjectsNow(t *testing.T) {
	w := newSetupWorld(t)
	fakehome.Transcript(t, w.home, "x", "a", filepath.Join(w.home, filepath.FromSlash(fakehome.Repository)))
	r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--no-install", "--projects-now")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if calls := strings.Join(w.calls(t), "\n"); !strings.Contains(calls, `"projects"`) {
		t.Errorf("calls:\n%s", calls)
	}
}

func TestSetupSkipsTheSnapshotOnRequest(t *testing.T) {
	w := newSetupWorld(t)
	r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--no-install", "--no-snapshot")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(strings.Join(w.calls(t), "\n"), `["backup"`) {
		t.Error("a snapshot was taken")
	}
}

func TestSetupWithoutAScheduler(t *testing.T) {
	w := newSetupWorld(t)
	w.sched = noScheduler{}
	r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--no-install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	step, _ := stepByID(w.report(t, r), "schedule")
	if step.Status != stepWarn {
		t.Errorf("%+v", step)
	}
}

// noScheduler is an OS the scheduler does not support yet.
type noScheduler struct{}

func (noScheduler) Install(schedule.Task) error { return errNoScheduler }
func (noScheduler) Remove(string) error         { return nil }
func (noScheduler) List() ([]schedule.Info, error) {
	return nil, errNoScheduler
}
func (noScheduler) Run(string) error { return nil }

func TestSkillTextIsTheSameAsInstalled(t *testing.T) {
	// The wizard installs exactly the text of the skill package.
	w := newSetupWorld(t)
	r := w.run("skill", "install", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	state, err := skill.Status(filepath.Join(w.home, ".claude"), `"`+w.exe+`"`)
	if err != nil || !state.Installed || !state.Ours || !state.Current {
		t.Errorf("%+v %v", state, err)
	}
	if r := w.run("skill", "status", "--json"); r.code != 0 || !strings.Contains(r.stdout, `"current": true`) {
		t.Errorf("%+v", r)
	}
	if r := w.run("skill", "remove", "--json"); r.code != 0 || !strings.Contains(r.stdout, `"changed": true`) {
		t.Errorf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".claude", "skills", "backup")); err == nil {
		t.Error("the skill is still there")
	}
}

func TestSkillInstallNeedsClaude(t *testing.T) {
	w := newSetupWorld(t)
	w.home = t.TempDir()
	if r := w.run("skill", "install", "--json"); r.code == 0 || r.envelope(t).Error.Code != "skill.no_claude" {
		t.Errorf("%+v", r)
	}
}

func TestInstallCommand(t *testing.T) {
	w := newSetupWorld(t)
	r := w.run("install", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var report programReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil || !report.Placed || !report.InPath {
		t.Fatalf("%+v %v", report, err)
	}
	layout, _ := config.Resolve()
	if data, err := os.ReadFile(report.Path); err != nil || string(data) != "program v1" || filepath.Dir(report.Path) != layout.BinDir() {
		t.Errorf("%v", err)
	}
	if r := w.run("install", "--json", "--no-path"); r.code != 0 {
		t.Errorf("%+v", r)
	}
	w2 := newSetupWorld(t)
	if r := w2.run("install", "--no-path", "--json"); r.code != 0 || w2.path.(*fakePath).adds != 0 {
		t.Errorf("--no-path touched the PATH: %+v", r)
	}
}

var errNoScheduler = i18n.NotReady("schedule", 9)
