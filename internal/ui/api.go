package ui

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

//go:embed web
var webFiles embed.FS

// Job states.
const (
	stateRunning   = "running"
	stateDone      = "done"
	stateFailed    = "failed"
	stateCancelled = "cancelled"
)

// Kinds of job.
const (
	kindBackup  = "backup"
	kindRestore = "restore"
)

// Steps of a restore: a copy into a folder of its own, a dry run that shows
// what putting back would do, and putting back.
const (
	stepCopy    = "copy"
	stepPreview = "preview"
	stepApply   = "apply"
)

// planTTL is how long a preview stays valid for the confirmation that follows.
const planTTL = 30 * time.Minute

// jobState is what the page learns about the long operation.
type jobState struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Step    string          `json:"step,omitempty"`
	State   string          `json:"state"`
	Started time.Time       `json:"started"`
	Ended   *time.Time      `json:"ended,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	// Plan is the receipt of a successful preview; putting back needs it.
	Plan string `json:"plan,omitempty"`
	// Request echoes what was asked, so a reloaded page shows the same thing.
	Request any `json:"request,omitempty"`

	done chan struct{}
}

type planEntry struct {
	key     string
	expires time.Time
}

func decodeStrict(reader io.Reader, into any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	return decoder.Decode(into)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.opts.Backend.Settings(r.Context())
	if err != nil {
		s.writeFailure(w, "settings", err)
		return
	}
	s.writeOK(w, "settings", settings)
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	var patch Patch
	if !s.readBody(w, r, &patch) {
		return
	}
	settings, err := s.opts.Backend.Save(r.Context(), patch)
	if err != nil {
		s.writeFailure(w, "settings", err)
		return
	}
	// The page speaks the language it was saved with.
	if patch.Language != nil {
		if lang := i18n.Normalize(*patch.Language); lang != "" {
			s.mu.Lock()
			s.lang = lang
			s.mu.Unlock()
		}
	}
	s.writeOK(w, "settings", settings)
}

// backupProfiles are the values of the profile the page may ask for.
var backupProfiles = map[string]bool{"all": true, "claude": true, "projects": true}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile string `json:"profile"`
	}
	if !s.readBody(w, r, &body) {
		return
	}
	if body.Profile == "" {
		body.Profile = "all"
	}
	if !backupProfiles[body.Profile] {
		s.writeError(w, http.StatusBadRequest, i18n.E("ui.bad_request").Hint("ui.bad_request.hint"))
		return
	}
	args := []string{"backup", "--profile", body.Profile}
	s.startJob(w, kindBackup, "", args, body, "", false)
}

// restoreRequest is the page's request for bringing something back.
type restoreRequest struct {
	// Mode is "session" or "project".
	Mode string `json:"mode"`
	// Name is a session id, or a project name or path.
	Name     string `json:"name"`
	From     string `json:"from"`
	Snapshot string `json:"snapshot"`
	Step     string `json:"step"`
	// Plan and Confirm are what putting back requires: the receipt of a preview
	// of exactly this request, and the person's yes.
	Plan    string `json:"plan"`
	Confirm bool   `json:"confirm"`
}

var (
	sessionName  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,100}$`)
	snapshotName = regexp.MustCompile(`^[0-9a-f]{6,64}$`)
)

// args builds the command line, or says why the request is not acceptable.
// Every value goes behind --name=, so none can be taken for an option.
func (q restoreRequest) args() ([]string, error) {
	var args []string
	switch q.Mode {
	case "session":
		if !sessionName.MatchString(q.Name) {
			return nil, i18n.E("ui.bad_request").Hint("ui.bad_request.hint")
		}
		args = []string{"restore", "--session=" + q.Name}
	case "project":
		if strings.TrimSpace(q.Name) == "" || len(q.Name) > 500 || strings.ContainsRune(q.Name, 0) {
			return nil, i18n.E("ui.bad_request").Hint("ui.bad_request.hint")
		}
		args = []string{"restore", "--project=" + q.Name}
	default:
		return nil, i18n.E("ui.bad_request").Hint("ui.bad_request.hint")
	}
	from := q.From
	if from == "" {
		from = "local"
	}
	if from != "local" && from != "offsite" {
		return nil, i18n.E("ui.bad_request").Hint("ui.bad_request.hint")
	}
	args = append(args, "--from="+from)
	if q.Snapshot != "" {
		if !snapshotName.MatchString(q.Snapshot) {
			return nil, i18n.E("ui.bad_request").Hint("ui.bad_request.hint")
		}
		args = append(args, "--snapshot="+q.Snapshot)
	}
	switch q.Step {
	case stepCopy:
	case stepPreview:
		args = append(args, "--apply", "--dry-run")
	case stepApply:
		args = append(args, "--apply")
	default:
		return nil, i18n.E("ui.bad_request").Hint("ui.bad_request.hint")
	}
	return args, nil
}

// key identifies what a preview was about.
func (q restoreRequest) key() string {
	from := q.From
	if from == "" {
		from = "local"
	}
	return strings.Join([]string{q.Mode, q.Name, from, q.Snapshot}, "\x00")
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var request restoreRequest
	if !s.readBody(w, r, &request) {
		return
	}
	args, err := request.args()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Step == stepApply {
		if !request.Confirm {
			s.writeError(w, http.StatusForbidden, i18n.E("ui.restore.not_confirmed").Hint("ui.restore.not_confirmed.hint"))
			return
		}
		if !s.takePlan(request.Plan, request.key()) {
			s.writeError(w, http.StatusForbidden, i18n.E("ui.restore.no_preview").Hint("ui.restore.no_preview.hint"))
			return
		}
	}
	// The receipt is not part of the echo.
	echo := request
	echo.Plan, echo.Confirm = "", false
	s.startJob(w, kindRestore, request.Step, args, echo, request.key(), request.Step == stepPreview)
}

// takePlan uses up the receipt of a preview. A receipt works once, for the
// request it was made for.
func (s *Server) takePlan(plan, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.plans[plan]
	if plan == "" || !ok || entry.key != key || !time.Now().Before(entry.expires) {
		return false
	}
	delete(s.plans, plan)
	return true
}

// startJob runs a command in the background. One job at a time: two writers
// would only wait for each other's lock.
func (s *Server) startJob(w http.ResponseWriter, kind, step string, args []string, echo any, planKey string, issuePlan bool) {
	id, err := randomString(9)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, i18n.E("error.unexpected"))
		return
	}
	s.mu.Lock()
	if s.job != nil && s.job.State == stateRunning {
		s.mu.Unlock()
		s.writeError(w, http.StatusConflict, i18n.E("ui.job.busy").Hint("ui.job.busy.hint"))
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	job := &jobState{ID: id, Kind: kind, Step: step, State: stateRunning, Started: time.Now(), Request: echo, done: make(chan struct{})}
	s.job, s.cancelJob = job, cancel
	s.mu.Unlock()

	full := append(args, s.langArgs()...)
	go func() {
		defer close(job.done)
		defer cancel()
		reply := s.opts.Backend.Run(ctx, full)
		ended := time.Now()

		var answer struct {
			OK bool `json:"ok"`
		}
		_ = json.Unmarshal(reply.Body, &answer)
		s.mu.Lock()
		defer s.mu.Unlock()
		job.Ended, job.Result = &ended, json.RawMessage(bytes.TrimSpace(reply.Body))
		if !json.Valid(job.Result) {
			job.Result = nil
		}
		switch {
		case ctx.Err() != nil:
			job.State = stateCancelled
		case answer.OK && reply.Code == 0:
			job.State = stateDone
			if issuePlan {
				receipt, err := randomString(18)
				if err == nil {
					s.plans[receipt] = planEntry{key: planKey, expires: ended.Add(planTTL)}
					job.Plan = receipt
				}
			}
		default:
			job.State = stateFailed
		}
	}()
	s.writeOK(w, "job", map[string]string{"id": id})
}

func (s *Server) handleJob(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	var snapshot *jobState
	if s.job != nil {
		copied := *s.job
		snapshot = &copied
	}
	s.mu.Unlock()
	s.writeOK(w, "job", map[string]any{"job": snapshot, "now": time.Now()})
}

func (s *Server) handleCancel(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	cancel := s.cancelJob
	running := s.job != nil && s.job.State == stateRunning
	s.mu.Unlock()
	if running && cancel != nil {
		cancel()
	}
	s.writeOK(w, "job", map[string]bool{"cancelled": running})
}

// waitJob waits for the running job to end after it has been cancelled.
func (s *Server) waitJob() {
	s.mu.Lock()
	job := s.job
	s.mu.Unlock()
	if job != nil {
		<-job.done
	}
}
