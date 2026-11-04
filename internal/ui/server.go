package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/locales"
)

// Defaults of how long the server waits for the page.
const (
	// DefaultGrace is how long the server keeps running after the page was
	// closed. A reload of the page closes and reopens its connection in less.
	DefaultGrace = 8 * time.Second
	// DefaultFirstVisit is how long the server waits for the first visit: a
	// browser that did not open leaves no server behind.
	DefaultFirstVisit = 3 * time.Minute

	sessionCookie = "bc_session"
	// requestHeader marks the calls of the page itself. A form on another site
	// cannot add a header like this without a permission check by the browser.
	requestHeader = "X-BackUpClaude"
)

// Options configure the server.
type Options struct {
	Backend Backend
	// Language is the language of the page until the person picks another.
	Language string
	Version  string
	// Grace and FirstVisit replace the defaults when set.
	Grace, FirstVisit time.Duration
}

// Server is the local page.
type Server struct {
	opts     Options
	listener net.Listener
	httpSrv  *http.Server
	host     string

	token   string
	session string

	mu        sync.Mutex
	tokenUsed bool
	lang      string
	streams   int
	seen      bool
	zeroSince time.Time
	job       *jobState
	cancelJob context.CancelFunc
	plans     map[string]planEntry
	readSlots chan struct{}

	started  time.Time
	quitOnce sync.Once
	quit     chan struct{}
	// ctx is the lifetime of the server; jobs run under it.
	ctx context.Context
}

// New starts listening on a random port of 127.0.0.1 and makes the one-time
// token. Nothing is served until Serve runs.
func New(opts Options) (*Server, error) {
	if opts.Grace <= 0 {
		opts.Grace = DefaultGrace
	}
	if opts.FirstVisit <= 0 {
		opts.FirstVisit = DefaultFirstVisit
	}
	lang := i18n.Normalize(opts.Language)
	if lang == "" {
		lang = i18n.DefaultLang
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, i18n.E("ui.listen_failed").Hint("ui.listen_failed.hint").Because(err)
	}
	token, err := randomString(32)
	if err != nil {
		_ = listener.Close()
		return nil, i18n.E("ui.listen_failed").Because(err)
	}
	session, err := randomString(32)
	if err != nil {
		_ = listener.Close()
		return nil, i18n.E("ui.listen_failed").Because(err)
	}
	s := &Server{
		opts: opts, listener: listener, host: listener.Addr().String(), token: token, session: session,
		lang: lang, plans: map[string]planEntry{}, readSlots: make(chan struct{}, 3),
		quit: make(chan struct{}), started: time.Now(),
	}
	s.httpSrv = &http.Server{Handler: s.handler(), ReadHeaderTimeout: 10 * time.Second}
	return s, nil
}

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// URL is the address to open, with the one-time token.
func (s *Server) URL() string {
	return "http://" + s.host + "/?t=" + s.token
}

// Addr is the host and port the server listens on.
func (s *Server) Addr() string { return s.host }

// Quit asks the server to end.
func (s *Server) Quit() { s.quitOnce.Do(func() { close(s.quit) }) }

// Serve runs until the page is closed, Quit is called or ctx ends. A running
// job is waited for when the page closes; it is cancelled when ctx ends.
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.ctx = ctx

	failed := make(chan error, 1)
	go func() {
		if err := s.httpSrv.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()
	go s.watch()

	select {
	case err := <-failed:
		return i18n.E("ui.listen_failed").Because(err)
	case <-s.quit:
	case <-ctx.Done():
		s.Quit()
	}
	cancel()
	s.waitJob()
	shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := s.httpSrv.Shutdown(shutdown); err != nil {
		_ = s.httpSrv.Close()
	}
	return nil
}

// watch ends the server when the page goes away. The page keeps one event
// stream open; closing the tab closes it. A background tab still holds its
// stream, which a timer in the page would not survive.
func (s *Server) watch() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		gone := false
		switch {
		case s.streams > 0:
			s.zeroSince = time.Time{}
		case !s.seen:
			gone = time.Since(s.started) > s.opts.FirstVisit
		default:
			if s.zeroSince.IsZero() {
				s.zeroSince = time.Now()
			}
			gone = time.Since(s.zeroSince) > s.opts.Grace
		}
		busy := s.job != nil && s.job.State == stateRunning
		s.mu.Unlock()
		// A running backup is let finish: cutting it off would only waste it.
		if gone && !busy {
			s.Quit()
			return
		}
	}
}

func (s *Server) language() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lang
}

func (s *Server) translator() *i18n.Translator { return i18n.New(s.language()) }

// handler builds the routes and puts the checks in front of all of them.
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/meta", s.handleMeta)
	mux.HandleFunc("GET /api/strings", s.handleStrings)
	mux.HandleFunc("POST /api/lang", s.handleLang)
	mux.HandleFunc("GET /api/status", s.handleRead(func() []string { return []string{"status"} }))
	mux.HandleFunc("GET /api/snapshots", s.handleRead(func() []string { return []string{"list", "--snapshots"} }))
	mux.HandleFunc("GET /api/sessions", s.handleRead(func() []string { return []string{"list", "--sessions"} }))
	mux.HandleFunc("GET /api/projects", s.handleRead(func() []string { return []string{"list", "--projects"} }))
	mux.HandleFunc("GET /api/schedule", s.handleRead(func() []string { return []string{"schedule", "status"} }))
	mux.HandleFunc("GET /api/password", s.handleRead(func() []string { return []string{"password"} }))
	mux.HandleFunc("GET /api/settings", s.handleSettings)
	mux.HandleFunc("PUT /api/settings", s.handleSave)
	mux.HandleFunc("GET /api/job", s.handleJob)
	mux.HandleFunc("POST /api/backup", s.handleBackup)
	mux.HandleFunc("POST /api/restore", s.handleRestore)
	mux.HandleFunc("POST /api/job/cancel", s.handleCancel)
	mux.HandleFunc("POST /api/quit", s.handleQuit)
	mux.Handle("GET /", http.HandlerFunc(s.handlePage))
	return s.guard(mux)
}

// guard is the front door. It applies to every request, whatever the route:
//   - the Host header must be exactly the address we listen on, which defeats
//     DNS rebinding (a hostile name that resolves to 127.0.0.1);
//   - Origin, when the browser sends it, must be our own page, and every
//     change must carry it;
//   - everything but the entry with the token needs the session cookie that
//     only the entry hands out.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Cache-Control", "no-store")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
		header.Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")

		if r.Host != s.host {
			s.deny(w, http.StatusForbidden, "ui.denied.host")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+s.host {
			s.deny(w, http.StatusForbidden, "ui.denied.origin")
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			s.deny(w, http.StatusForbidden, "ui.denied.origin")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("Origin") == "" || r.Header.Get(requestHeader) != "1" {
				s.deny(w, http.StatusForbidden, "ui.denied.origin")
				return
			}
		}
		if r.Method == http.MethodGet && r.URL.Path == "/" && r.URL.Query().Has("t") {
			s.enter(w, r)
			return
		}
		if !s.signedIn(r) {
			s.deny(w, http.StatusForbidden, "ui.denied.session")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) signedIn(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.session)) == 1
}

// enter takes the one-time token from the link, hands out the session cookie
// and sends the browser on to the page without the token in its address. The
// token works once: a second use, by anyone, is refused.
func (s *Server) enter(w http.ResponseWriter, r *http.Request) {
	given := r.URL.Query().Get("t")
	s.mu.Lock()
	ok := !s.tokenUsed && subtle.ConstantTimeCompare([]byte(given), []byte(s.token)) == 1
	if ok {
		s.tokenUsed = true
	}
	s.mu.Unlock()
	if !ok {
		s.deny(w, http.StatusForbidden, "ui.denied.token")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: s.session, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// deny answers a refused request in plain words.
func (s *Server) deny(w http.ResponseWriter, status int, key string) {
	tr := s.translator()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintln(w, tr.T(key))
	fmt.Fprintln(w, tr.T(key+".hint"))
}

// handlePage serves the embedded page.
func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Path
	if name == "/" {
		name = "/index.html"
	}
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		http.Error(w, "page missing", http.StatusInternalServerError)
		return
	}
	file := name[1:]
	if _, err := fs.Stat(sub, file); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFileFS(w, r, sub, file)
}

// handleEvents is the heartbeat: the page keeps it open while it is open.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 1500\n\nevent: hello\ndata: {}\n\n")
	flusher.Flush()

	s.mu.Lock()
	s.streams++
	s.seen = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.streams--
		s.mu.Unlock()
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.quit:
			fmt.Fprint(w, "event: bye\ndata: {}\n\n")
			flusher.Flush()
			return
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	s.writeOK(w, "meta", map[string]any{
		"version": s.opts.Version, "language": s.language(), "languages": locales.Languages(),
	})
}

// stringPrefixes are the parts of the catalogs the page may read: its own
// texts and the words the commands' answers refer to by code.
var stringPrefixes = []string{
	"ui.", "status.", "backup.status.", "restore.apply.skipped.", "restore.resume", "size.", "age.", "error.unexpected",
}

func (s *Server) handleStrings(w http.ResponseWriter, _ *http.Request) {
	lang := s.language()
	catalog := i18n.New(lang)
	messages := map[string]string{}
	for key := range locales.Catalog(i18n.DefaultLang) {
		if hasAnyPrefix(key, stringPrefixes) {
			messages[key] = catalog.T(key)
		}
	}
	s.writeOK(w, "strings", map[string]any{"language": lang, "strings": messages})
}

func hasAnyPrefix(key string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func (s *Server) handleLang(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Language string `json:"language"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	lang := i18n.Normalize(body.Language)
	if lang == "" {
		s.writeError(w, http.StatusBadRequest, i18n.E("ui.bad_request").Hint("ui.bad_request.hint"))
		return
	}
	s.mu.Lock()
	s.lang = lang
	s.mu.Unlock()
	s.writeOK(w, "lang", map[string]string{"language": lang})
}

func (s *Server) handleQuit(w http.ResponseWriter, _ *http.Request) {
	s.writeOK(w, "quit", map[string]bool{"bye": true})
	s.Quit()
}

// langArgs is appended to every command so its texts come in the language of
// the page.
func (s *Server) langArgs() []string { return []string{"--lang", s.language()} }

// handleRead runs a read-only command and passes its answer on. Three at a time
// at most: each can start a restic.
func (s *Server) handleRead(build func() []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.readSlots <- struct{}{}:
			defer func() { <-s.readSlots }()
		case <-r.Context().Done():
			return
		}
		reply := s.opts.Backend.Run(r.Context(), append(build(), s.langArgs()...))
		s.writeReply(w, reply)
	}
}

func (s *Server) writeReply(w http.ResponseWriter, reply Reply) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if len(reply.Body) == 0 {
		reply.Body = marshal(envelope{OK: false, Command: "ui", Error: &i18n.Description{
			Code: "error.unexpected", Message: s.translator().T("error.unexpected"),
		}})
	}
	_, _ = w.Write(reply.Body)
}

func (s *Server) writeOK(w http.ResponseWriter, command string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(marshal(envelope{OK: true, Command: command, Data: data}))
}

func (s *Server) writeError(w http.ResponseWriter, status int, err error) {
	description := s.translator().Describe(err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(marshal(envelope{OK: false, Command: "ui", Error: &description}))
}

func (s *Server) writeFailure(w http.ResponseWriter, command string, err error) {
	description := s.translator().Describe(err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(marshal(envelope{OK: false, Command: command, Error: &description}))
}

// maxBody limits what a request may carry: the page sends a few short fields.
const maxBody = 64 << 10

func (s *Server) readBody(w http.ResponseWriter, r *http.Request, into any) bool {
	reader := http.MaxBytesReader(w, r.Body, maxBody)
	if err := decodeStrict(reader, into); err != nil {
		s.writeError(w, http.StatusBadRequest, i18n.E("ui.bad_request").Hint("ui.bad_request.hint"))
		return false
	}
	return true
}
