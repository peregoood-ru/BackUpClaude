package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	envFakeMode = "BACKUPCLAUDE_FAKE_RESTIC"
	envFakeArgs = "BACKUPCLAUDE_FAKE_ARGS"
)

// TestMain lets the test binary play restic: the child process prints canned
// answers and records how it was called.
func TestMain(m *testing.M) {
	if mode := os.Getenv(envFakeMode); mode != "" {
		fakeRestic(mode)
		return
	}
	os.Exit(m.Run())
}

func fakeRestic(mode string) {
	record, _ := json.Marshal(map[string]any{
		"args":     os.Args[1:],
		"password": os.Getenv("RESTIC_PASSWORD"),
		"repo":     os.Getenv("RESTIC_REPOSITORY"),
		"child":    os.Getenv(EnvPasswordHelper),
		"stdin":    readStdin(mode),
	})
	_ = os.WriteFile(os.Getenv(envFakeArgs), record, 0o600)
	exitWith := func(code int, message string) {
		fmt.Fprintf(os.Stderr, `{"message_type":"exit_error","code":%d,"message":%q}`+"\n", code, message)
		os.Exit(code)
	}
	switch mode {
	case "version":
		fmt.Println(`{"message_type":"version","version":"0.19.1","go_version":"go1.26","go_os":"windows","go_arch":"amd64"}`)
	case "init":
		fmt.Println(`{"message_type":"initialized","id":"abc123","repository":"/repo"}`)
	case "backup":
		fmt.Println(`{"message_type":"status","percent_done":0.5,"total_files":2}`)
		fmt.Println("this line is not JSON")
		fmt.Println(`{"message_type":"summary","files_new":2,"files_changed":1,"files_unmodified":0,"data_added":123,"total_files_processed":3,"total_bytes_processed":456,"total_duration":1.5,"snapshot_id":"deadbeef"}`)
	case "backup-incomplete":
		fmt.Println(`{"message_type":"error","error":{"message":"file is locked"},"during":"archival","item":"C:\\busy.db"}`)
		fmt.Fprintln(os.Stderr, `{"message_type":"error","error":{"message":"also on stderr"},"during":"scan","item":"C:\\gone.db"}`)
		fmt.Println(`{"message_type":"summary","files_new":1,"snapshot_id":"deadbeef"}`)
		os.Exit(exitIncomplete)
	case "backup-no-summary":
		fmt.Println(`{"message_type":"status"}`)
	case "restore":
		fmt.Println(`{"message_type":"status","percent_done":1}`)
		fmt.Println(`{"message_type":"summary","seconds_elapsed":2,"total_files":3,"files_restored":3,"total_bytes":456,"bytes_restored":456}`)
	case "restore-failed":
		fmt.Fprintln(os.Stderr, `{"message_type":"error","error":{"message":"access denied"},"during":"restore","item":"/data/file.txt"}`)
		exitWith(1, "There were 1 errors")
	case "snapshots":
		fmt.Println(`[{"id":"deadbeef00","short_id":"deadbeef","time":"2026-11-01T10:00:00.5+03:00","hostname":"pc","paths":["D:\\x"],"tags":["selftest"],"summary":{"total_files_processed":7,"total_bytes_processed":70}}]`)
	case "ls":
		fmt.Println(`{"time":"2026-11-01T10:00:00+03:00","paths":["/data"],"id":"deadbeef00","short_id":"deadbeef","struct_type":"snapshot","message_type":"snapshot"}`)
		fmt.Println(`{"name":"data","type":"dir","path":"/data","size":0,"mtime":"2026-11-01T10:00:00+03:00","struct_type":"node","message_type":"node"}`)
		fmt.Println(`{"name":"файл.txt","type":"file","path":"/data/файл.txt","size":12,"mtime":"2026-11-01T10:00:00+03:00","struct_type":"node","message_type":"node"}`)
	case "forget":
		fmt.Println(`[{"tags":["claude"],"host":"pc","paths":null,"keep":[{"short_id":"aaaaaaaa"},{"short_id":"bbbbbbbb"}],"remove":[{"short_id":"cccccccc"}],"reasons":[]},{"tags":["selftest"],"host":"pc","paths":null,"keep":[{"short_id":"dddddddd"}],"remove":null,"reasons":[]}]`)
		fmt.Println(`{"message_type":"summary","snapshots_removed":1}`)
	case "check":
		fmt.Println(`{"message_type":"summary","num_errors":0,"broken_packs":null,"suggest_repair_index":false,"suggest_prune":false}`)
	case "check-errors":
		fmt.Println(`{"message_type":"summary","num_errors":2,"broken_packs":["abc"],"suggest_repair_index":true,"suggest_prune":false}`)
		exitWith(1, "repository contains errors")
	case "stats":
		fmt.Println(`{"total_size":1048576,"total_file_count":9,"total_blob_count":30,"snapshots_count":4}`)
	case "silent":
	case "echo-stdin":
		fmt.Println(`[]`)
	case "snapshots-empty":
		fmt.Println(`[]`)
	case "missing":
		exitWith(exitRepoMissing, "repository does not exist")
	case "locked":
		exitWith(exitLockFailed, "repository is already locked")
	case "password":
		exitWith(exitWrongPassword, "wrong password or no key found")
	case "fatal":
		exitWith(1, "disk is on fire")
	case "plain-fatal":
		fmt.Fprintln(os.Stderr, "Fatal: plain text problem")
		os.Exit(1)
	}
}

func fakeClient(t *testing.T, mode string) (*Restic, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "args.json")
	return &Restic{
		Binary:          self,
		Repo:            "/repo with spaces",
		PasswordCommand: "helper restic-password",
		CacheDir:        "/cache",
		ExtraEnv:        []string{envFakeMode + "=" + mode, envFakeArgs + "=" + record},
	}, record
}

// readStdin takes what the parent wrote to the fake's standard input, only in
// the mode that tests it: the other modes have no input and must not wait.
func readStdin(mode string) string {
	if mode != "echo-stdin" {
		return ""
	}
	data, _ := io.ReadAll(os.Stdin)
	return string(data)
}

type call struct {
	Stdin    string   `json:"stdin"`
	Args     []string `json:"args"`
	Password string   `json:"password"`
	Repo     string   `json:"repo"`
	Child    string   `json:"child"`
}

func recorded(t *testing.T, path string) call {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result call
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestVersion(t *testing.T) {
	r, _ := fakeClient(t, "version")
	info, err := r.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "0.19.1" || info.GoOS != "windows" {
		t.Errorf("got %+v", info)
	}
}

func TestInit(t *testing.T) {
	r, _ := fakeClient(t, "init")
	id, err := r.Init(context.Background())
	if err != nil || id != "abc123" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestChildGetsPasswordCommandAndCleanEnvironment(t *testing.T) {
	t.Setenv("RESTIC_PASSWORD", "must-not-leak")
	t.Setenv("RESTIC_REPOSITORY", "wrong-repo")
	r, record := fakeClient(t, "init")
	if _, err := r.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := recorded(t, record)
	if got.Password != "" || got.Repo != "" {
		t.Errorf("parent settings reached restic: %+v", got)
	}
	if got.Child != "1" {
		t.Errorf("helper marker not set: %+v", got)
	}
	want := []string{"init", "--json", "--repo", "/repo with spaces", "--password-command", "helper restic-password", "--cache-dir", "/cache"}
	if !slices.Equal(got.Args, want) {
		t.Errorf("args = %v", got.Args)
	}
}

func TestBackupParsesSummaryAndPassesOptions(t *testing.T) {
	r, record := fakeClient(t, "backup")
	result, err := r.Backup(context.Background(), BackupOptions{
		Paths: []string{"/data", "/-odd name"}, Tags: []string{"selftest"}, Excludes: []string{"node_modules"},
	})
	if err != nil {
		t.Fatal(err)
	}
	summary := result.Summary
	if summary.SnapshotID != "deadbeef" || summary.FilesNew != 2 || summary.DataAdded != 123 || summary.TotalDuration != 1.5 {
		t.Errorf("summary = %+v", summary)
	}
	args := strings.Join(recorded(t, record).Args, " ")
	if !strings.HasSuffix(args, "--tag selftest --exclude node_modules -- /data /-odd name") {
		t.Errorf("args = %s", args)
	}
}

func TestBackupPassesProfileOptions(t *testing.T) {
	r, record := fakeClient(t, "backup")
	_, err := r.Backup(context.Background(), BackupOptions{
		Paths: []string{"/data"}, Tags: []string{"claude"}, ExcludeFile: "/excludes.txt", GroupByTags: true, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(recorded(t, record).Args, " ")
	if !strings.HasSuffix(args, "--tag claude --iexclude-file /excludes.txt --group-by host,tags --dry-run -- /data") {
		t.Errorf("args = %s", args)
	}
}

func TestMetadataOnly(t *testing.T) {
	if !(ItemError{Message: "incomplete metadata for D:\\x: get EA failed"}).MetadataOnly() {
		t.Error("metadata problem not recognised")
	}
	if (ItemError{Message: "failed to save D:\\x: read: Incorrect function."}).MetadataOnly() {
		t.Error("a file that was not saved must not pass as a metadata problem")
	}
}

func TestLsReadsNodesAndSkipsTheSnapshotLine(t *testing.T) {
	r, record := fakeClient(t, "ls")
	var nodes []Node
	if err := r.Ls(context.Background(), "deadbeef", []string{"/data"}, func(node Node) { nodes = append(nodes, node) }); err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].Type != NodeDir || nodes[1].Type != NodeFile || nodes[1].Name != "файл.txt" || nodes[1].Size != 12 {
		t.Fatalf("nodes = %+v", nodes)
	}
	if args := strings.Join(recorded(t, record).Args, " "); !strings.HasSuffix(args, "--recursive deadbeef /data") {
		t.Errorf("without --recursive restic shows only direct children: %s", args)
	}
	if err := r.Ls(context.Background(), "deadbeef", nil, func(Node) {}); err != nil {
		t.Fatal(err)
	}
	if args := recorded(t, record).Args; slices.Contains(args, "--recursive") {
		t.Errorf("args = %v", args)
	}
}

func TestBackupIncompleteKeepsSummaryAndItems(t *testing.T) {
	r, _ := fakeClient(t, "backup-incomplete")
	result, err := r.Backup(context.Background(), BackupOptions{Paths: []string{"/data"}})
	if !IsKind(err, KindIncomplete) {
		t.Fatalf("err = %v", err)
	}
	if result.Summary == nil || result.Summary.SnapshotID != "deadbeef" {
		t.Errorf("summary lost: %+v", result.Summary)
	}
	if len(result.Skipped) != 2 || result.Skipped[0].Message != "file is locked" || result.Skipped[0].Item != `C:\busy.db` || result.Skipped[1].Message != "also on stderr" {
		t.Errorf("skipped = %+v", result.Skipped)
	}
}

func TestBackupWithoutSummaryIsAnError(t *testing.T) {
	r, _ := fakeClient(t, "backup-no-summary")
	if _, err := r.Backup(context.Background(), BackupOptions{Paths: []string{"/data"}}); err == nil {
		t.Fatal("a backup without a summary passed as success")
	}
}

func TestRestore(t *testing.T) {
	r, record := fakeClient(t, "restore")
	summary, err := r.Restore(context.Background(), RestoreOptions{
		Snapshot: "deadbeef", Subfolder: "/C/data", Target: "/target", Includes: []string{"/data"},
		IncludeFile: "/list.txt", Verify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.FilesRestored != 3 || summary.BytesRestored != 456 {
		t.Errorf("summary = %+v", summary)
	}
	args := strings.Join(recorded(t, record).Args, " ")
	if !strings.HasSuffix(args, "deadbeef:/C/data --target /target --overwrite never --include /data --include-file /list.txt --verify") {
		t.Errorf("args = %s", args)
	}
}

// A password the person typed goes to restic's standard input and nowhere else:
// not into the arguments and not into the environment.
func TestTypedPasswordGoesToStdin(t *testing.T) {
	r, record := fakeClient(t, "echo-stdin")
	r.Options = []string{"rclone.program=/bin/rclone"}
	r.UseTypedPassword("typed secret")
	if _, err := r.Snapshots(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	got := recorded(t, record)
	joined := strings.Join(got.Args, " ")
	if strings.Contains(joined, "typed secret") || strings.Contains(joined, "--password-command") || got.Password != "" {
		t.Errorf("the password leaked: %+v", got)
	}
	if !strings.Contains(joined, "-o rclone.program=/bin/rclone") {
		t.Errorf("args = %s", joined)
	}
	if got.Stdin != "typed secret\n" {
		t.Errorf("stdin = %q", got.Stdin)
	}
}

func TestCheckDataSubset(t *testing.T) {
	r, record := fakeClient(t, "check")
	if _, err := r.CheckData(context.Background(), "10%"); err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(recorded(t, record).Args, " "); !strings.Contains(args, "check --json") || !strings.HasSuffix(args, "--read-data-subset 10%") {
		t.Errorf("args = %s", args)
	}
}

func TestRestoreFailureNamesTheFile(t *testing.T) {
	r, _ := fakeClient(t, "restore-failed")
	_, err := r.Restore(context.Background(), RestoreOptions{Snapshot: "deadbeef", Target: "/target"})
	if !IsKind(err, KindFatal) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "/data/file.txt") || !strings.Contains(err.Error(), "access denied") {
		t.Errorf("err = %v", err)
	}
}

func TestSnapshots(t *testing.T) {
	r, record := fakeClient(t, "snapshots")
	snapshots, err := r.Snapshots(context.Background(), "selftest")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].ShortID != "deadbeef" || snapshots[0].Tags[0] != "selftest" || snapshots[0].Time.IsZero() {
		t.Errorf("snapshots = %+v", snapshots)
	}
	if snapshots[0].Summary == nil || snapshots[0].Summary.TotalFilesProcessed != 7 {
		t.Errorf("summary = %+v", snapshots[0].Summary)
	}
	if args := recorded(t, record).Args; !slices.Contains(args, "--tag") {
		t.Errorf("args = %v", args)
	}
	empty, _ := fakeClient(t, "snapshots-empty")
	snapshots, err = empty.Snapshots(context.Background(), "")
	if err != nil || len(snapshots) != 0 {
		t.Errorf("snapshots=%v err=%v", snapshots, err)
	}
}

func TestExitCodesBecomeKinds(t *testing.T) {
	cases := map[string]Kind{
		"missing":  KindRepoMissing,
		"locked":   KindLocked,
		"password": KindWrongPassword,
		"fatal":    KindFatal,
	}
	for mode, kind := range cases {
		r, _ := fakeClient(t, mode)
		err := r.Probe(context.Background())
		if !IsKind(err, kind) {
			t.Errorf("%s: got %v", mode, err)
		}
	}
}

func TestErrorMessageComesFromExitError(t *testing.T) {
	r, _ := fakeClient(t, "fatal")
	err := r.Probe(context.Background())
	if !strings.Contains(err.Error(), "disk is on fire") {
		t.Errorf("err = %v", err)
	}
	if code := errorCode(err); code != "engine.restic.fatal" {
		t.Errorf("code = %s", code)
	}
	plain, _ := fakeClient(t, "plain-fatal")
	if err := plain.Probe(context.Background()); !strings.Contains(err.Error(), "plain text problem") {
		t.Errorf("err = %v", err)
	}
}

func TestEveryKindIsLocalized(t *testing.T) {
	for _, kind := range []Kind{KindFatal, KindIncomplete, KindRepoMissing, KindLocked, KindWrongPassword, KindInterrupted} {
		description := errorCode(&ResticError{Kind: kind, Message: "m"})
		if !strings.HasPrefix(description, "engine.restic.") {
			t.Errorf("kind %d: %s", kind, description)
		}
	}
}

func TestContextCancelMeansInterrupted(t *testing.T) {
	r, _ := fakeClient(t, "version")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Probe(ctx); !IsKind(err, KindInterrupted) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingBinary(t *testing.T) {
	r := &Restic{Binary: filepath.Join(t.TempDir(), "absent")}
	if _, err := r.Version(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
}

func TestPasswordCommandQuoting(t *testing.T) {
	cases := []struct {
		executable string
		want       string
	}{
		{`C:\Apps\backupclaude.exe`, `"C:\Apps\backupclaude.exe" restic-password`},
		{`/usr/local/bin/backupclaude`, `/usr/local/bin/backupclaude restic-password`},
		{`D:\Мои проекты\backupclaude.exe`, `"D:\Мои проекты\backupclaude.exe" restic-password`},
		{`/opt/odd"name/app`, `'/opt/odd"name/app' restic-password`},
	}
	for _, tc := range cases {
		if got := PasswordCommand(tc.executable, "restic-password"); got != tc.want {
			t.Errorf("got %s, want %s", got, tc.want)
		}
	}
}

func TestParseRcloneVersion(t *testing.T) {
	version, ok := parseRcloneVersion("rclone v1.75.2\n- os/version: x\n")
	if !ok || version != "1.75.2" {
		t.Errorf("version=%q ok=%v", version, ok)
	}
	if _, ok := parseRcloneVersion("something else\n"); ok {
		t.Error("garbage accepted")
	}
}

func TestRcloneVersionFailsForMissingBinary(t *testing.T) {
	if _, err := RcloneVersion(context.Background(), filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseBackupLineAcceptsBothErrorForms(t *testing.T) {
	_, item := parseBackupLine([]byte(`{"message_type":"error","error":"plain text","during":"scan","item":"x"}`))
	if item == nil || item.Message != "plain text" {
		t.Errorf("item = %+v", item)
	}
	if summary, item := parseBackupLine([]byte(`not json`)); summary != nil || item != nil {
		t.Error("garbage produced a result")
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	buffer := &tailBuffer{limit: 4}
	if _, err := buffer.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := buffer.String(); got != "cdef" {
		t.Errorf("got %q", got)
	}
}
