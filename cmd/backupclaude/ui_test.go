package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
	"github.com/peregoood-ru/BackUpClaude/internal/tray"
)

// fakeAutostart stands for the Run key of the registry: a test must never
// touch the real one.
type fakeAutostart struct {
	mu      sync.Mutex
	on      bool
	exe     string
	enables int
	failOn  error
}

func (f *fakeAutostart) Supported() bool { return true }

func (f *fakeAutostart) Enable(executable string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn != nil {
		return f.failOn
	}
	f.on, f.exe = true, executable
	f.enables++
	return nil
}

func (f *fakeAutostart) Disable() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.on = false
	return nil
}

func (f *fakeAutostart) Enabled() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on, nil
}

// page is a running `backupclaude ui` and a browser that has been let in.
type page struct {
	t      *testing.T
	client *http.Client
	base   string
	cancel context.CancelFunc
	done   chan int
	url    string
}

// startPage runs the ui command the way a person does, and reads from its JSON
// answer where the page is.
func startPage(t *testing.T, w *world, extra ...string) *page {
	t.Helper()
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	args := append([]string{"ui", "--json"}, extra...)
	go func() {
		code := run(ctx, args, strings.NewReader(""), writer, io.Discard, w.deps())
		_ = writer.Close()
		done <- code
	}()
	var envelope envelopeJSON
	if err := json.NewDecoder(reader).Decode(&envelope); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	var started uiStarted
	if err := json.Unmarshal(envelope.Data, &started); err != nil || !envelope.OK {
		cancel()
		t.Fatalf("%v %+v", err, envelope)
	}
	jar, _ := cookiejar.New(nil)
	p := &page{t: t, client: &http.Client{Jar: jar, Timeout: 2 * time.Minute}, base: "http://" + started.Port, cancel: cancel, done: done, url: started.URL}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the ui command did not end")
		}
	})
	return p
}

func (p *page) enter() {
	p.t.Helper()
	response, err := p.client.Get(p.url)
	if err != nil {
		p.t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("entering: %d", response.StatusCode)
	}
}

func (p *page) call(method, path string, body any) (int, envelopeJSON, []byte) {
	p.t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	request, _ := http.NewRequest(method, p.base+path, reader)
	request.Header.Set("Origin", p.base)
	request.Header.Set("X-BackUpClaude", "1")
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		p.t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	var parsed envelopeJSON
	_ = json.Unmarshal(data, &parsed)
	return response.StatusCode, parsed, data
}

// job waits for the long operation to end and returns its answer.
func (p *page) job() (state string, plan string, result envelopeJSON) {
	p.t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		_, parsed, _ := p.call("GET", "/api/job", nil)
		var data struct {
			Job *struct {
				State  string       `json:"state"`
				Plan   string       `json:"plan"`
				Result envelopeJSON `json:"result"`
			} `json:"job"`
		}
		if err := json.Unmarshal(parsed.Data, &data); err != nil {
			p.t.Fatal(err)
		}
		if data.Job != nil && data.Job.State != "running" {
			return data.Job.State, data.Job.Plan, data.Job.Result
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.t.Fatal("the job did not end")
	return "", "", envelopeJSON{}
}

func TestUIOpensTheBrowserWithTheOneTimeLink(t *testing.T) {
	w := newWorld(t)
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		code := run(ctx, []string{"ui"}, strings.NewReader(""), writer, io.Discard, w.deps())
		_ = writer.Close()
		done <- code
	}()
	var out bytes.Buffer
	go func() { _, _ = io.Copy(&out, reader) }()
	var address string
	select {
	case address = <-w.opened:
	case <-time.After(10 * time.Second):
		t.Fatal("the browser was not asked to open the page")
	}
	if !strings.HasPrefix(address, "http://127.0.0.1:") || !strings.Contains(address, "/?t=") {
		t.Errorf("link: %s", address)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not end")
	}
	if !strings.Contains(out.String(), address) || !strings.Contains(out.String(), "closed") {
		t.Errorf("output: %q", out.String())
	}
}

func TestUINoOpenOnlyPrintsTheLink(t *testing.T) {
	w := newWorld(t)
	p := startPage(t, w, "--no-open")
	select {
	case address := <-w.opened:
		t.Fatalf("the browser was opened: %s", address)
	case <-time.After(200 * time.Millisecond):
	}
	p.enter()
}

func TestUIRunsTheSameCommandsAsTheCommandLine(t *testing.T) {
	w := newBackupWorld(t, "")
	w.writeSnapshots(t, map[string]time.Duration{"claude": 12 * time.Minute, "projects": 5 * time.Hour})
	p := startPage(t, w.world)
	p.enter()

	// The status of the page is the status of the command.
	_, onPage, _ := p.call("GET", "/api/status", nil)
	if !onPage.OK || onPage.Command != "status" {
		t.Fatalf("%+v", onPage)
	}
	cli := w.run("status", "--json").envelope(t)
	var a, b statusReport
	if err := json.Unmarshal(onPage.Data, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(cli.Data, &b); err != nil {
		t.Fatal(err)
	}
	if a.Verdict != b.Verdict || len(a.Copies) != len(b.Copies) || a.Copies[0].Profiles[0].Profile != b.Copies[0].Profiles[0].Profile {
		t.Errorf("page: %+v\ncommand: %+v", a, b)
	}
	if a.Verdict != verdictOK {
		t.Errorf("verdict %s, issues %v", a.Verdict, a.Issues)
	}
	_, snapshots, _ := p.call("GET", "/api/snapshots", nil)
	if !snapshots.OK || !strings.Contains(string(snapshots.Data), `"snapshots"`) {
		t.Errorf("snapshots: %+v", snapshots)
	}
}

func TestUIBringsASessionBackOnlyThroughThePage(t *testing.T) {
	w := newRestoreWorld(t, "")
	want, err := os.ReadFile(w.transcript)
	if err != nil {
		t.Fatal(err)
	}
	fakehome.Remove(t, w.transcript)
	p := startPage(t, w.world)
	p.enter()

	// The session is listed as living only in the copy.
	_, sessions, _ := p.call("GET", "/api/sessions", nil)
	var listed struct {
		Sessions []sessionRow `json:"sessions"`
	}
	if err := json.Unmarshal(sessions.Data, &listed); err != nil || !sessions.OK {
		t.Fatalf("%v %+v", err, sessions)
	}
	found := false
	for _, row := range listed.Sessions {
		if row.ID == w.id {
			found = true
			if row.OnDisk || !row.InSnapshot {
				t.Errorf("row: %+v", row)
			}
		}
	}
	if !found {
		t.Fatalf("session not listed: %+v", listed.Sessions)
	}

	// The safe way: into a separate folder. Nothing is put back.
	request := map[string]any{"mode": "session", "name": w.id, "step": "copy"}
	if code, _, body := p.call("POST", "/api/restore", request); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	state, _, result := p.job()
	if state != "done" || !result.OK {
		t.Fatalf("copy: %s %+v", state, result)
	}
	var copied restoreAnswer
	if err := json.Unmarshal(result.Data, &copied); err != nil || copied.Fetched == nil || copied.Fetched.Files != 1 || copied.Apply != nil {
		t.Fatalf("%v %+v", err, copied)
	}
	if _, err := os.Stat(w.transcript); err == nil {
		t.Fatal("the session came back without confirmation")
	}
	staged := restore.StagedPath(copied.Fetched.Target, restore.SnapshotPath(w.transcript))
	if _, err := os.Stat(staged); err != nil {
		t.Errorf("not in the separate folder: %v", err)
	}

	// Putting back without confirmation or without a preview is refused.
	request["step"] = "apply"
	if code, _, _ := p.call("POST", "/api/restore", request); code != http.StatusForbidden {
		t.Fatalf("an unconfirmed apply: %d", code)
	}
	request["confirm"] = true
	if code, _, _ := p.call("POST", "/api/restore", request); code != http.StatusForbidden {
		t.Fatalf("an apply without a preview: %d", code)
	}
	if _, err := os.Stat(w.transcript); err == nil {
		t.Fatal("the session came back without a preview")
	}

	// The preview shows what would happen and writes nothing.
	request["step"], request["confirm"] = "preview", false
	p.call("POST", "/api/restore", request)
	state, plan, result := p.job()
	if state != "done" || plan == "" {
		t.Fatalf("preview: %s %q %+v", state, plan, result)
	}
	var preview restoreAnswer
	if err := json.Unmarshal(result.Data, &preview); err != nil || preview.Apply == nil || !preview.Apply.DryRun || preview.Apply.Planned != 1 {
		t.Fatalf("%v %+v", err, preview.Apply)
	}
	if _, err := os.Stat(w.transcript); err == nil {
		t.Fatal("the preview wrote the session")
	}

	// Confirmed with the receipt of the preview: the session is back, byte for byte.
	request["step"], request["confirm"], request["plan"] = "apply", true, plan
	if code, _, body := p.call("POST", "/api/restore", request); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	state, _, result = p.job()
	if state != "done" || !result.OK {
		t.Fatalf("apply: %s %+v", state, result)
	}
	got, err := os.ReadFile(w.transcript)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the session did not come back: %v", err)
	}
	var applied restoreAnswer
	if err := json.Unmarshal(result.Data, &applied); err != nil || applied.Apply == nil || applied.Apply.Copied != 1 || len(applied.Resume) != 1 {
		t.Fatalf("%v %+v", err, applied)
	}
	// The receipt works once.
	if code, _, _ := p.call("POST", "/api/restore", request); code != http.StatusForbidden {
		t.Errorf("a receipt used twice: %d", code)
	}
}

func TestUISettingsAreSavedByTheSameCodeAndNeverShowThePassword(t *testing.T) {
	w := newWorld(t)
	saveConfig(t, nil)
	const secret = "correct-horse-battery-staple-4321"
	if err := w.store.Set(secrets.RepoPasswordKey, secret); err != nil {
		t.Fatal(err)
	}
	p := startPage(t, w)
	p.enter()

	code, settings, body := p.call("GET", "/api/settings", nil)
	if code != http.StatusOK || !settings.OK {
		t.Fatalf("%d %s", code, body)
	}
	if bytes.Contains(body, []byte(secret)) {
		t.Fatal("the settings carry the password")
	}
	if !bytes.Contains(body, []byte(`"password_stored":true`)) || !bytes.Contains(body, []byte(`"tray":false`)) {
		t.Errorf("settings: %s", body)
	}
	_, password, body := p.call("GET", "/api/password", nil)
	if !password.OK || bytes.Contains(body, []byte(secret)) || !bytes.Contains(body, []byte(`"stored": true`)) && !bytes.Contains(body, []byte(`"stored":true`)) {
		t.Errorf("password: %s", body)
	}

	// A change goes through config.Save: pasted quotes are removed.
	change := map[string]any{
		"language": "ru", "deletion_drop_percent": 35,
		"retention": map[string]int{"hourly": 24, "daily": 14, "weekly": 8, "monthly": 6},
		"extra":     []string{` "~/Заметки" `, ""}, "exclude": []string{"D:\\Big"},
	}
	if code, saved, body := p.call("PUT", "/api/settings", change); code != http.StatusOK || !saved.OK {
		t.Fatalf("%d %s", code, body)
	}
	layout, _ := config.Resolve()
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != "ru" || cfg.Notify.DeletionDropPercent != 35 || cfg.Retention.Hourly != 24 || cfg.Retention.Monthly != 6 ||
		len(cfg.Sources.Extra) != 1 || cfg.Sources.Extra[0] != "~/Заметки" || len(cfg.Sources.Exclude) != 1 {
		t.Errorf("saved: %+v", cfg)
	}
	if cfg.Tray.Enabled || w.autostart.on {
		t.Error("the icon was switched on without being asked")
	}

	// A wrong value changes nothing and is explained.
	_, refused, _ := p.call("PUT", "/api/settings", map[string]any{"retention": map[string]int{"hourly": 0, "daily": 0, "weekly": 0, "monthly": 0}})
	if refused.OK || refused.Error == nil || refused.Error.Code != "config.invalid" {
		t.Fatalf("%+v", refused)
	}
	after, _ := config.Load(layout.ConfigFile())
	if after.Retention.Hourly != 24 {
		t.Errorf("a wrong value was saved: %+v", after.Retention)
	}
}

func TestUITrayIsOffByDefaultAndSwitchedInTheSettings(t *testing.T) {
	w := newWorld(t)
	w.exe = filepath.Join(t.TempDir(), "backupclaude.exe")
	saveConfig(t, nil)
	p := startPage(t, w)
	p.enter()

	_, settings, _ := p.call("GET", "/api/settings", nil)
	if !strings.Contains(string(settings.Data), `"tray":false`) || !strings.Contains(string(settings.Data), `"tray_supported":true`) {
		t.Fatalf("%s", settings.Data)
	}
	if code, saved, body := p.call("PUT", "/api/settings", map[string]any{"tray": true}); code != http.StatusOK || !saved.OK {
		t.Fatalf("%d %s", code, body)
	}
	if !w.autostart.on || w.autostart.exe != w.exe {
		t.Errorf("autostart: %+v", w.autostart)
	}
	select {
	case started := <-w.spawned:
		if len(started) != 2 || started[0] != w.exe || started[1] != "tray" {
			t.Errorf("started %v", started)
		}
	case <-time.After(5 * time.Second):
		t.Error("the icon was not started")
	}
	layout, _ := config.Resolve()
	if cfg, _ := config.Load(layout.ConfigFile()); !cfg.Tray.Enabled {
		t.Error("the setting was not saved")
	}

	// Switching off removes the start at logon.
	p.call("PUT", "/api/settings", map[string]any{"tray": false})
	if w.autostart.on {
		t.Error("autostart stayed")
	}
	if cfg, _ := config.Load(layout.ConfigFile()); cfg.Tray.Enabled {
		t.Error("the setting stayed on")
	}

	// When the registration fails the setting does not claim the icon is on.
	w.autostart.failOn = errors.New("registry is locked")
	_, failed, _ := p.call("PUT", "/api/settings", map[string]any{"tray": true})
	if failed.OK {
		t.Fatal("a failure was reported as success")
	}
	if cfg, _ := config.Load(layout.ConfigFile()); cfg.Tray.Enabled {
		t.Error("the setting says on, but nothing is registered")
	}
}

func TestTrayCommandSwitchesTheAutostart(t *testing.T) {
	w := newWorld(t)
	w.exe = filepath.Join(t.TempDir(), "backupclaude.exe")
	saveConfig(t, nil)

	r := w.run("tray", "--enable", "--json")
	if r.code != 0 || !w.autostart.on || w.autostart.exe != w.exe {
		t.Fatalf("%+v %+v", r, w.autostart)
	}
	layout, _ := config.Resolve()
	if cfg, _ := config.Load(layout.ConfigFile()); !cfg.Tray.Enabled {
		t.Error("the setting was not saved")
	}
	text := w.run("tray", "--disable")
	if text.code != 0 || w.autostart.on || !strings.Contains(text.stdout, "no longer") {
		t.Fatalf("%+v", text)
	}
	if cfg, _ := config.Load(layout.ConfigFile()); cfg.Tray.Enabled {
		t.Error("the setting stayed on")
	}
	if r := w.run("tray", "--enable", "--disable"); r.code != exitUsage {
		t.Errorf("both options: %+v", r)
	}
}

func TestTrayColourFollowsTheVerdict(t *testing.T) {
	w := newBackupWorld(t, "")
	w.writeSnapshots(t, map[string]time.Duration{"claude": 12 * time.Minute, "projects": 5 * time.Hour})
	a := newApp(nil, strings.NewReader(""), io.Discard, io.Discard, w.deps())
	state := a.trayState(context.Background())
	if state.Level != tray.LevelOK || !strings.Contains(state.Text, "all is well") {
		t.Errorf("%+v", state)
	}
	// Old snapshots: the colour changes and the first reason is told.
	w.writeSnapshots(t, map[string]time.Duration{"claude": 30 * time.Hour, "projects": 5 * time.Hour})
	state = a.trayState(context.Background())
	if state.Level != tray.LevelAttention || !strings.Contains(state.Text, "too long") {
		t.Errorf("%+v", state)
	}
	// Settings that cannot be read: grey, not green.
	layout, _ := config.Resolve()
	if err := os.WriteFile(layout.ConfigFile(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if state := a.trayState(context.Background()); state.Level != tray.LevelUnknown {
		t.Errorf("%+v", state)
	}
}
