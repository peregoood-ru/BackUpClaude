package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/profile"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
	"github.com/peregoood-ru/BackUpClaude/locales"
)

const (
	envFakeRestic = "BACKUPCLAUDE_FAKE_RESTIC_DIR"
	envFakeMode   = "BACKUPCLAUDE_FAKE_RESTIC_MODE"
)

// TestMain lets the test binary play restic.
func TestMain(m *testing.M) {
	if dir := os.Getenv(envFakeRestic); dir != "" {
		fakeRestic(dir, os.Getenv(envFakeMode))
		return
	}
	os.Exit(m.Run())
}

const fakeSummary = `{"message_type":"summary","files_new":2,"files_changed":1,"total_files_processed":3,"total_bytes_processed":3000,"data_added":100,"data_added_packed":90,"total_duration":1.5,"snapshot_id":"feedface"}`

// fakeRestic records each call and answers by command.
func fakeRestic(dir, mode string) {
	command := os.Args[1]
	record, _ := json.Marshal(os.Args[1:])
	file, _ := os.OpenFile(filepath.Join(dir, "calls.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintf(file, "%s\n", record)
	_ = file.Close()
	switch command {
	case "backup":
		fakeBackup(os.Args, dir, mode)
	case "snapshots":
		data, err := os.ReadFile(filepath.Join(dir, "snapshots.json"))
		if err != nil {
			fmt.Println("[]")
			return
		}
		fmt.Println(string(data))
	case "ls":
		data, _ := os.ReadFile(filepath.Join(dir, "nodes.jsonl"))
		fmt.Print(string(data))
	}
}

func fakeBackup(args []string, dir, mode string) {
	for i, arg := range args {
		if arg == "--iexclude-file" {
			data, _ := os.ReadFile(args[i+1])
			_ = os.WriteFile(filepath.Join(dir, "excludes.txt"), data, 0o600)
		}
	}
	switch mode {
	case "locked":
		fmt.Fprintln(os.Stderr, `{"message_type":"error","error":{"message":"locked"},"during":"archival","item":"`+jsonEscaped(os.Getenv(envLockedFile))+`"}`)
		fmt.Println(fakeSummary)
		os.Exit(3)
	case "fails":
		fmt.Fprintln(os.Stderr, `{"message_type":"exit_error","code":1,"message":"disk is on fire"}`)
		os.Exit(1)
	default:
		fmt.Println(fakeSummary)
	}
}

const envLockedFile = "BACKUPCLAUDE_FAKE_LOCKED_FILE"

func jsonEscaped(value string) string {
	data, _ := json.Marshal(value)
	return strings.Trim(string(data), `"`)
}

type backupWorld struct {
	*world
	dir string
}

// newBackupWorld is a computer with settings, a fake home and a fake restic.
func newBackupWorld(t *testing.T, mode string) *backupWorld {
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
	t.Setenv(envFakeMode, mode)
	saveConfig(t, nil)
	return &backupWorld{w, dir}
}

func (w *backupWorld) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(w.dir, "calls.txt"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (w *backupWorld) report(t *testing.T, r result) backupReport {
	t.Helper()
	var report backupReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil {
		t.Fatalf("%v\n%s", err, r.stdout)
	}
	return report
}

func TestBackupRunsBothProfiles(t *testing.T) {
	w := newBackupWorld(t, "")
	project := filepath.Join(w.home, filepath.FromSlash(fakehome.Repository))
	fakehome.Transcript(t, w.home, "x", "a", project)

	r := w.run("backup", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	report := w.report(t, r)
	if report.DryRun || len(report.Profiles) != 2 {
		t.Fatalf("%+v", report)
	}
	for _, item := range report.Profiles {
		if item.Status != backupOK || item.SnapshotID != "feedface" || item.Files != 3 || item.DataAdded != 90 {
			t.Errorf("%+v", item)
		}
	}
	calls := w.calls(t)
	if len(calls) != 2 || !strings.Contains(calls[0], `"claude"`) || !strings.Contains(calls[1], `"projects"`) {
		t.Fatalf("calls = %v", calls)
	}
	if !strings.Contains(calls[1], jsonEscaped(project)) {
		t.Errorf("the project did not reach restic: %s", calls[1])
	}

	layout, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(layout.ProjectsFile()); err != nil || !strings.Contains(string(data), jsonEscaped(project)) {
		t.Errorf("state: %s %v", data, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(layout.TempDir(), "excludes-*"))
	if err != nil || len(leftovers) != 0 {
		t.Errorf("exclude files left behind: %v %v", leftovers, err)
	}
}

func TestBackupOneProfile(t *testing.T) {
	w := newBackupWorld(t, "")
	r := w.run("backup", "--profile", profile.Claude, "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if report := w.report(t, r); len(report.Profiles) != 1 || report.Profiles[0].Profile != profile.Claude {
		t.Fatalf("%+v", report)
	}
	if calls := w.calls(t); len(calls) != 1 || strings.Contains(calls[0], `"projects"`) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestBackupPassesExclusionsToRestic(t *testing.T) {
	w := newBackupWorld(t, "")
	if r := w.run("backup", "--profile", profile.Claude, "--json"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	data, err := os.ReadFile(filepath.Join(w.dir, "excludes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), filepath.Join(w.home, ".claude", ".credentials.json")) {
		t.Errorf("excludes:\n%s", data)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	w := newBackupWorld(t, "")
	fakehome.Transcript(t, w.home, "x", "a", filepath.Join(w.home, "проект"))
	fakehome.Write(t, filepath.Join(w.home, "проект", "a.txt"), "x")
	r := w.run("backup", "--dry-run", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	report := w.report(t, r)
	if !report.DryRun || report.Profiles[0].Status != backupPlanned {
		t.Fatalf("%+v", report)
	}
	for _, call := range w.calls(t) {
		if !strings.Contains(call, `"--dry-run"`) {
			t.Errorf("call without --dry-run: %s", call)
		}
	}
	layout, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.ProjectsFile()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a dry run wrote the state: %v", err)
	}
}

func TestBackupOutputForAPerson(t *testing.T) {
	w := newBackupWorld(t, "")
	r := w.run("backup", "--profile", profile.Claude)
	if r.code != 0 || !strings.Contains(r.stdout, "Profile claude: saved") || !strings.Contains(r.stdout, "feedface") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.stdout, "plan.") || strings.Contains(r.stdout, "backup.") {
		t.Errorf("raw key:\n%s", r.stdout)
	}
}

func TestLockedFileAllowedByTheManifestIsAWarning(t *testing.T) {
	w := newBackupWorld(t, "locked")
	// On every OS the manifest allows locks in Local Storage of a Desktop folder.
	root := filepath.Join(w.home, filepath.FromSlash(fakehome.StoreRoot))
	allowed := filepath.Join(root, "Local Storage", "leveldb", "LOCK")
	t.Setenv(envLockedFile, allowed)
	r := w.run("backup", "--profile", profile.Claude, "--json")
	report := w.report(t, r)
	item := report.Profiles[0]
	if !isWindowsManifest(t) {
		if r.code != 1 || item.Status != backupIncomplete {
			t.Fatalf("no Desktop root on this OS, so it is a gap: %+v", item)
		}
		return
	}
	if r.code != 0 || item.Status != backupLocked || len(item.LockedFiles) != 1 {
		t.Fatalf("%+v", item)
	}
}

func isWindowsManifest(t *testing.T) bool {
	t.Helper()
	return filepath.Separator == '\\'
}

func TestAnyOtherUnreadableFileIsAFailure(t *testing.T) {
	w := newBackupWorld(t, "locked")
	gap := filepath.Join(w.home, ".claude", "projects", "x", "a.jsonl")
	t.Setenv(envLockedFile, gap)
	r := w.run("backup", "--profile", profile.Claude, "--json")
	if r.code != 1 {
		t.Fatalf("a gap in a snapshot must not exit 0: %+v", r)
	}
	item := w.report(t, r).Profiles[0]
	if item.Status != backupIncomplete || len(item.SkippedFiles) != 1 || item.SnapshotID != "feedface" {
		t.Fatalf("%+v", item)
	}
	text := w.run("backup", "--profile", profile.Claude)
	if !strings.Contains(text.stdout, "NOT SAVED") || !strings.Contains(text.stdout, "a.jsonl") {
		t.Errorf("%+v", text)
	}
}

func TestFailureOfOneProfileLetsTheOtherRun(t *testing.T) {
	w := newBackupWorld(t, "fails")
	fakehome.Transcript(t, w.home, "x", "a", filepath.Join(w.home, filepath.FromSlash(fakehome.PlainFolder)))
	r := w.run("backup", "--json")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	report := w.report(t, r)
	if len(report.Profiles) != 2 || len(w.calls(t)) != 2 {
		t.Fatalf("%+v calls=%v", report, w.calls(t))
	}
	if report.Profiles[0].Error == nil || !strings.Contains(report.Profiles[0].Error.Message, "disk is on fire") {
		t.Errorf("%+v", report.Profiles[0])
	}
}

func TestBackupRefusesUnknownProfile(t *testing.T) {
	w := newBackupWorld(t, "")
	r := w.run("backup", "--profile", "everything", "--json")
	if r.code != exitUsage || r.envelope(t).Error.Code != "backup.unknown_profile" {
		t.Fatalf("%+v", r)
	}
	if len(w.calls(t)) != 0 {
		t.Errorf("calls = %v", w.calls(t))
	}
}

func TestBackupWithNothingToSave(t *testing.T) {
	w := newBackupWorld(t, "")
	w.home = t.TempDir()
	r := w.run("backup", "--json")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	for _, item := range w.report(t, r).Profiles {
		if item.Status != backupFailed || item.Error == nil || item.Error.Code != "backup.nothing_to_save" {
			t.Errorf("%+v", item)
		}
	}
}

func writeSnapshots(t *testing.T, dir string, snapshots ...string) {
	t.Helper()
	data := "[" + strings.Join(snapshots, ",") + "]"
	if err := os.WriteFile(filepath.Join(dir, "snapshots.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func snapshotJSON(id, tag, when string) string {
	return fmt.Sprintf(`{"id":"%s00","short_id":"%s","time":"%s","hostname":"pc","paths":["/x"],"tags":["%s"],"summary":{"total_files_processed":5,"total_bytes_processed":2500}}`, id, id, when, tag)
}

func nodeJSON(path, kind string, size int64) string {
	return fmt.Sprintf(`{"name":%q,"type":%q,"path":%q,"size":%d,"mtime":"2026-11-01T10:00:00+03:00","struct_type":"node"}`,
		path[strings.LastIndexByte(path, '/')+1:], kind, path, size)
}

func TestListShowsAllThree(t *testing.T) {
	w := newBackupWorld(t, "")
	project := filepath.Join(w.home, filepath.FromSlash(fakehome.PlainFolder))
	fakehome.Transcript(t, w.home, "folder", "on-disk-session", project)
	writeSnapshots(t, w.dir,
		snapshotJSON("aaaa1111", profile.Claude, "2026-11-01T09:00:00+03:00"),
		snapshotJSON("bbbb2222", profile.Projects, "2026-11-01T10:00:00+03:00"))
	root := restore.SnapshotPath(filepath.Join(w.home, ".claude", "projects"))
	nodes := []string{
		`{"struct_type":"snapshot","short_id":"aaaa1111"}`,
		nodeJSON(root+"/folder", "dir", 0),
		nodeJSON(root+"/folder/on-disk-session.jsonl", "file", 10),
		nodeJSON(root+"/old/deleted-session.jsonl", "file", 20),
		nodeJSON(root+"/old/deleted-session/subagents/agent-1.jsonl", "file", 30),
	}
	if err := os.WriteFile(filepath.Join(w.dir, "nodes.jsonl"), []byte(strings.Join(nodes, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := w.run("list", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var data listResult
	if err := json.Unmarshal(r.envelope(t).Data, &data); err != nil {
		t.Fatal(err)
	}
	snapshots := *data.Snapshots
	if len(snapshots) != 2 || snapshots[0].ID != "bbbb2222" || snapshots[0].Profile != profile.Projects || snapshots[0].Files != 5 {
		t.Fatalf("%+v", snapshots)
	}
	byID := map[string]sessionRow{}
	for _, row := range *data.Sessions {
		byID[row.ID] = row
	}
	if row := byID["on-disk-session"]; !row.OnDisk || !row.InSnapshot || row.Cwd != project {
		t.Errorf("%+v", row)
	}
	if row := byID["deleted-session"]; row.OnDisk || !row.InSnapshot {
		t.Errorf("a session Claude deleted must stay visible: %+v", row)
	}
	if _, ok := byID["agent-1"]; ok {
		t.Error("a subagent is not a session")
	}
	found := false
	for _, row := range *data.Projects {
		if row.Path == project && row.Saved && row.Source == profile.SourceProject {
			found = true
		}
	}
	if !found {
		t.Errorf("%+v", *data.Projects)
	}
}

func TestListOfProjectsNeedsNoRepository(t *testing.T) {
	w := newBackupWorld(t, "")
	w.restic = ""
	r := w.run("list", "--projects")
	if r.code != 0 || !strings.Contains(r.stdout, "Projects:") {
		t.Fatalf("%+v", r)
	}
}

func TestListTextLimitsLines(t *testing.T) {
	w := newBackupWorld(t, "")
	for i := range 5 {
		writeSnapshots(t, w.dir, snapshotJSON("cccc000"+fmt.Sprint(i), profile.Claude, "2026-11-01T09:00:00+03:00"))
		fakehome.Transcript(t, w.home, "x", fmt.Sprintf("session-%d", i), "/nowhere")
	}
	r := w.run("list", "--sessions", "--limit", "2")
	if r.code != 0 || strings.Count(r.stdout, "session-") != 2 || !strings.Contains(r.stdout, "Sessions:") {
		t.Fatalf("%+v", r)
	}
}

func TestListShowsAProjectThatIsNotSaved(t *testing.T) {
	w := newBackupWorld(t, "")
	gone := filepath.Join(w.home, "удалённый")
	fakehome.Transcript(t, w.home, "x", "a", gone)
	r := w.run("list", "--projects")
	if r.code != 0 || !strings.Contains(r.stdout, "NOT SAVED") || !strings.Contains(r.stdout, gone) {
		t.Fatalf("%+v", r)
	}
}

func TestMeasureCommand(t *testing.T) {
	w := newBackupWorld(t, "")
	plain := filepath.Join(w.home, filepath.FromSlash(fakehome.PlainFolder))
	fakehome.Transcript(t, w.home, "x", "a", plain)
	r := w.run("measure", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	var data map[string]*profile.Measurement
	if err := json.Unmarshal(r.envelope(t).Data, &data); err != nil {
		t.Fatal(err)
	}
	if data[profile.Projects].Files == 0 {
		t.Errorf("%+v", data[profile.Projects])
	}
	if text := w.run("measure", "--heavy-gb", "0"); text.code != 0 || !strings.Contains(text.stdout, "projects:") {
		t.Errorf("%+v", text)
	}
}

func TestVerifyCommand(t *testing.T) {
	w := newBackupWorld(t, "")
	writeSnapshots(t, w.dir, snapshotJSON("aaaa1111", profile.Claude, "2026-11-01T09:00:00+03:00"))
	banned := restore.SnapshotPath(filepath.Join(w.home, ".claude", ".credentials.json"))
	if err := os.WriteFile(filepath.Join(w.dir, "nodes.jsonl"), []byte(nodeJSON(banned, "file", 5)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := w.run("verify", "--profile", profile.Claude, "--find", "vm_bundles")
	if r.code != 1 || !strings.Contains(r.stdout, "MUST NOT BE IN THE SNAPSHOT") || !strings.Contains(r.stdout, "vm_bundles: found 0") {
		t.Fatalf("%+v", r)
	}
	asJSON := w.run("verify", "--profile", profile.Claude, "--json")
	if asJSON.code != 1 || asJSON.envelope(t).OK {
		t.Fatalf("%+v", asJSON)
	}
}

func TestVerifyWithoutSnapshots(t *testing.T) {
	w := newBackupWorld(t, "")
	r := w.run("verify", "--json")
	if r.code != 1 || r.envelope(t).Error.Code != "verify.no_snapshot" {
		t.Fatalf("%+v", r)
	}
}

func TestDoctorReportsDesktopData(t *testing.T) {
	w := newWorld(t)
	w.home = fakehome.Copy(t, fakehome.NoDesktop)
	_, checks := doctorChecks(t, w.run("doctor", "--json"))
	if checks["claude_data"].Status != statusOK || checks["desktop_data"].Status != statusSkip {
		t.Errorf("%+v", checks)
	}
}

// Keys built at run time escape the scan of the sources in the locales tests.
func TestComputedKeysExist(t *testing.T) {
	en := locales.Catalog("en")
	keys := []string{}
	for _, code := range profile.WarningCodes() {
		keys = append(keys, "plan.warning."+code)
	}
	for _, status := range []string{backupOK, backupLocked, backupIncomplete, backupFailed, backupPlanned} {
		keys = append(keys, "backup.status."+status)
	}
	for _, reason := range []string{"home", "root", "not_project", "foreign"} {
		keys = append(keys, "backup.skip."+reason)
	}
	for _, status := range []string{profile.RowOK, profile.RowMissing, profile.RowAbsent} {
		keys = append(keys, "verify.status."+status)
	}
	for _, key := range keys {
		if _, ok := en[key]; !ok {
			t.Errorf("no message for %s", key)
		}
	}
	if !slices.Contains(keys, "plan.warning.project_missing") {
		t.Errorf("keys = %v", keys)
	}
}

func TestFormatBytes(t *testing.T) {
	for size, want := range map[int64]string{5: "5 B", 2500: "2.5 KB", 3_400_000: "3.4 MB", 77_500_000_000: "77.50 GB"} {
		if got := formatBytes(size); got != want {
			t.Errorf("%d: %s", size, got)
		}
	}
}
