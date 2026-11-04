package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/profile"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
)

func TestBackupRemovesStaleLocksBeforeSaving(t *testing.T) {
	w := newBackupWorld(t, "")
	if r := w.run("backup", "--profile", profile.Claude, "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	calls := w.allCalls(t)
	if len(calls) != 2 || !strings.HasPrefix(calls[0], `["unlock"`) || !strings.HasPrefix(calls[1], `["backup"`) {
		t.Fatalf("calls = %v", calls)
	}
	if strings.Contains(calls[0], "remove-all") {
		t.Errorf("--remove-all would break a live run: %s", calls[0])
	}
}

func TestDryRunLeavesLocksAlone(t *testing.T) {
	w := newBackupWorld(t, "")
	if r := w.run("backup", "--profile", profile.Claude, "--dry-run", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	for _, call := range w.allCalls(t) {
		if strings.HasPrefix(call, `["unlock"`) {
			t.Errorf("a dry run touched the locks: %s", call)
		}
	}
}

func TestBackupStopsWhenTheRepositoryCannotBeOpened(t *testing.T) {
	w := newBackupWorld(t, "unlock-fails")
	r := w.run("backup", "--profile", profile.Claude, "--json")
	if r.code == 0 || r.envelope(t).OK {
		t.Fatalf("%+v", r)
	}
	if calls := w.calls(t); len(calls) != 0 {
		t.Errorf("a backup was tried: %v", calls)
	}
}

// readRunLog returns the lines of the run log of this month.
func readRunLog(t *testing.T) []runRecord {
	t.Helper()
	layout, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(layout.LogsDir(), "runs-"+time.Now().Format("2006-01")+".jsonl"))
	if err != nil {
		t.Fatalf("no run log: %v", err)
	}
	defer file.Close()
	var records []runRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var record runRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("bad log line: %v: %s", err, scanner.Text())
		}
		records = append(records, record)
	}
	return records
}

func TestRunSavesAProfileAtLowPriorityAndLogsIt(t *testing.T) {
	w := newBackupWorld(t, "")
	r := w.run("run", "--job", "claude", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if w.lowered != 1 || w.contained != 1 {
		t.Errorf("priority lowered %d times, job object made %d times", w.lowered, w.contained)
	}
	calls := w.allCalls(t)
	if len(calls) != 2 || !strings.HasPrefix(calls[0], `["unlock"`) || !strings.Contains(calls[1], `"claude"`) {
		t.Fatalf("calls = %v", calls)
	}
	records := readRunLog(t)
	if len(records) != 1 || records[0].Job != "claude" || !records[0].OK || records[0].Seconds < 0 || records[0].Time == "" {
		t.Fatalf("log = %+v", records)
	}
	data, err := json.Marshal(records[0].Result)
	if err != nil || !strings.Contains(string(data), `"snapshot_id":"feedface"`) {
		t.Errorf("the log lost the snapshot id: %s", data)
	}
}

func TestRunLogsAFailureAndFails(t *testing.T) {
	w := newBackupWorld(t, "fails")
	r := w.run("run", "--job", "projects", "--json")
	if r.code == 0 {
		t.Fatalf("a failed job must fail: %+v", r)
	}
	records := readRunLog(t)
	if len(records) != 1 || records[0].OK || records[0].Error == nil {
		t.Fatalf("log = %+v", records)
	}
}

func TestRunCleanupAppliesRetentionAndPrunes(t *testing.T) {
	w := newBackupWorld(t, "")
	if r := w.run("run", "--job", "cleanup", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	calls := w.allCalls(t)
	if len(calls) != 2 || !strings.HasPrefix(calls[0], `["unlock"`) || !strings.HasPrefix(calls[1], `["forget"`) {
		t.Fatalf("calls = %v", calls)
	}
	var args []string
	if err := json.Unmarshal([]byte(calls[1]), &args); err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{"--group-by host,tags", "--keep-hourly 48", "--keep-daily 30", "--keep-weekly 12", "--keep-monthly 24", "--prune", "--retry-lock 3h0m0s"} {
		if !strings.Contains(line, want) {
			t.Errorf("forget lacks %q: %s", want, line)
		}
	}
	data, _ := json.Marshal(readRunLog(t)[0].Result)
	if !strings.Contains(string(data), `"removed":2`) {
		t.Errorf("log result = %s", data)
	}
}

func TestRetentionComesFromTheSettings(t *testing.T) {
	w := newBackupWorld(t, "")
	layout, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Retention = config.Retention{Hourly: 6, Daily: 7, Weekly: 0, Monthly: 3}
	if err := config.Save(layout.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	if r := w.run("run", "--job", "cleanup", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	line := w.calls(t)[0]
	for _, want := range []string{`"--keep-hourly","6"`, `"--keep-daily","7"`, `"--keep-monthly","3"`} {
		if !strings.Contains(line, want) {
			t.Errorf("forget lacks %s: %s", want, line)
		}
	}
	if strings.Contains(line, "keep-weekly") {
		t.Errorf("a zero rule must not be passed: %s", line)
	}
}

func TestRunCheckFailsOnRepositoryErrors(t *testing.T) {
	w := newBackupWorld(t, "check-errors")
	r := w.run("run", "--job", "check", "--json")
	if r.code == 0 {
		t.Fatalf("a damaged repository must fail the job: %+v", r)
	}
	records := readRunLog(t)
	if len(records) != 1 || records[0].OK {
		t.Fatalf("log = %+v", records)
	}
	ok := newBackupWorld(t, "")
	if r := ok.run("run", "--job", "check", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestRunRejectsUnknownJob(t *testing.T) {
	w := newBackupWorld(t, "")
	r := w.run("run", "--job", "restore")
	if r.code != exitUsage || !strings.Contains(r.stderr, "claude") {
		t.Fatalf("%+v", r)
	}
	if r := w.run("run"); r.code == 0 {
		t.Errorf("--job is required: %+v", r)
	}
}

// fakeScheduler remembers the tasks it was given.
type fakeScheduler struct {
	tasks   map[string]schedule.Task
	removed []string
	failOn  string
}

func (f *fakeScheduler) Install(task schedule.Task) error {
	if task.Name == f.failOn {
		return errors.New("refused")
	}
	if f.tasks == nil {
		f.tasks = map[string]schedule.Task{}
	}
	f.tasks[task.Name] = task
	return nil
}

func (f *fakeScheduler) Remove(name string) error {
	f.removed = append(f.removed, name)
	delete(f.tasks, name)
	return nil
}

func (f *fakeScheduler) Run(string) error { return nil }

func (f *fakeScheduler) List() ([]schedule.Info, error) {
	var found []schedule.Info
	for _, name := range schedule.JobNames() {
		if task, ok := f.tasks[name]; ok {
			found = append(found, schedule.Info{
				Task: task, Enabled: true, StartWhenAvailable: true, Hidden: true, Headless: true, Priority: 7,
				Status: "Ready", NextRun: "soon", LastRun: "N/A", LastResult: "267011",
			})
		}
	}
	return found, nil
}

func TestScheduleInstallPlacesEveryTask(t *testing.T) {
	w := newBackupWorld(t, "")
	w.sched = &fakeScheduler{}
	r := w.run("schedule", "install", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	tasks := w.sched.(*fakeScheduler).tasks
	if len(tasks) != len(schedule.JobNames()) {
		t.Fatalf("tasks: %v", tasks)
	}
	if got := tasks["claude"]; got.Every != time.Hour || got.Command != "backupclaude" || !slices.Equal(got.Args, []string{"run", "--job", "claude"}) {
		t.Errorf("claude: %+v", got)
	}
	var report scheduleReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil || len(report.Jobs) != len(schedule.JobNames()) {
		t.Fatalf("%v %s", err, r.stdout)
	}
	for _, job := range report.Jobs {
		if !job.Installed || !job.InSync || !job.CatchUp || !job.NoWindow || job.LastResultWords == "" {
			t.Errorf("%+v", job)
		}
	}
}

func TestScheduleFollowsTheSettings(t *testing.T) {
	w := newBackupWorld(t, "")
	w.sched = &fakeScheduler{}
	layout, _ := config.Resolve()
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Schedule.ClaudeEveryHours = 4
	if err := config.Save(layout.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	if r := w.run("schedule", "install"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if got := w.sched.(*fakeScheduler).tasks["claude"].Every; got != 4*time.Hour {
		t.Errorf("claude every %s", got)
	}
	// A change of the settings after the install shows up as out of sync.
	cfg.Schedule.ClaudeEveryHours = 2
	if err := config.Save(layout.ConfigFile(), cfg); err != nil {
		t.Fatal(err)
	}
	r := w.run("schedule", "status", "--json")
	var report scheduleReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil {
		t.Fatal(err)
	}
	for _, job := range report.Jobs {
		if job.Job == "claude" && job.InSync {
			t.Errorf("claude should be out of sync: %+v", job)
		}
		if job.Job == "projects" && !job.InSync {
			t.Errorf("projects should be in sync: %+v", job)
		}
	}
}

func TestScheduleStatusSaysWhatIsMissing(t *testing.T) {
	w := newBackupWorld(t, "")
	w.sched = &fakeScheduler{}
	r := w.run("schedule", "status")
	if r.code != 0 || strings.Count(r.stdout, "not installed") != len(schedule.JobNames()) {
		t.Fatalf("%+v", r)
	}
}

func TestScheduleRemoveKeepsTheCopies(t *testing.T) {
	w := newBackupWorld(t, "")
	f := &fakeScheduler{}
	w.sched = f
	if r := w.run("schedule", "install"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := w.run("schedule", "remove"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if len(f.tasks) != 0 || len(f.removed) != len(schedule.JobNames()) {
		t.Errorf("tasks=%v removed=%v", f.tasks, f.removed)
	}
	if len(w.allCalls(t)) != 0 {
		t.Error("removing the tasks must not touch the repository")
	}
}

func TestScheduleInstallReportsARefusal(t *testing.T) {
	w := newBackupWorld(t, "")
	w.sched = &fakeScheduler{failOn: "check"}
	r := w.run("schedule", "install")
	if r.code == 0 {
		t.Fatalf("%+v", r)
	}
}

func TestPlainCommandsDoNotChangePriority(t *testing.T) {
	w := newBackupWorld(t, "")
	if r := w.run("backup", "--profile", profile.Claude, "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if w.lowered != 0 || w.contained != 0 {
		t.Error("a backup started by a person is not lowered: only the scheduled run is")
	}
}
