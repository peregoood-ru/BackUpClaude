package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
)

// installed is a computer that setup has just finished with.
func installed(t *testing.T, w *setupWorld) (repo string) {
	t.Helper()
	repo = w.localRepo(t)
	token := "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef"
	w.stdin = token + "\n"
	r := w.run("setup", "--yes", "--json", "--local", repo, "--install", "--skill", "--telegram-chat-id", "42", "--telegram-token-stdin")
	if r.code != 0 {
		t.Fatalf("setup: %+v", r)
	}
	w.stdin = ""
	fakehome.Write(t, filepath.Join(repo, "data", "pack"), "encrypted copies")
	// The files the installer of the tools leaves.
	layout, _ := config.Resolve()
	fakehome.Write(t, filepath.Join(layout.BinDir(), "restic-0.19.1", "restic.exe"), "restic")
	fakehome.Write(t, filepath.Join(layout.BinDir(), "rclone-1.75.2", "rclone.exe"), "rclone")
	fakehome.Write(t, filepath.Join(layout.BinDir(), "tools.json"), "{}")
	fakehome.Write(t, filepath.Join(layout.CacheDir(), "index"), "cache")
	return repo
}

func TestUninstallRemovesProgramsAndTasksButNotCopiesOrPassword(t *testing.T) {
	w := newSetupWorld(t)
	repo := installed(t, w)
	layout, _ := config.Resolve()
	password, _ := w.store.Get(secrets.RepoPasswordKey)
	sched := w.sched.(*fakeScheduler)
	if len(sched.tasks) != len(schedule.JobNames()) {
		t.Fatalf("setup left %d tasks", len(sched.tasks))
	}

	r := w.run("uninstall", "--yes", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var report uninstallReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil {
		t.Fatal(err)
	}
	if len(sched.tasks) != 0 || len(report.TasksRemoved) != len(schedule.JobNames()) {
		t.Errorf("tasks left: %v, removed %v", sched.tasks, report.TasksRemoved)
	}
	if !report.SkillRemoved || !report.PathRemoved {
		t.Errorf("%+v", report)
	}
	if _, err := os.Stat(filepath.Join(w.home, ".claude", "skills", "backup")); err == nil {
		t.Error("the skill is still there")
	}
	if len(w.path.(*fakePath).dirs) != 0 {
		t.Errorf("PATH still holds %v", w.path.(*fakePath).dirs)
	}
	entries, _ := os.ReadDir(layout.BinDir())
	if len(entries) != 0 {
		t.Errorf("programs left: %v", entries)
	}
	if _, err := os.Stat(layout.CacheDir()); err == nil {
		t.Error("the cache is left")
	}
	if !report.ProgramRemoved {
		t.Errorf("%+v", report)
	}
	// The copies, the settings and the password are untouched.
	if data, err := os.ReadFile(filepath.Join(repo, "data", "pack")); err != nil || string(data) != "encrypted copies" {
		t.Errorf("the repository was touched: %v", err)
	}
	if _, err := os.Stat(layout.ConfigFile()); err != nil {
		t.Errorf("the settings are gone: %v", err)
	}
	if got, err := w.store.Get(secrets.RepoPasswordKey); err != nil || got != password {
		t.Error("the password was touched")
	}
	if !report.PasswordKept || report.KeptRepository != repo {
		t.Errorf("%+v", report)
	}
}

func TestUninstallWarnsAboutThePassword(t *testing.T) {
	w := newSetupWorld(t)
	_ = installed(t, w)
	r := w.run("uninstall", "--yes")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"Credential Manager", "Do not delete it unless you have it on paper", "still in"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the output lacks %q:\n%s", want, r.stdout)
		}
	}
	password, _ := w.store.Get(secrets.RepoPasswordKey)
	if strings.Contains(r.stdout, password) {
		t.Error("the password was printed")
	}
}

func TestUninstallAsksFirstAndCanBeCancelled(t *testing.T) {
	w := newSetupWorld(t)
	_ = installed(t, w)
	w.terminal = true
	w.stdin = "n\n"
	r := w.run("uninstall")
	if r.code == 0 {
		t.Fatalf("cancelled but succeeded: %+v", r)
	}
	for _, want := range []string{"This will:", "NOT touched", "Without it the copies cannot be opened", "Continue?"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("the plan lacks %q:\n%s", want, r.stdout)
		}
	}
	if len(w.sched.(*fakeScheduler).tasks) != len(schedule.JobNames()) {
		t.Error("tasks were removed although the answer was no")
	}
	w.stdin = "y\n"
	if r := w.run("uninstall"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if len(w.sched.(*fakeScheduler).tasks) != 0 {
		t.Error("tasks remain after the yes")
	}
}

func TestUninstallNeedsYesWithoutATerminal(t *testing.T) {
	w := newSetupWorld(t)
	_ = installed(t, w)
	if r := w.run("uninstall"); r.code != exitUsage {
		t.Fatalf("%+v", r)
	}
	if len(w.sched.(*fakeScheduler).tasks) == 0 {
		t.Error("tasks were removed without a yes")
	}
}

func TestUninstallPurgeKeepsThePasswordAndTheCopies(t *testing.T) {
	w := newSetupWorld(t)
	repo := installed(t, w)
	layout, _ := config.Resolve()
	fakehome.Write(t, layout.WatchFile(), "{}")
	fakehome.Write(t, filepath.Join(layout.LogsDir(), "runs.jsonl"), "{}")
	fakehome.Write(t, filepath.Join(layout.Root, "rclone.conf"), "[remote]")
	password, _ := w.store.Get(secrets.RepoPasswordKey)

	r := w.run("uninstall", "--yes", "--purge", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, gone := range []string{layout.ConfigFile(), layout.WatchFile(), layout.LogsDir()} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s is still there", gone)
		}
	}
	if _, err := w.store.Get("telegram-bot-token"); err == nil {
		t.Error("the bot token stays in the key store after a purge")
	}
	if got, err := w.store.Get(secrets.RepoPasswordKey); err != nil || got != password {
		t.Error("a purge touched the password")
	}
	if _, err := os.Stat(filepath.Join(repo, "data", "pack")); err != nil {
		t.Error("a purge touched the copies")
	}
	// The login to an external copy stays: it is needed to read the copy.
	if _, err := os.Stat(filepath.Join(layout.Root, "rclone.conf")); err != nil {
		t.Error("rclone.conf was deleted")
	}
}

func TestUninstallLeavesAForeignSkillAndAForeignProgram(t *testing.T) {
	w := newSetupWorld(t)
	foreign := filepath.Join(w.home, ".claude", "skills", "backup", "SKILL.md")
	fakehome.Write(t, foreign, "---\nname: backup\n---\nmy own skill\n")
	// The tasks run a program that lives elsewhere: it is not ours to delete.
	elsewhere := filepath.Join(t.TempDir(), "backupclaude.exe")
	fakehome.Write(t, elsewhere, "dev build")
	w.exe = elsewhere
	if r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--no-install"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := w.run("uninstall", "--yes", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if data, err := os.ReadFile(foreign); err != nil || !strings.Contains(string(data), "my own skill") {
		t.Errorf("the foreign skill was touched: %v", err)
	}
	if _, err := os.Stat(elsewhere); err != nil {
		t.Errorf("a program outside the application folder was deleted: %v", err)
	}
}
