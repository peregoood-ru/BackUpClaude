package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/watch"
)

// writeSnapshots gives the fake restic a list of snapshots: tag and age.
func (w *backupWorld) writeSnapshots(t *testing.T, ages map[string]time.Duration) {
	t.Helper()
	var items []string
	i := 0
	for tag, age := range ages {
		i++
		items = append(items, fmt.Sprintf(`{"id":"%064d","short_id":"%08d","time":%q,"hostname":"pc","paths":["x"],"tags":[%q]}`,
			i, i, time.Now().Add(-age).Format(time.RFC3339Nano), tag))
	}
	data := "[" + strings.Join(items, ",") + "]"
	if err := os.WriteFile(filepath.Join(w.dir, "snapshots.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (w *backupWorld) statusReport(t *testing.T) statusReport {
	t.Helper()
	r := w.run("status", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var report statusReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil {
		t.Fatalf("%v\n%s", err, r.stdout)
	}
	return report
}

// logRun adds a line to the run log as a scheduled run would.
func logRun(t *testing.T, job string, ok bool, ago time.Duration, result any) {
	t.Helper()
	layout, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	record := runRecord{Time: time.Now().Add(-ago).Format(time.RFC3339), Job: job, OK: ok, Result: result}
	if !ok {
		record.Error = &i18n.Description{Code: "x", Message: "it broke"}
	}
	if err := appendRunLog(layout, record); err != nil {
		t.Fatal(err)
	}
}

func TestStatusSaysAllIsWellAndAdmitsThereIsNoExternalCopy(t *testing.T) {
	w := newBackupWorld(t, "")
	w.writeSnapshots(t, map[string]time.Duration{"claude": 12 * time.Minute, "projects": 5 * time.Hour})
	layout, _ := config.Resolve()
	if err := os.MkdirAll(filepath.Join(layout.Root, "repo"), 0o700); err != nil {
		t.Fatal(err)
	}
	fakehome.Write(t, filepath.Join(layout.Root, "repo", "data", "pack"), strings.Repeat("x", 4096))
	logRun(t, "check", true, 20*time.Hour, map[string]any{"errors": 0})

	report := w.statusReport(t)
	if report.Verdict != verdictOK || len(report.Issues) != 0 {
		t.Fatalf("verdict = %s, issues = %v", report.Verdict, report.Issues)
	}
	if len(report.Copies) != 2 || !report.Copies[0].Reachable || report.Copies[1].Configured {
		t.Fatalf("copies = %+v", report.Copies)
	}
	local := report.Copies[0]
	for _, age := range local.Profiles {
		if age.LastSnap == nil || age.Stale {
			t.Errorf("%+v", age)
		}
		if age.Profile == "claude" && (age.AgeSeconds < 11*60 || age.AgeSeconds > 14*60) {
			t.Errorf("claude age = %d s", age.AgeSeconds)
		}
	}
	if local.SizeBytes == nil || *local.SizeBytes < 4096 {
		t.Errorf("size = %v", local.SizeBytes)
	}
	if len(report.Disks) == 0 || report.Disks[0].Total == 0 {
		t.Errorf("disks = %+v", report.Disks)
	}
	if report.Check.State != "ok" || report.Check.Time == nil {
		t.Errorf("check = %+v", report.Check)
	}

	text := w.run("status")
	if text.code != 0 {
		t.Fatalf("%+v", text)
	}
	for _, want := range []string{"All is well", "Copy outside this computer: not set up", "last snapshot 12 min ago", "last snapshot 5 h ago", "Storage check", "no errors", "Notifications are not set up", "free of"} {
		if !strings.Contains(text.stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, text.stdout)
		}
	}
	if strings.Contains(text.stdout, "status.") || strings.Contains(text.stdout, "%!") {
		t.Errorf("raw key or bad format in:\n%s", text.stdout)
	}
}

func TestStatusIsTranslated(t *testing.T) {
	w := newBackupWorld(t, "")
	w.writeSnapshots(t, map[string]time.Duration{"claude": time.Minute, "projects": time.Minute})
	r := w.run("status", "--lang", "ru")
	if r.code != 0 || !strings.Contains(r.stdout, "Всё в порядке") || !strings.Contains(r.stdout, "не настроена") || !strings.Contains(r.stdout, "ГБ") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.stdout, "status.") || strings.Contains(r.stdout, "%!") {
		t.Errorf("raw key or bad format in:\n%s", r.stdout)
	}
}

func TestStatusSeesAStuckCopy(t *testing.T) {
	w := newBackupWorld(t, "")
	w.writeSnapshots(t, map[string]time.Duration{"claude": 10 * time.Hour, "projects": 2 * time.Hour})
	report := w.statusReport(t)
	if report.Verdict != verdictAttention || !contains(report.Issues, issueStale) {
		t.Fatalf("verdict = %s, issues = %v", report.Verdict, report.Issues)
	}
	text := w.run("status").stdout
	if !strings.Contains(text, "Needs attention") || !strings.Contains(text, "too long ago") {
		t.Errorf("\n%s", text)
	}
}

func TestStatusWithoutSnapshotsAndWithAnUnreachableRepository(t *testing.T) {
	w := newBackupWorld(t, "")
	if report := w.statusReport(t); report.Verdict != verdictAttention || !contains(report.Issues, issueNoSnapshots) {
		t.Errorf("verdict = %s, issues = %v", report.Verdict, report.Issues)
	}
	broken := newBackupWorld(t, "snapshots-fails")
	report := broken.statusReport(t)
	if report.Verdict != verdictProblem || !contains(report.Issues, issueRepoUnreachable) || report.Copies[0].Error == nil {
		t.Fatalf("verdict = %s, issues = %v", report.Verdict, report.Issues)
	}
	text := broken.run("status").stdout
	if !strings.Contains(text, "Problem") || !strings.Contains(text, "Cannot be opened") {
		t.Errorf("\n%s", text)
	}
}

func TestStatusReadsTheCheckAndTheFailuresFromTheRunLog(t *testing.T) {
	w := newBackupWorld(t, "")
	w.writeSnapshots(t, map[string]time.Duration{"claude": time.Minute, "projects": time.Minute})
	logRun(t, "check", false, 30*time.Hour, map[string]any{"errors": 3})
	report := w.statusReport(t)
	if report.Verdict != verdictProblem || report.Check.State != "errors" || report.Check.Errors != 3 || !contains(report.Issues, issueCheckErrors) {
		t.Errorf("%s %+v %v", report.Verdict, report.Check, report.Issues)
	}

	w2 := newBackupWorld(t, "")
	w2.writeSnapshots(t, map[string]time.Duration{"claude": time.Minute, "projects": time.Minute})
	logRun(t, "check", true, 20*24*time.Hour, map[string]any{"errors": 0})
	logRun(t, "cleanup", false, 3*time.Hour, nil)
	logRun(t, "claude", false, 5*time.Hour, nil)
	logRun(t, "claude", true, time.Hour, nil)
	r2 := w2.statusReport(t)
	if !r2.Check.Overdue || !contains(r2.Issues, issueCheckOverdue) {
		t.Errorf("an old check must be called overdue: %+v", r2.Check)
	}
	if len(r2.FailingJobs) != 1 || r2.FailingJobs[0].Job != "cleanup" || r2.FailedRuns != 2 {
		t.Errorf("only a job whose last run failed is failing: %+v, failed runs %d", r2.FailingJobs, r2.FailedRuns)
	}
	if !contains(r2.Issues, issueJobFailing) {
		t.Errorf("issues = %v", r2.Issues)
	}
}

func TestStatusShowsConfiguredChannelsAndRecentAlarms(t *testing.T) {
	w := newBackupWorld(t, "")
	w.writeSnapshots(t, map[string]time.Duration{"claude": time.Minute, "projects": time.Minute})
	w.withTelegram(t)
	layout, _ := config.Resolve()
	_ = watch.Update(layout.WatchFile(), func(s *watch.State) {
		s.SetTranscripts(time.Now().Add(-time.Hour), 42)
		s.AddAlert(time.Now().Add(-2*time.Hour), watch.AlertDeletion, "very old alarm text")
		s.Alerts[0].Time = time.Now().Add(-90 * time.Hour) // outside the window
		s.AddAlert(time.Now().Add(-2*time.Hour), watch.AlertFolder, "fresh alarm text")
	})
	text := w.run("status")
	for _, want := range []string{"Messages about failures go to: Telegram", "fresh alarm text", "Claude sessions on this computer: 42"} {
		if !strings.Contains(text.stdout, want) {
			t.Errorf("status lacks %q:\n%s", want, text.stdout)
		}
	}
	if strings.Contains(text.stdout, "very old alarm text") {
		t.Error("an alarm outside the window must not be shown")
	}
	noSecrets(t, "status", text.stdout, text.stderr)
	if report := w.statusReport(t); report.Verdict != verdictAttention || !contains(report.Issues, issueAlert) {
		t.Errorf("%s %v", report.Verdict, report.Issues)
	}
}

func TestStatusNeedsSettings(t *testing.T) {
	r := newWorld(t).run("status")
	if r.code == 0 || !strings.Contains(r.stderr, "What to do:") {
		t.Errorf("%+v", r)
	}
}

func TestSafeLocationHidesPasswords(t *testing.T) {
	for in, want := range map[string]string{
		`D:\ClaudeBackup\repo`:               `D:\ClaudeBackup\repo`,
		"rclone:yadisk:backup":               "rclone:yadisk:backup",
		"rest:https://user:s3cret@host/repo": "rest:https://user:***@host/repo",
		"sftp:user@host:/srv/repo":           "sftp:user@host:/srv/repo",
	} {
		if got := safeLocation(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestDiskRolesAreMergedAndLowSpaceIsFlagged(t *testing.T) {
	w := newBackupWorld(t, "")
	a := newApp(nil, nil, nil, nil, w.deps())
	session, err := a.openSession()
	if err != nil {
		t.Fatal(err)
	}
	disks := a.readDisks(session)
	if len(disks) == 0 {
		t.Fatal("no disks")
	}
	for _, d := range disks {
		if len(d.Roles) == 0 || d.Total == 0 {
			t.Errorf("%+v", d)
		}
	}
}

func TestVerdictTable(t *testing.T) {
	for _, c := range []struct {
		issues []string
		want   string
	}{
		{nil, verdictOK},
		{[]string{issueStale}, verdictAttention},
		{[]string{issueDiskLow, issueAlert}, verdictAttention},
		{[]string{issueStale, issueRepoUnreachable}, verdictProblem},
		{[]string{issueCheckErrors}, verdictProblem},
		{[]string{issueOffsiteBroken}, verdictProblem},
	} {
		if got := verdictOf(&statusReport{Issues: c.issues}); got != c.want {
			t.Errorf("%v -> %s, want %s", c.issues, got, c.want)
		}
	}
}

func contains(items []string, item string) bool { return containsString(items, item) }

func TestSummaryJobSendsTheStatusEvenWhenAllIsWell(t *testing.T) {
	w := newBackupWorld(t, "")
	w.writeSnapshots(t, map[string]time.Duration{"claude": 5 * time.Minute, "projects": time.Hour})
	server := w.withTelegram(t)
	r := w.run("run", "--job", "summary", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	sent := server.messages()
	if len(sent) != 1 {
		t.Fatalf("sent = %+v", sent)
	}
	for _, want := range []string{"weekly summary, all is well", "last snapshot 5 min ago", "not set up", "Messages about failures go to: Telegram"} {
		if !strings.Contains(sent[0].Text, want) {
			t.Errorf("summary lacks %q:\n%s", want, sent[0].Text)
		}
	}
	noSecrets(t, "summary", sent[0].Text, r.stdout, r.stderr)
	if records := readRunLog(t); len(records) != 1 || records[0].Job != "summary" || !records[0].OK {
		t.Errorf("%+v", records)
	}
}

func TestSummaryWithoutChannelIsNotAFailure(t *testing.T) {
	w := newBackupWorld(t, "")
	if r := w.run("run", "--job", "summary", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestSummaryThatCannotBeDeliveredFailsWithoutTellingAnyone(t *testing.T) {
	w := newBackupWorld(t, "")
	server := w.withTelegram(t)
	server.refuse = true
	r := w.run("run", "--job", "summary", "--json")
	if r.code == 0 {
		t.Fatal("a summary that reaches nobody is a failure of the job")
	}
	noSecrets(t, "summary failure", r.stdout, r.stderr)
	if len(server.messages()) != 0 {
		t.Error("nothing could be sent")
	}
}

func TestSummaryOfABrokenRepositoryStillGoesOut(t *testing.T) {
	w := newBackupWorld(t, "snapshots-fails")
	server := w.withTelegram(t)
	if r := w.run("run", "--job", "summary", "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	sent := server.messages()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "there is a problem") {
		t.Fatalf("a broken repository is exactly what the summary must say: %+v", sent)
	}
}
