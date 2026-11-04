package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/disk"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/notify"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
	"github.com/peregoood-ru/BackUpClaude/internal/syncdir"
	"github.com/peregoood-ru/BackUpClaude/internal/sysproc"
	"github.com/peregoood-ru/BackUpClaude/internal/update"
)

type result struct {
	code           int
	stdout, stderr string
}

func (r result) envelope(t *testing.T) envelopeJSON {
	t.Helper()
	var parsed envelopeJSON
	if err := json.Unmarshal([]byte(r.stdout), &parsed); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.stdout)
	}
	return parsed
}

type envelopeJSON struct {
	OK      bool            `json:"ok"`
	Command string          `json:"command"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	} `json:"error"`
}

type world struct {
	store    *secrets.Memory
	terminal bool
	stdin    string
	// home is the fake home folder. The real one is never read.
	home string
	// restic replaces the installed program when set.
	restic string
	// sched replaces the scheduler of the OS when set.
	sched schedule.Scheduler
	// lowered and contained count the calls of a scheduled run.
	lowered, contained int
	// notify points the notification channels at a local server.
	notify notify.Options
	// running is what the process list shows.
	running []sysproc.Process
	// exe is the program file; empty means a name that is not a file.
	exe string
	// path, drives, syncProbe, releases and runProgram replace the OS side of
	// the installer.
	path       pathManager
	drives     func() []disk.Drive
	syncProbe  func() syncdir.Probe
	releases   func(token string) *update.Client
	runProgram func(ctx context.Context, program string, args ...string) ([]byte, error)
}

func newWorld(t *testing.T) *world {
	t.Helper()
	t.Setenv(config.EnvHome, filepath.Join(t.TempDir(), "data"))
	t.Setenv("BACKUPCLAUDE_LANG", "en")
	// The OS side of the installer is replaced by default: a test must never
	// touch the real PATH, the real scheduler or look at the real disks.
	return &world{
		store: secrets.NewMemory(), home: t.TempDir(),
		sched:     &fakeScheduler{},
		path:      &fakePath{},
		drives:    func() []disk.Drive { return nil },
		syncProbe: func() syncdir.Probe { return syncdir.Probe{} },
	}
}

func (w *world) deps() dependencies {
	return dependencies{
		secrets: func() secrets.Store { return w.store },
		executable: func() (string, error) {
			if w.exe != "" {
				return w.exe, nil
			}
			return "backupclaude", nil
		},
		terminal:       func() bool { return w.terminal },
		stdoutTerminal: func() bool { return w.terminal },
		env: func() (discover.Env, error) {
			return discover.Env{Home: w.home, Lookup: fakehome.Lookup(w.home)}, nil
		},
		resticPath: w.resticPath(),
		scheduler:  func(string) schedule.Scheduler { return w.sched },
		lower:      func() error { w.lowered++; return nil },
		contain:    func() (*sysproc.Group, error) { w.contained++; return &sysproc.Group{}, nil },
		notify:     w.notify,
		running:    func([]string, []string) ([]sysproc.Process, error) { return w.running, nil },
		path:       w.path,
		drives:     w.drives,
		syncProbe:  w.syncProbe,
		releases:   w.releases,
		runProgram: w.runProgram,
	}
}

func (w *world) resticPath() func() string {
	if w.restic == "" {
		return nil
	}
	return func() string { return w.restic }
}

func (w *world) run(args ...string) result {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(w.stdin), &stdout, &stderr, w.deps())
	return result{code, stdout.String(), stderr.String()}
}

func TestVersionText(t *testing.T) {
	r := newWorld(t).run("--version")
	if r.code != 0 || !strings.Contains(r.stdout, "backupclaude dev") {
		t.Fatalf("%+v", r)
	}
}

func TestVersionJSON(t *testing.T) {
	r := newWorld(t).run("--version", "--json")
	parsed := r.envelope(t)
	if r.code != 0 || !parsed.OK || parsed.Command != "version" {
		t.Fatalf("%+v", r)
	}
	var info versionInfo
	if err := json.Unmarshal(parsed.Data, &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "dev" || info.Tools[engine.ToolRestic] == "" || info.Tools[engine.ToolRclone] == "" {
		t.Errorf("%+v", info)
	}
}

func TestHelpIsTranslated(t *testing.T) {
	w := newWorld(t)
	ru := w.run("--help", "--lang", "ru")
	if ru.code != 0 || !strings.Contains(ru.stdout, "Команды:") || !strings.Contains(ru.stdout, "неофициальный") {
		t.Fatalf("%+v", ru)
	}
	if strings.Contains(ru.stdout, "cli.") {
		t.Errorf("raw key in help:\n%s", ru.stdout)
	}
	en := w.run("--help", "--lang=en")
	if !strings.Contains(en.stdout, "Commands:") || !strings.Contains(en.stdout, "unofficial") {
		t.Fatalf("%+v", en)
	}
}

func TestLanguageFromConfig(t *testing.T) {
	w := newWorld(t)
	t.Setenv("BACKUPCLAUDE_LANG", "")
	saveConfig(t, func(cfg *config.Config) { cfg.Language = "ru" })
	if r := w.run("--help"); !strings.Contains(r.stdout, "Команды:") {
		t.Fatalf("%+v", r)
	}
}

func TestPublicCommandsAreListed(t *testing.T) {
	r := newWorld(t).run("--json")
	parsed := r.envelope(t)
	var data struct {
		Commands []commandSummary `json:"commands"`
	}
	if err := json.Unmarshal(parsed.Data, &data); err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, command := range data.Commands {
		found[command.Name] = command.Summary
	}
	for _, name := range []string{"setup", "status", "backup", "list", "restore", "doctor", "ui", "update", "uninstall", "password"} {
		summary, ok := found[name]
		if !ok || summary == "" || strings.HasPrefix(summary, "cli.") {
			t.Errorf("%s: %q", name, summary)
		}
	}
	for _, name := range []string{resticPasswordCommand, "tools", "repo", "selftest", "measure", "verify"} {
		if _, ok := found[name]; ok {
			t.Errorf("%s should stay out of the public list", name)
		}
	}
}

func TestStubsExplainThemselves(t *testing.T) {
	for _, name := range []string{"ui"} {
		w := newWorld(t)
		text := w.run(name)
		if text.code != 3 || !strings.Contains(text.stderr, name) || !strings.Contains(text.stderr, "What to do:") {
			t.Errorf("%s: %+v", name, text)
		}
		asJSON := w.run(name, "--json")
		parsed := asJSON.envelope(t)
		if asJSON.code != 3 || parsed.OK || parsed.Error == nil || parsed.Error.Code != "stub.not_ready" || parsed.Command != name {
			t.Errorf("%s: %+v", name, asJSON)
		}
	}
}

func TestUsageErrors(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{{"bogus"}, {"doctor", "extra"}, {"--no-such-flag"}} {
		r := w.run(args...)
		if r.code != exitUsage || r.stderr == "" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	r := w.run("bogus", "--json")
	if parsed := r.envelope(t); parsed.Error == nil || parsed.Error.Code != "cli.unknown_command" {
		t.Errorf("%+v", r)
	}
}

func TestHelpCommand(t *testing.T) {
	r := newWorld(t).run("help", "doctor")
	if r.code != 0 || !strings.Contains(r.stdout, "doctor") {
		t.Fatalf("%+v", r)
	}
}

func saveConfig(t *testing.T, change func(*config.Config)) {
	t.Helper()
	layout, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Repositories.Local = filepath.Join(layout.Root, "repo")
	if change != nil {
		change(&cfg)
	}
	if err := config.Save(layout.ConfigFile(), &cfg); err != nil {
		t.Fatal(err)
	}
}

func doctorChecks(t *testing.T, r result) (bool, map[string]check) {
	t.Helper()
	var report doctorReport
	if err := json.Unmarshal(r.envelope(t).Data, &report); err != nil {
		t.Fatal(err)
	}
	checks := map[string]check{}
	for _, item := range report.Checks {
		checks[item.ID] = item
	}
	return report.Healthy, checks
}

func TestDoctorOnFreshComputer(t *testing.T) {
	r := newWorld(t).run("doctor", "--json")
	if r.code != 0 {
		t.Fatalf("a fresh computer is not a failure: %+v", r)
	}
	_, checks := doctorChecks(t, r)
	for id, status := range map[string]string{
		"platform": statusOK, "data_dir": statusWarn, "config": statusWarn,
		engine.ToolRestic: statusWarn, engine.ToolRclone: statusWarn,
		"password": statusWarn, "repository": statusSkip,
	} {
		if checks[id].Status != status {
			t.Errorf("%s: %+v", id, checks[id])
		}
		if status == statusWarn && checks[id].Hint == "" {
			t.Errorf("%s has no hint", id)
		}
	}
}

func TestDoctorReportsBrokenConfig(t *testing.T) {
	w := newWorld(t)
	layout, err := config.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.ConfigFile(), []byte(`{"version": 1, "typo": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := w.run("doctor", "--json")
	if r.code != 1 || r.envelope(t).OK {
		t.Fatalf("%+v", r)
	}
	healthy, checks := doctorChecks(t, r)
	if healthy || checks["config"].Status != statusFail || !strings.Contains(checks["config"].Message, "typo") {
		t.Errorf("%+v", checks["config"])
	}
	text := w.run("doctor")
	if text.code != 1 || !strings.Contains(text.stdout, "FAILED") {
		t.Errorf("%+v", text)
	}
}

func TestDoctorSeesPasswordWithoutShowingIt(t *testing.T) {
	w := newWorld(t)
	if _, err := secrets.EnsureRepoPassword(w.store); err != nil {
		t.Fatal(err)
	}
	password, _ := w.store.Get(secrets.RepoPasswordKey)
	r := w.run("doctor", "--json")
	_, checks := doctorChecks(t, r)
	if checks["password"].Status != statusOK {
		t.Errorf("%+v", checks["password"])
	}
	if strings.Contains(r.stdout+r.stderr, password) {
		t.Error("doctor printed the password")
	}
}

type failingStore struct{ secrets.Memory }

func (*failingStore) Get(string) (string, error) { return "", errors.New("store is down") }

func TestDoctorReportsBrokenKeyStore(t *testing.T) {
	w := newWorld(t)
	deps := w.deps()
	deps.secrets = func() secrets.Store { return &failingStore{} }
	var stdout bytes.Buffer
	code := run(context.Background(), []string{"doctor", "--json"}, strings.NewReader(""), &stdout, io.Discard, deps)
	if code != 1 {
		t.Fatalf("code = %d, %s", code, stdout.String())
	}
	_, checks := doctorChecks(t, result{stdout: stdout.String()})
	if checks["password"].Status != statusFail {
		t.Errorf("%+v", checks["password"])
	}
}

func TestPasswordStatusNeverShowsValue(t *testing.T) {
	w := newWorld(t)
	r := w.run("password", "--json")
	if r.code != 0 || !strings.Contains(r.stdout, `"stored": false`) {
		t.Fatalf("%+v", r)
	}
	if _, err := secrets.EnsureRepoPassword(w.store); err != nil {
		t.Fatal(err)
	}
	password, _ := w.store.Get(secrets.RepoPasswordKey)
	r = w.run("password")
	if r.code != 0 || strings.Contains(r.stdout, password) {
		t.Fatalf("%+v", r)
	}
}

func TestPasswordShowRefusesWithoutTerminal(t *testing.T) {
	w := newWorld(t)
	if _, err := secrets.EnsureRepoPassword(w.store); err != nil {
		t.Fatal(err)
	}
	password, _ := w.store.Get(secrets.RepoPasswordKey)
	w.stdin = "yes\n"
	for _, args := range [][]string{{"password", "--show"}, {"password", "--show", "--json"}} {
		r := w.run(args...)
		if r.code == 0 || strings.Contains(r.stdout+r.stderr, password) {
			t.Errorf("%v: %+v", args, r)
		}
	}
	w.terminal = true
	r := w.run("password", "--show", "--json")
	if r.code == 0 || strings.Contains(r.stdout+r.stderr, password) {
		t.Errorf("--json must never carry the password: %+v", r)
	}
}

func TestPasswordShowAtTerminal(t *testing.T) {
	w := newWorld(t)
	w.terminal = true
	w.stdin = "yes\n"
	if r := w.run("password", "--show"); r.code == 0 {
		t.Fatalf("no password yet: %+v", r)
	}
	if _, err := secrets.EnsureRepoPassword(w.store); err != nil {
		t.Fatal(err)
	}
	password, _ := w.store.Get(secrets.RepoPasswordKey)

	r := w.run("password", "--show")
	if r.code != 0 || !strings.Contains(r.stdout, password) {
		t.Fatalf("%+v", r)
	}
	w.stdin = "no\n"
	r = w.run("password", "--show")
	if r.code == 0 || strings.Contains(r.stdout+r.stderr, password) {
		t.Fatalf("cancel still printed: %+v", r)
	}
}

func TestPasswordShowAcceptsRussianYes(t *testing.T) {
	if !confirmed("да\r\n", "да") || !confirmed(" YES ", "да") || confirmed("нет", "да") {
		t.Error("confirmed gives wrong answers")
	}
}

func TestResticPasswordHelperGuards(t *testing.T) {
	w := newWorld(t)
	if _, err := secrets.EnsureRepoPassword(w.store); err != nil {
		t.Fatal(err)
	}
	password, _ := w.store.Get(secrets.RepoPasswordKey)

	t.Setenv(engine.EnvPasswordHelper, "")
	if r := w.run(resticPasswordCommand); r.code == 0 || strings.Contains(r.stdout+r.stderr, password) {
		t.Errorf("stray call: %+v", r)
	}
	t.Setenv(engine.EnvPasswordHelper, "1")
	w.terminal = true
	if r := w.run(resticPasswordCommand); r.code == 0 || strings.Contains(r.stdout+r.stderr, password) {
		t.Errorf("screen output: %+v", r)
	}
	w.terminal = false
	if r := w.run(resticPasswordCommand); r.code != 0 || strings.TrimSpace(r.stdout) != password {
		t.Errorf("restic gets no password: code %d", r.code)
	}
}

func TestRepoCommandsNeedSettings(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{{"repo", "init"}, {"selftest"}, {"backup"}, {"list"}, {"measure"}, {"verify"}} {
		r := w.run(append(args, "--json")...)
		parsed := r.envelope(t)
		if r.code == 0 || parsed.Error == nil || parsed.Error.Code != "config.missing" || parsed.Error.Hint == "" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	saveConfig(t, nil)
	r := w.run("selftest", "--json")
	if parsed := r.envelope(t); parsed.Error == nil || parsed.Error.Code != "engine.tool.missing" {
		t.Errorf("%+v", r)
	}
}

func TestGroupsAnswerJSON(t *testing.T) {
	w := newWorld(t)
	for _, name := range []string{"tools", "repo"} {
		r := w.run(name, "--json")
		if r.code != 0 || !r.envelope(t).OK {
			t.Errorf("%s: %+v", name, r)
		}
		if text := w.run(name); text.code != 0 || !strings.Contains(text.stdout, "Commands:") {
			t.Errorf("%s: %+v", name, text)
		}
	}
}
