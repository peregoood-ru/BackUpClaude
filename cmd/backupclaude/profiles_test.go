package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
)

// projectsOff switches the projects off in the settings, as a person would.
func projectsOff(c *config.Config) { c.Profiles.Projects = false }

func TestScheduleInstallRemovesTheTaskOfAProfileThatIsOff(t *testing.T) {
	w := newBackupWorld(t, "")
	f := &fakeScheduler{}
	w.sched = f
	if r := w.run("schedule", "install"); r.code != 0 || len(f.tasks) != len(schedule.JobNames()) {
		t.Fatalf("%+v %v", r, f.tasks)
	}
	saveConfig(t, projectsOff)
	// Before the install the leftover task is reported, not hidden.
	var before scheduleReport
	r := w.run("schedule", "status", "--json")
	if err := json.Unmarshal(r.envelope(t).Data, &before); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range before.Jobs {
		if job.Job == "projects" {
			found = true
			if !job.Disabled || !job.Installed || job.InSync {
				t.Errorf("a task left for a profile that is off must show as out of sync: %+v", job)
			}
		}
	}
	if !found {
		t.Error("the projects job is not in the report")
	}
	if r := w.run("schedule", "install"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if _, left := f.tasks["projects"]; left || len(f.tasks) != len(schedule.JobNames())-1 {
		t.Errorf("tasks: %v", f.tasks)
	}
	text := w.run("schedule", "status")
	if !strings.Contains(text.stdout, "projects: switched off in the settings") || strings.Contains(text.stdout, "not installed") {
		t.Errorf("%s", text.stdout)
	}
	var after scheduleReport
	if err := json.Unmarshal(w.run("schedule", "status", "--json").envelope(t).Data, &after); err != nil {
		t.Fatal(err)
	}
	for _, job := range after.Jobs {
		if !job.InSync {
			t.Errorf("%+v", job)
		}
	}
	// And back on: the task returns.
	saveConfig(t, nil)
	if r := w.run("schedule", "install"); r.code != 0 || f.tasks["projects"].Name != "projects" {
		t.Fatalf("%+v %v", r, f.tasks)
	}
}

func TestBackupTakesOnlyTheProfilesThatAreOn(t *testing.T) {
	w := newBackupWorld(t, "")
	saveConfig(t, projectsOff)
	r := w.run("backup", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	report := w.report(t, r)
	if len(report.Profiles) != 1 || report.Profiles[0].Profile != "claude" {
		t.Fatalf("%+v", report.Profiles)
	}
	for _, call := range w.calls(t) {
		if strings.Contains(call, `"projects"`) {
			t.Errorf("a backup of projects was started: %s", call)
		}
	}
}

func TestBackupOfAProfileThatIsOffExplainsWhy(t *testing.T) {
	w := newBackupWorld(t, "")
	saveConfig(t, projectsOff)
	r := w.run("backup", "--profile", "projects")
	if r.code == 0 || !strings.Contains(r.stdout+r.stderr, "switched off in the settings") {
		t.Fatalf("%+v", r)
	}
	if len(w.calls(t)) != 0 {
		t.Errorf("restic was called: %v", w.calls(t))
	}
	ru := w.run("backup", "--profile", "projects", "--lang", "ru")
	if !strings.Contains(ru.stdout+ru.stderr, "выключен в настройках") {
		t.Errorf("%+v", ru)
	}
}

func TestRunOfAJobWhoseProfileIsOffDoesNothing(t *testing.T) {
	w := newBackupWorld(t, "")
	saveConfig(t, projectsOff)
	r := w.run("run", "--job", "projects")
	if r.code != 0 || !strings.Contains(r.stdout, "switched off in the settings") {
		t.Fatalf("%+v", r)
	}
	if len(w.allCalls(t)) != 0 {
		t.Errorf("restic was called: %v", w.allCalls(t))
	}
	records := readRunLog(t)
	if len(records) != 1 || !records[0].OK || records[0].Job != "projects" {
		t.Errorf("%+v", records)
	}
	// Claude's job still works.
	if r := w.run("run", "--job", "claude"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestStatusDoesNotCallAProfileThatIsOffLate(t *testing.T) {
	w := newBackupWorld(t, "")
	// Old snapshots of the projects, far past three intervals; Claude's is fresh.
	w.writeSnapshots(t, map[string]time.Duration{"claude": 12 * time.Minute, "projects": 30 * 24 * time.Hour})
	logRun(t, "check", true, 20*time.Hour, map[string]any{"errors": 0})
	logRun(t, "projects", false, 3*time.Hour, nil)

	// On, it is late and its last run failed.
	on := w.statusReport(t)
	if on.Verdict == verdictOK {
		t.Fatalf("a stale profile that is on must be reported: %+v", on.Issues)
	}
	saveConfig(t, projectsOff)
	off := w.statusReport(t)
	if off.Verdict != verdictOK || len(off.Issues) != 0 || len(off.FailingJobs) != 0 {
		t.Fatalf("verdict %s, issues %v, failing %v", off.Verdict, off.Issues, off.FailingJobs)
	}
	var seen bool
	for _, age := range off.Copies[0].Profiles {
		if age.Profile == "projects" {
			seen = true
			if !age.Disabled || age.Stale || age.LastSnap == nil {
				t.Errorf("%+v", age)
			}
		}
	}
	if !seen {
		t.Error("the old snapshots of the profile that is off should still be listed")
	}
	text := w.run("status")
	if !strings.Contains(text.stdout, "All is well") || !strings.Contains(text.stdout, "switched off in the settings") {
		t.Errorf("%s", text.stdout)
	}
}

func TestStatusStaysQuietAboutAProfileThatWasNeverSaved(t *testing.T) {
	w := newBackupWorld(t, "")
	saveConfig(t, projectsOff)
	w.writeSnapshots(t, map[string]time.Duration{"claude": time.Minute})
	logRun(t, "check", true, time.Hour, map[string]any{"errors": 0})
	report := w.statusReport(t)
	if report.Verdict != verdictOK {
		t.Fatalf("%v", report.Issues)
	}
	for _, age := range report.Copies[0].Profiles {
		if age.Profile == "projects" {
			t.Errorf("a profile that is off and was never saved is listed: %+v", age)
		}
	}
	// Old snapshots of a profile that is off do not stand in for Claude's own.
	w.writeSnapshots(t, map[string]time.Duration{"projects": time.Minute})
	if got := w.statusReport(t); got.Verdict == verdictOK {
		t.Errorf("snapshots of a profile that is off must not count as Claude's backup: %v", got.Issues)
	}
}

func TestSetupNewInstallationSavesOnlyClaudeByDefault(t *testing.T) {
	w := newSetupWorld(t)
	r := w.run("setup", "--yes", "--json", "--local", w.localRepo(t), "--no-install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	layout, _ := config.Resolve()
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil || cfg.Profiles != (config.Profiles{Claude: true}) {
		t.Fatalf("%+v %v", cfg, err)
	}
	sched := w.sched.(*fakeScheduler)
	if _, there := sched.tasks["projects"]; there || len(sched.tasks) != len(schedule.JobNames())-1 {
		t.Errorf("tasks: %v", sched.tasks)
	}
	if strings.Contains(strings.Join(w.calls(t), "\n"), `"projects"`) {
		t.Error("projects were saved")
	}
}

func TestSetupRerunKeepsTheChoiceAndChangesItOnRequest(t *testing.T) {
	w := newSetupWorld(t)
	local := w.localRepo(t)
	if r := w.run("setup", "--yes", "--json", "--local", local, "--no-install", "--save", "all"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	sched := w.sched.(*fakeScheduler)
	if len(sched.tasks) != len(schedule.JobNames()) {
		t.Fatalf("tasks: %v", sched.tasks)
	}
	// A rerun without the flag keeps both on.
	r := w.run("setup", "--yes", "--json", "--local", local, "--no-install")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	step, _ := stepByID(w.report(t, r), "profiles")
	if step.Status != stepKept || len(sched.tasks) != len(schedule.JobNames()) {
		t.Errorf("%+v %v", step, sched.tasks)
	}
	// The flag turns the projects off and the task goes.
	if r := w.run("setup", "--yes", "--json", "--local", local, "--no-install", "--save", "claude"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	layout, _ := config.Resolve()
	cfg, _ := config.Load(layout.ConfigFile())
	if cfg.Profiles.Projects || !cfg.Profiles.Claude {
		t.Errorf("%+v", cfg.Profiles)
	}
	if _, there := sched.tasks["projects"]; there {
		t.Errorf("tasks: %v", sched.tasks)
	}
}

func TestSetupRejectsBadSaveValues(t *testing.T) {
	w := newSetupWorld(t)
	local := w.localRepo(t)
	for _, args := range [][]string{
		{"--save", "everything"},
		{"--save", "claude", "--projects-now"},
	} {
		r := w.run(append([]string{"setup", "--yes", "--json", "--local", local, "--no-install"}, args...)...)
		if r.code != exitUsage {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestSetupAsksWhatToSaveAndShowsTheCurrentChoice(t *testing.T) {
	w := newSetupWorld(t)
	w.terminal = true
	local := w.localRepo(t)
	// install: no; what to save: 2 (with projects); local; offsite; notify;
	// skill: no; projects now: no; password: no.
	w.stdin = "n\n2\n" + local + "\n\n\nn\nn\nn\n"
	r := w.run("setup")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"What to save?", "1. Only Claude's data", "2. Claude's data and the folders of your projects", "Also save your projects now?"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("lacks %q:\n%s", want, r.stdout)
		}
	}
	layout, _ := config.Resolve()
	if cfg, _ := config.Load(layout.ConfigFile()); cfg.Profiles != (config.Profiles{Claude: true, Projects: true}) {
		t.Fatalf("%+v", cfg.Profiles)
	}
	// The rerun shows the current choice and Enter keeps it.
	w.stdin = "n\n\n\n\n\nn\nn\n"
	again := w.run("setup")
	if again.code != 0 || !strings.Contains(again.stdout, "Now saved: Claude's data and the folders of your projects") {
		t.Fatalf("%+v", again)
	}
	if cfg, _ := config.Load(layout.ConfigFile()); cfg.Profiles != (config.Profiles{Claude: true, Projects: true}) {
		t.Errorf("Enter changed the choice: %+v", cfg.Profiles)
	}
}

func TestUIProfilesSwitchTheTasksAtOnce(t *testing.T) {
	w := newWorld(t)
	w.exe = "backupclaude"
	saveConfig(t, nil)
	f := &fakeScheduler{}
	w.sched = f
	p := startPage(t, w)
	p.enter()

	_, settings, _ := p.call("GET", "/api/settings", nil)
	if !strings.Contains(string(settings.Data), `"profiles":{"claude":true,"projects":true}`) {
		t.Fatalf("%s", settings.Data)
	}
	if code, saved, body := p.call("PUT", "/api/settings", map[string]any{"profiles": map[string]bool{"claude": true, "projects": false}}); code != http.StatusOK || !saved.OK {
		t.Fatalf("%d %s", code, body)
	}
	layout, _ := config.Resolve()
	if cfg, _ := config.Load(layout.ConfigFile()); cfg.Profiles.Projects || !cfg.Profiles.Claude {
		t.Errorf("not saved: %+v", cfg.Profiles)
	}
	if len(f.removed) != 1 || f.removed[0] != "projects" {
		t.Errorf("removed %v", f.removed)
	}
	// On again: the task of the projects is installed and the others are not touched.
	f.installs = 0
	if code, saved, body := p.call("PUT", "/api/settings", map[string]any{"profiles": map[string]bool{"claude": true, "projects": true}}); code != http.StatusOK || !saved.OK {
		t.Fatalf("%d %s", code, body)
	}
	if f.installs != 1 || f.tasks["projects"].Name != "projects" {
		t.Errorf("installs %d, tasks %v", f.installs, f.tasks)
	}
	// Nothing at all is refused and changes nothing.
	f.removed = nil
	code, refused, _ := p.call("PUT", "/api/settings", map[string]any{"profiles": map[string]bool{"claude": false, "projects": false}})
	if code == http.StatusOK && refused.OK {
		t.Fatal("switching everything off was accepted")
	}
	if cfg, _ := config.Load(layout.ConfigFile()); !cfg.Profiles.Claude || !cfg.Profiles.Projects {
		t.Errorf("a refused change was saved: %+v", cfg.Profiles)
	}
	if len(f.removed) != 0 {
		t.Errorf("tasks were removed: %v", f.removed)
	}
}

func TestUIProfilesAreRolledBackWhenTheTaskCannotBeChanged(t *testing.T) {
	w := newWorld(t)
	w.exe = "backupclaude"
	saveConfig(t, projectsOff)
	w.sched = &fakeScheduler{failOn: "projects"}
	p := startPage(t, w)
	p.enter()
	_, failed, _ := p.call("PUT", "/api/settings", map[string]any{"profiles": map[string]bool{"claude": true, "projects": true}})
	if failed.OK {
		t.Fatal("a refusal of the scheduler was reported as success")
	}
	layout, _ := config.Resolve()
	if cfg, _ := config.Load(layout.ConfigFile()); cfg.Profiles.Projects {
		t.Error("the setting says on, but there is no task")
	}
}
