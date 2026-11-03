package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/locales"
)

// fakeBackend records the commands and answers them from a table.
type fakeBackend struct {
	mu       sync.Mutex
	calls    [][]string
	reply    func(args []string) Reply
	block    chan struct{}
	settings Settings
	patches  []Patch
}

func okReply(data any) Reply {
	body, _ := json.Marshal(map[string]any{"ok": true, "command": "x", "data": data})
	return Reply{Body: body}
}

func (f *fakeBackend) Run(ctx context.Context, args []string) Reply {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{}, args...))
	block := f.block
	reply := f.reply
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return Reply{Code: 1, Body: []byte(`{"ok":false,"command":"x","error":{"code":"x","message":"cancelled"}}`)}
		}
	}
	if reply != nil {
		return reply(args)
	}
	return okReply(map[string]any{})
}

func (f *fakeBackend) Settings(context.Context) (Settings, error) { return f.settings, nil }

func (f *fakeBackend) Save(_ context.Context, patch Patch) (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patches = append(f.patches, patch)
	return f.settings, nil
}

func (f *fakeBackend) commands() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string{}, f.calls...)
}

type harness struct {
	t      *testing.T
	server *Server
	client *http.Client
	base   string
	done   chan error
	cancel context.CancelFunc
}

func start(t *testing.T, backend Backend, opts Options) *harness {
	t.Helper()
	opts.Backend = backend
	if opts.Language == "" {
		opts.Language = "en"
	}
	server, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	jar, _ := cookiejar.New(nil)
	h := &harness{t: t, server: server, client: &http.Client{Jar: jar, Timeout: 10 * time.Second}, base: "http://" + server.Addr(), done: done, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop")
		}
	})
	return h
}

// ended reports whether the server has ended, within the time given.
func (h *harness) ended(within time.Duration) bool {
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Error(err)
		}
		h.done <- nil
		return true
	case <-time.After(within):
		return false
	}
}

// enter opens the page the way the browser does: with the link from the
// command, which hands out the cookie.
func (h *harness) enter() {
	h.t.Helper()
	response, err := h.client.Get(h.server.URL())
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		h.t.Fatalf("entering gave %d", response.StatusCode)
	}
}

func (h *harness) get(path string) (int, []byte) {
	h.t.Helper()
	response, err := h.client.Get(h.base + path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, body
}

func (h *harness) send(method, path string, body any) (int, []byte) {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	request, _ := http.NewRequest(method, h.base+path, reader)
	request.Header.Set("Origin", h.base)
	request.Header.Set(requestHeader, "1")
	request.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response.StatusCode, data
}

type answer struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parse(t *testing.T, data []byte) answer {
	t.Helper()
	var a answer
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, data)
	}
	return a
}

func TestListensOnLoopbackOnly(t *testing.T) {
	h := start(t, &fakeBackend{}, Options{})
	if !strings.HasPrefix(h.server.Addr(), "127.0.0.1:") {
		t.Fatalf("listens on %s", h.server.Addr())
	}
	if strings.HasSuffix(h.server.Addr(), ":0") {
		t.Fatal("the port was not chosen")
	}
	second, err := New(Options{Backend: &fakeBackend{}})
	if err != nil {
		t.Fatal(err)
	}
	defer second.listener.Close()
	if second.Addr() == h.server.Addr() || second.token == h.server.token {
		t.Error("two servers share a port or a token")
	}
}

func TestTokenWorksOnceAndTheRestNeedsTheCookie(t *testing.T) {
	h := start(t, &fakeBackend{}, Options{})
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Without the token or the cookie nothing is served.
	for _, path := range []string{"/", "/app.js", "/api/status", "/api/strings"} {
		response, err := plain.Get(h.base + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Errorf("%s without a session: %d", path, response.StatusCode)
		}
	}
	// A wrong token does not let in.
	response, err := plain.Get(h.base + "/?t=wrong")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("wrong token: %d", response.StatusCode)
	}

	// The right token gives the cookie and sends the browser on, without the token in the address.
	response, err = plain.Get(h.server.URL())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/" {
		t.Fatalf("entry: %d %q", response.StatusCode, response.Header.Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range response.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie: %+v", cookie)
	}
	// The same token again is refused, even though it is correct.
	response, err = plain.Get(h.server.URL())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("a used token gave %d", response.StatusCode)
	}
	// The cookie opens the page.
	request, _ := http.NewRequest("GET", h.base+"/", nil)
	request.AddCookie(cookie)
	response, err = plain.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("page with the cookie: %d", response.StatusCode)
	}
}

func TestHostOriginAndHeaderAreChecked(t *testing.T) {
	h := start(t, &fakeBackend{}, Options{})
	h.enter()
	port := strings.Split(h.server.Addr(), ":")[1]

	do := func(mutate func(*http.Request)) int {
		request, _ := http.NewRequest("POST", h.base+"/api/lang", strings.NewReader(`{"language":"ru"}`))
		request.Header.Set("Origin", h.base)
		request.Header.Set(requestHeader, "1")
		mutate(request)
		response, err := h.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		return response.StatusCode
	}
	if code := do(func(*http.Request) {}); code != http.StatusOK {
		t.Fatalf("a proper request gave %d", code)
	}
	cases := map[string]func(*http.Request){
		"another host (DNS rebinding)": func(r *http.Request) { r.Host = "evil.example:" + port },
		"localhost name":               func(r *http.Request) { r.Host = "localhost:" + port },
		"another port":                 func(r *http.Request) { r.Host = "127.0.0.1:1" },
		"foreign origin":               func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") },
		"no origin":                    func(r *http.Request) { r.Header.Del("Origin") },
		"no marker header":             func(r *http.Request) { r.Header.Del(requestHeader) },
		"cross-site fetch":             func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
	}
	for name, mutate := range cases {
		if code := do(mutate); code != http.StatusForbidden {
			t.Errorf("%s: %d", name, code)
		}
	}
	// A read from another host is refused too.
	request, _ := http.NewRequest("GET", h.base+"/api/meta", nil)
	request.Host = "evil.example"
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("GET with a foreign host: %d", response.StatusCode)
	}
}

func TestPageIsEmbeddedAndNeedsNoNetwork(t *testing.T) {
	h := start(t, &fakeBackend{}, Options{})
	h.enter()
	code, page := h.get("/")
	if code != http.StatusOK || !bytes.Contains(page, []byte(`<script src="app.js">`)) {
		t.Fatalf("page: %d %s", code, page)
	}
	response, err := h.client.Get(h.base + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	csp := response.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("the policy lacks %s: %s", want, csp)
		}
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Error("the page may be cached")
	}
	// No file of the page refers to anything outside the program.
	external := regexp.MustCompile(`(?i)(https?:)?//[a-z0-9.-]+\.[a-z]{2,}`)
	err = fs.WalkDir(webFiles, "web", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, readErr := webFiles.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := strings.ReplaceAll(string(data), "http://www.w3.org/2000/svg", "")
		if found := external.FindString(text); found != "" {
			t.Errorf("%s refers to %s", path, found)
		}
		if strings.Contains(text, "innerHTML") || strings.Contains(text, "eval(") {
			t.Errorf("%s writes markup or evaluates text", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"/app.js", "/style.css", "/favicon.svg"} {
		if code, _ := h.get(name); code != http.StatusOK {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := h.get("/secret.txt"); code != http.StatusNotFound {
		t.Errorf("an unknown file: %d", code)
	}
}

func TestReadsRunTheSameCommands(t *testing.T) {
	backend := &fakeBackend{}
	h := start(t, backend, Options{Language: "ru"})
	h.enter()
	for path, want := range map[string][]string{
		"/api/status":    {"status", "--lang", "ru"},
		"/api/snapshots": {"list", "--snapshots", "--lang", "ru"},
		"/api/sessions":  {"list", "--sessions", "--lang", "ru"},
		"/api/projects":  {"list", "--projects", "--lang", "ru"},
		"/api/schedule":  {"schedule", "status", "--lang", "ru"},
		"/api/password":  {"password", "--lang", "ru"},
	} {
		before := len(backend.commands())
		if code, body := h.get(path); code != http.StatusOK || !parse(t, body).OK {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		calls := backend.commands()
		if len(calls) != before+1 || strings.Join(calls[len(calls)-1], " ") != strings.Join(want, " ") {
			t.Errorf("%s ran %v, want %v", path, calls[before:], want)
		}
	}
}

func jobOf(t *testing.T, h *harness) *jobState {
	t.Helper()
	_, body := h.get("/api/job")
	var data struct {
		Job *jobState `json:"job"`
	}
	if err := json.Unmarshal(parse(t, body).Data, &data); err != nil {
		t.Fatal(err)
	}
	return data.Job
}

// finished waits until the job ends and returns it.
func finished(t *testing.T, h *harness) *jobState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if job := jobOf(t, h); job != nil && job.State != stateRunning {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the job did not end")
	return nil
}

func TestThePageCanRunOnlyKnownCommands(t *testing.T) {
	backend := &fakeBackend{}
	h := start(t, backend, Options{})
	h.enter()
	for _, path := range []string{"/api/status", "/api/snapshots", "/api/sessions", "/api/projects", "/api/schedule", "/api/password"} {
		h.get(path)
	}
	h.send("POST", "/api/backup", map[string]string{"profile": "all"})
	finished(t, h)
	for _, body := range []map[string]any{
		{"mode": "session", "name": "0a1b2c3d", "step": "copy"},
		{"mode": "project", "name": "demo", "step": "preview"},
		{"mode": "session", "name": "--show", "step": "copy"},
		{"mode": "session", "name": "a b", "step": "copy"},
		{"mode": "other", "name": "abcdef", "step": "copy"},
	} {
		if code, _ := h.send("POST", "/api/restore", body); code == http.StatusOK {
			finished(t, h)
		}
	}
	allowed := map[string]bool{"status": true, "list": true, "schedule": true, "password": true, "backup": true, "restore": true}
	for _, call := range backend.commands() {
		if !allowed[call[0]] {
			t.Errorf("ran %v", call)
		}
		for _, arg := range call {
			if strings.Contains(arg, "show") {
				t.Errorf("a command asked to show the password: %v", call)
			}
		}
		if call[0] == "password" && len(call) != 3 {
			t.Errorf("password with options: %v", call)
		}
	}
}

func TestRestoreNeedsAPreviewAndAConfirmation(t *testing.T) {
	backend := &fakeBackend{}
	h := start(t, backend, Options{})
	h.enter()
	request := map[string]any{"mode": "session", "name": "0a1b2c3d", "from": "local", "step": "apply"}

	// No confirmation: refused, and nothing ran.
	if code, _ := h.send("POST", "/api/restore", request); code != http.StatusForbidden {
		t.Fatalf("an unconfirmed apply: %d", code)
	}
	// Confirmed but without a preview receipt: refused.
	request["confirm"] = true
	if code, _ := h.send("POST", "/api/restore", request); code != http.StatusForbidden {
		t.Fatalf("an apply without a preview: %d", code)
	}
	request["plan"] = "invented"
	if code, _ := h.send("POST", "/api/restore", request); code != http.StatusForbidden {
		t.Fatalf("an apply with an invented receipt: %d", code)
	}
	if len(backend.commands()) != 0 {
		t.Fatalf("something ran: %v", backend.commands())
	}

	// A preview gives a receipt for exactly that request.
	preview := map[string]any{"mode": "session", "name": "0a1b2c3d", "from": "local", "step": "preview"}
	if code, _ := h.send("POST", "/api/restore", preview); code != http.StatusOK {
		t.Fatalf("preview: %d", code)
	}
	job := finished(t, h)
	if job.State != stateDone || job.Plan == "" || job.Step != stepPreview {
		t.Fatalf("preview job: %+v", job)
	}
	if got := strings.Join(backend.commands()[0], " "); got != "restore --session=0a1b2c3d --from=local --apply --dry-run --lang en" {
		t.Errorf("preview ran %q", got)
	}

	// The receipt does not fit another session.
	other := map[string]any{"mode": "session", "name": "ffff0000", "from": "local", "step": "apply", "confirm": true, "plan": job.Plan}
	if code, _ := h.send("POST", "/api/restore", other); code != http.StatusForbidden {
		t.Fatalf("a receipt for another session: %d", code)
	}
	// It does fit the previewed one, once.
	request["plan"] = job.Plan
	if code, body := h.send("POST", "/api/restore", request); code != http.StatusOK {
		t.Fatalf("a proper apply: %d %s", code, body)
	}
	if applied := finished(t, h); applied.State != stateDone || applied.Plan != "" {
		t.Errorf("apply job: %+v", applied)
	}
	if got := strings.Join(backend.commands()[1], " "); got != "restore --session=0a1b2c3d --from=local --apply --lang en" {
		t.Errorf("apply ran %q", got)
	}
	if code, _ := h.send("POST", "/api/restore", request); code != http.StatusForbidden {
		t.Errorf("a receipt works twice: %d", code)
	}
}

func TestRestoreRefusesOddInput(t *testing.T) {
	backend := &fakeBackend{}
	h := start(t, backend, Options{})
	h.enter()
	for name, body := range map[string]map[string]any{
		"option as a session": {"mode": "session", "name": "--password-command=x", "step": "copy"},
		"space in a session":  {"mode": "session", "name": "a b c", "step": "copy"},
		"path in a session":   {"mode": "session", "name": "../../x", "step": "copy"},
		"empty project":       {"mode": "project", "name": "  ", "step": "copy"},
		"unknown mode":        {"mode": "all", "name": "abcdef", "step": "copy"},
		"unknown source":      {"mode": "session", "name": "abcdef", "from": "repo", "step": "copy"},
		"odd snapshot":        {"mode": "session", "name": "abcdef", "snapshot": "latest", "step": "copy"},
		"unknown step":        {"mode": "session", "name": "abcdef", "step": "everything"},
		"unknown field":       {"mode": "session", "name": "abcdef", "step": "copy", "to": "C:\\"},
	} {
		if code, _ := h.send("POST", "/api/restore", body); code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, code)
		}
	}
	if len(backend.commands()) != 0 {
		t.Errorf("something ran: %v", backend.commands())
	}
	// A project that looks like an option travels behind --project=, so it cannot be one.
	if code, _ := h.send("POST", "/api/restore", map[string]any{"mode": "project", "name": "--all", "step": "copy"}); code != http.StatusOK {
		t.Fatal(code)
	}
	finished(t, h)
	if got := backend.commands()[0][1]; got != "--project=--all" {
		t.Errorf("project argument: %q", got)
	}
}

func TestBackupJobRunsOneAtATimeAndCanBeCancelled(t *testing.T) {
	backend := &fakeBackend{block: make(chan struct{})}
	h := start(t, backend, Options{})
	h.enter()
	if code, _ := h.send("POST", "/api/backup", map[string]string{"profile": "projects"}); code != http.StatusOK {
		t.Fatal(code)
	}
	if got := strings.Join(backend.commands()[0], " "); got != "backup --profile projects --lang en" {
		t.Errorf("ran %q", got)
	}
	if code, _ := h.send("POST", "/api/backup", map[string]string{"profile": "all"}); code != http.StatusConflict {
		t.Errorf("a second job: %d", code)
	}
	h.send("POST", "/api/job/cancel", map[string]string{})
	if job := finished(t, h); job.State != stateCancelled {
		t.Errorf("cancelled job: %+v", job)
	}

	backend.mu.Lock()
	backend.block = nil
	backend.reply = func([]string) Reply {
		return okReply(map[string]any{"profiles": []map[string]any{{"profile": "claude", "status": "ok"}}})
	}
	backend.mu.Unlock()
	h.send("POST", "/api/backup", map[string]string{})
	job := finished(t, h)
	if job.State != stateDone || job.Kind != kindBackup || !bytes.Contains(job.Result, []byte(`"profile":"claude"`)) {
		t.Errorf("finished job: %+v %s", job, job.Result)
	}
	if code, _ := h.send("POST", "/api/backup", map[string]string{"profile": "everything"}); code != http.StatusBadRequest {
		t.Errorf("a bad profile: %d", code)
	}
}

func TestFailedCommandMakesAFailedJob(t *testing.T) {
	backend := &fakeBackend{reply: func([]string) Reply {
		return Reply{Code: 1, Body: []byte(`{"ok":false,"command":"backup","error":{"code":"x","message":"disk is gone"}}`)}
	}}
	h := start(t, backend, Options{})
	h.enter()
	h.send("POST", "/api/backup", map[string]string{})
	job := finished(t, h)
	if job.State != stateFailed || !bytes.Contains(job.Result, []byte("disk is gone")) {
		t.Errorf("%+v", job)
	}
}

func TestSettingsNeverCarryThePassword(t *testing.T) {
	backend := &fakeBackend{settings: Settings{Local: "D:\\copies", PasswordStored: true, Extra: []string{}, Exclude: []string{}, Channels: []string{}}}
	h := start(t, backend, Options{})
	h.enter()
	code, body := h.get("/api/settings")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(parse(t, body).Data, &keys); err != nil {
		t.Fatal(err)
	}
	for key := range keys {
		if strings.Contains(key, "password") && key != "password_stored" {
			t.Errorf("settings carry %s", key)
		}
	}
	if string(keys["password_stored"]) != "true" {
		t.Errorf("password_stored: %s", keys["password_stored"])
	}
	// Only the known fields are accepted, and a missing one stays unchanged.
	if code, _ := h.send("PUT", "/api/settings", map[string]any{"password": "x"}); code != http.StatusBadRequest {
		t.Errorf("an unknown setting: %d", code)
	}
	if code, _ := h.send("PUT", "/api/settings", map[string]any{"deletion_drop_percent": 30, "tray": true}); code != http.StatusOK {
		t.Fatal(code)
	}
	patch := backend.patches[0]
	if patch.DropPercent == nil || *patch.DropPercent != 30 || patch.Tray == nil || !*patch.Tray || patch.Retention != nil || patch.Language != nil {
		t.Errorf("patch: %+v", patch)
	}
}

func TestStringsComeFromTheCatalogsInTheChosenLanguage(t *testing.T) {
	h := start(t, &fakeBackend{}, Options{Language: "en"})
	h.enter()
	read := func() map[string]string {
		_, body := h.get("/api/strings")
		var data struct {
			Strings map[string]string `json:"strings"`
		}
		if err := json.Unmarshal(parse(t, body).Data, &data); err != nil {
			t.Fatal(err)
		}
		return data.Strings
	}
	if read()["ui.hero.ok"] != locales.Catalog("en")["ui.hero.ok"] {
		t.Error("not English")
	}
	if code, _ := h.send("POST", "/api/lang", map[string]string{"language": "ru"}); code != http.StatusOK {
		t.Fatal(code)
	}
	strs := read()
	if strs["ui.hero.ok"] != locales.Catalog("ru")["ui.hero.ok"] || strs["status.verdict.ok"] == "" {
		t.Error("not Russian")
	}
	if code, _ := h.send("POST", "/api/lang", map[string]string{"language": "xx"}); code != http.StatusBadRequest {
		t.Errorf("an unknown language: %d", code)
	}
	for key := range strs {
		if strings.HasPrefix(key, "password.") || strings.HasPrefix(key, "notify.") {
			t.Errorf("the page may read %s", key)
		}
	}
}

// Every text the page asks for must exist in both catalogs: a typo would show
// a raw key to the person.
func TestEveryTextOfThePageExists(t *testing.T) {
	data, err := webFiles.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	literal := regexp.MustCompile(`'((?:ui|status|age|size|backup\.status|restore\.apply\.skipped|restore\.resume|error)\.[a-z0-9_.-]*)'`)
	en, ru := locales.Catalog("en"), locales.Catalog("ru")
	seen := 0
	for _, match := range literal.FindAllStringSubmatch(string(data), -1) {
		key := match[1]
		seen++
		if strings.HasSuffix(key, ".") {
			// A family chosen at run time: at least one member must exist.
			found := false
			for known := range en {
				found = found || strings.HasPrefix(known, key)
			}
			if !found {
				t.Errorf("no key starts with %s", key)
			}
			continue
		}
		if _, ok := en[key]; !ok {
			t.Errorf("en.json lacks %s", key)
		}
		if _, ok := ru[key]; !ok {
			t.Errorf("ru.json lacks %s", key)
		}
	}
	if seen < 100 {
		t.Errorf("the scan found only %d keys, the pattern is probably broken", seen)
	}
	// The families the page builds by name: every member the commands can answer.
	issues := []string{"repository_unreachable", "offsite_unreachable", "no_snapshots", "snapshot_stale", "check_errors",
		"check_failed", "check_overdue", "job_failing", "disk_low", "alert", "restore_test_failed", "restore_test_overdue"}
	families := map[string][]string{
		"ui.tab.":              {"status", "snapshots", "restore", "settings"},
		"ui.lang.":             {"en", "ru"},
		"ui.hero.":             {"ok", "attention", "problem", "attention_sub", "problem_sub"},
		"ui.snapshots.filter.": {"all", "claude", "projects"},
		"ui.restore.mode.":     {"session", "project"},
		"ui.kind.":             {"code", "desktop", "cowork"},
		"ui.session.":          {"both", "snapshot_only", "disk_only", "damaged"},
		"ui.job.":              {"claude", "projects", "cleanup", "check", "summary", "restore-test"},
		"ui.todo.":             issues,
		"status.issue.":        issues,
		"backup.status.":       {"ok", "locked_files", "incomplete", "failed"},
	}
	for prefix, members := range families {
		for _, member := range members {
			if _, ok := en[prefix+member]; !ok {
				t.Errorf("en.json lacks %s%s", prefix, member)
			}
			if _, ok := ru[prefix+member]; !ok {
				t.Errorf("ru.json lacks %s%s", prefix, member)
			}
		}
	}
}

func TestServerEndsWhenThePageIsClosed(t *testing.T) {
	h := start(t, &fakeBackend{}, Options{Grace: 300 * time.Millisecond})
	h.enter()
	// The page keeps one event stream open.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", h.base+"/api/events", nil)
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := response.Body.Read(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	if h.ended(900 * time.Millisecond) {
		t.Fatal("the server ended while the page was open")
	}
	// Closing the tab closes the stream.
	cancel()
	_ = response.Body.Close()
	if !h.ended(5 * time.Second) {
		t.Fatal("the server did not end after the page was closed")
	}
}

func TestServerEndsWhenNobodyComes(t *testing.T) {
	h := start(t, &fakeBackend{}, Options{FirstVisit: 300 * time.Millisecond})
	if !h.ended(5 * time.Second) {
		t.Fatal("the server waits for ever for a browser that never came")
	}
}

func TestARunningJobIsNotCutOffWhenThePageCloses(t *testing.T) {
	backend := &fakeBackend{block: make(chan struct{})}
	h := start(t, backend, Options{Grace: 200 * time.Millisecond})
	h.enter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", h.base+"/api/events", nil)
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := response.Body.Read(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	h.send("POST", "/api/backup", map[string]string{})
	cancel()
	_ = response.Body.Close()
	if h.ended(1200 * time.Millisecond) {
		t.Fatal("the server ended in the middle of a backup")
	}
	close(backend.block)
	if !h.ended(5 * time.Second) {
		t.Fatal("the server did not end after the backup finished")
	}
}

func TestQuitEndsTheServer(t *testing.T) {
	h := start(t, &fakeBackend{}, Options{})
	h.enter()
	if code, _ := h.send("POST", "/api/quit", map[string]string{}); code != http.StatusOK {
		t.Fatal(code)
	}
	if !h.ended(5 * time.Second) {
		t.Fatal("quit did not end the server")
	}
}
