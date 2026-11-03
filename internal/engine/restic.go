package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// EnvPasswordHelper is set for restic and the password helper it starts. The
// helper refuses to run without it, so a stray call prints nothing.
const EnvPasswordHelper = "BACKUPCLAUDE_RESTIC_CHILD"

// Exit codes from restic's scripting guide.
const (
	exitIncomplete    = 3
	exitRepoMissing   = 10
	exitLockFailed    = 11
	exitWrongPassword = 12
	exitInterrupted   = 130
)

const (
	stderrTailLimit = 64 << 10
	maxLineSize     = 64 << 20
	killWaitDelay   = 15 * time.Second
)

// Kind classifies a restic failure.
type Kind int

// Failure kinds.
const (
	KindFatal Kind = iota
	KindIncomplete
	KindRepoMissing
	KindLocked
	KindWrongPassword
	KindInterrupted
)

// ResticError is a failed restic run.
type ResticError struct {
	Kind     Kind
	ExitCode int
	Message  string
}

func (e *ResticError) Error() string {
	return "restic: " + e.Message
}

// Localized implements i18n.Localizer.
func (e *ResticError) Localized() *i18n.Error {
	switch e.Kind {
	case KindIncomplete:
		return i18n.E("engine.restic.incomplete").Hint("engine.restic.incomplete.hint")
	case KindRepoMissing:
		return i18n.E("engine.restic.repo_missing").Hint("engine.restic.repo_missing.hint")
	case KindLocked:
		return i18n.E("engine.restic.locked").Hint("engine.restic.locked.hint")
	case KindWrongPassword:
		return i18n.E("engine.restic.wrong_password").Hint("engine.restic.wrong_password.hint")
	case KindInterrupted:
		return i18n.E("engine.restic.interrupted")
	default:
		return i18n.E("engine.restic.fatal", e.ExitCode, e.Message).Hint("engine.restic.fatal.hint")
	}
}

// IsKind reports whether err is a restic failure of the given kind.
func IsKind(err error, kind Kind) bool {
	var e *ResticError
	return errors.As(err, &e) && e.Kind == kind
}

// Restic runs one restic binary against one repository.
type Restic struct {
	Binary string
	// Repo is any restic repository location.
	Repo string
	// PasswordCommand is the value of restic's --password-command.
	PasswordCommand string
	// CacheDir is optional.
	CacheDir string
	// ExtraEnv is appended to the child environment, mostly for tests.
	ExtraEnv []string
	// RetryLock makes restic wait this long for a repository that another run
	// holds, instead of failing at once. Zero means no waiting.
	RetryLock time.Duration
}

// lockingCommands are the restic commands that take a lock a running backup or
// cleanup can block.
var lockingCommands = map[string]bool{"backup": true, "forget": true, "prune": true, "check": true, "snapshots": true, "stats": true}

// PasswordCommand builds a --password-command value. restic splits it like a
// shell does and treats a backslash outside quotes as a separator, so Windows
// paths must be quoted too.
func PasswordCommand(executable string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteArgument(executable))
	for _, arg := range args {
		parts = append(parts, quoteArgument(arg))
	}
	return strings.Join(parts, " ")
}

func quoteArgument(arg string) string {
	if !strings.ContainsAny(arg, " \t'\"\\") {
		return arg
	}
	if strings.Contains(arg, `"`) {
		return "'" + arg + "'"
	}
	return `"` + arg + `"`
}

// environ passes the parent environment without anything that could override
// the explicit repository and password settings.
func (r *Restic) environ() []string {
	env := make([]string, 0, len(os.Environ())+len(r.ExtraEnv)+1)
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(name) {
		case "RESTIC_PASSWORD", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND",
			"RESTIC_REPOSITORY", "RESTIC_REPOSITORY_FILE", EnvPasswordHelper:
			continue
		}
		env = append(env, item)
	}
	env = append(env, EnvPasswordHelper+"=1")
	return append(env, r.ExtraEnv...)
}

func (r *Restic) repoArgs(command string, args ...string) []string {
	full := []string{command, "--json", "--repo", r.Repo}
	if r.PasswordCommand != "" {
		full = append(full, "--password-command", r.PasswordCommand)
	}
	if r.CacheDir != "" {
		full = append(full, "--cache-dir", r.CacheDir)
	}
	if r.RetryLock > 0 && lockingCommands[command] {
		full = append(full, "--retry-lock", r.RetryLock.String())
	}
	return append(full, args...)
}

// run starts restic and hands every stdout line to onLine. restic --json
// reports per-file errors on stderr; onErrorLine gets those lines at the end.
func (r *Restic) run(ctx context.Context, args []string, onLine, onErrorLine func(line []byte)) error {
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Env = r.environ()
	cmd.WaitDelay = killWaitDelay
	stderr := &tailBuffer{limit: stderrTailLimit}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return i18n.E("engine.restic.start_failed", r.Binary).Because(err)
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return &ResticError{Kind: KindInterrupted, ExitCode: exitInterrupted, Message: ctx.Err().Error()}
		}
		return i18n.E("engine.restic.start_failed", r.Binary).Hint("engine.tool.install.hint").Because(err)
	}
	readErr := forEachLine(stdout, onLine)
	waitErr := cmd.Wait()
	if onErrorLine != nil {
		_ = forEachLine(strings.NewReader(stderr.String()), onErrorLine)
	}

	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return &ResticError{Kind: KindInterrupted, ExitCode: exitInterrupted, Message: ctx.Err().Error()}
	case errors.As(waitErr, &exitErr):
		return classify(exitErr.ExitCode(), stderr.String())
	case waitErr != nil:
		return i18n.E("engine.restic.start_failed", r.Binary).Because(waitErr)
	case readErr != nil:
		return i18n.E("engine.restic.bad_output").Because(readErr)
	}
	return nil
}

func classify(code int, stderr string) *ResticError {
	message := exitMessage(stderr)
	kind := KindFatal
	switch code {
	case exitIncomplete:
		kind = KindIncomplete
	case exitRepoMissing:
		kind = KindRepoMissing
	case exitLockFailed:
		kind = KindLocked
	case exitWrongPassword:
		kind = KindWrongPassword
	case exitInterrupted:
		kind = KindInterrupted
	}
	return &ResticError{Kind: kind, ExitCode: code, Message: message}
}

// exitMessage extracts the reason from stderr, where restic --json reports a
// fatal error as an exit_error message.
func exitMessage(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var msg struct {
			MessageType string `json:"message_type"`
			Message     string `json:"message"`
		}
		line := strings.TrimSpace(lines[i])
		if json.Unmarshal([]byte(line), &msg) == nil && msg.MessageType == "exit_error" && msg.Message != "" {
			return msg.Message
		}
	}
	return strings.TrimSpace(stderr)
}

func forEachLine(r io.Reader, onLine func(line []byte)) error {
	reader := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > maxLineSize {
			return errors.New("line too long")
		}
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 && onLine != nil {
			onLine(trimmed)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// tailBuffer keeps the end of a stream.
type tailBuffer struct {
	limit int
	buf   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if extra := len(t.buf) - t.limit; extra > 0 {
		t.buf = t.buf[extra:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

// VersionInfo is the answer of restic version --json.
type VersionInfo struct {
	Version   string `json:"version"`
	GoVersion string `json:"go_version"`
	GoOS      string `json:"go_os"`
	GoArch    string `json:"go_arch"`
}

// Version asks restic for its version.
func (r *Restic) Version(ctx context.Context) (*VersionInfo, error) {
	var info VersionInfo
	var found bool
	err := r.run(ctx, []string{"version", "--json"}, func(line []byte) {
		var candidate VersionInfo
		if json.Unmarshal(line, &candidate) == nil && candidate.Version != "" {
			info, found = candidate, true
		}
	}, nil)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, i18n.E("engine.restic.bad_output")
	}
	return &info, nil
}

// Init creates the repository and returns its id.
func (r *Restic) Init(ctx context.Context) (string, error) {
	var id string
	err := r.run(ctx, r.repoArgs("init"), func(line []byte) {
		var msg struct {
			MessageType string `json:"message_type"`
			ID          string `json:"id"`
		}
		if json.Unmarshal(line, &msg) == nil && msg.MessageType == "initialized" {
			id = msg.ID
		}
	}, nil)
	return id, err
}

// Probe opens the repository without locking it. A missing repository gives
// KindRepoMissing, a wrong password KindWrongPassword.
func (r *Restic) Probe(ctx context.Context) error {
	return r.run(ctx, r.repoArgs("cat", "--no-lock", "config"), nil, nil)
}

// BackupOptions selects what a backup run stores.
type BackupOptions struct {
	Paths    []string
	Tags     []string
	Excludes []string
	// ExcludeFile holds case-insensitive patterns, one per line. A file has no
	// length limit, and a command line does.
	ExcludeFile string
	// GroupByTags finds the parent snapshot by host and tags, not by the exact
	// set of paths. Otherwise a new project makes restic read every file again.
	GroupByTags bool
	// DryRun reads the files and stores nothing.
	DryRun bool
}

// BackupResult is the outcome of a backup run. Skipped lists what restic could
// not read; such a run also returns KindIncomplete.
type BackupResult struct {
	Summary *BackupSummary
	Skipped []ItemError
}

// Backup creates a snapshot.
func (r *Restic) Backup(ctx context.Context, opts BackupOptions) (*BackupResult, error) {
	args := []string{}
	for _, tag := range opts.Tags {
		args = append(args, "--tag", tag)
	}
	for _, pattern := range opts.Excludes {
		args = append(args, "--exclude", pattern)
	}
	if opts.ExcludeFile != "" {
		args = append(args, "--iexclude-file", opts.ExcludeFile)
	}
	if opts.GroupByTags {
		args = append(args, "--group-by", "host,tags")
	}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	args = append(args, "--")
	args = append(args, opts.Paths...)

	result := &BackupResult{}
	err := r.run(ctx, r.repoArgs("backup", args...), func(line []byte) {
		summary, item := parseBackupLine(line)
		if summary != nil {
			result.Summary = summary
		}
		if item != nil {
			result.Skipped = append(result.Skipped, *item)
		}
	}, func(line []byte) {
		if _, item := parseBackupLine(line); item != nil {
			result.Skipped = append(result.Skipped, *item)
		}
	})
	if err != nil {
		return result, err
	}
	if result.Summary == nil {
		return result, i18n.E("engine.restic.bad_output")
	}
	return result, nil
}

// RestoreOptions selects what a restore run writes.
type RestoreOptions struct {
	Snapshot string
	// Subfolder is a path inside the snapshot, in restic form: /C/Users/name.
	// Its content lands directly in Target, and no parent folder is recreated.
	// Parents carry the access rights of system folders: on Windows an ordinary
	// user who restores them gets access denied.
	Subfolder string
	Target    string
	Includes  []string
}

// Restore writes a snapshot to the target folder. On failure the error names
// the first files restic could not write.
func (r *Restic) Restore(ctx context.Context, opts RestoreOptions) (*RestoreSummary, error) {
	snapshot := opts.Snapshot
	if opts.Subfolder != "" {
		snapshot += ":" + opts.Subfolder
	}
	args := []string{snapshot, "--target", opts.Target}
	for _, pattern := range opts.Includes {
		args = append(args, "--include", pattern)
	}
	var summary *RestoreSummary
	var failed []ItemError
	err := r.run(ctx, r.repoArgs("restore", args...), func(line []byte) {
		parsed, item := parseRestoreLine(line)
		if parsed != nil {
			summary = parsed
		}
		if item != nil {
			failed = append(failed, *item)
		}
	}, func(line []byte) {
		if _, item := parseRestoreLine(line); item != nil {
			failed = append(failed, *item)
		}
	})
	if err != nil {
		var resticErr *ResticError
		if errors.As(err, &resticErr) {
			resticErr.Message = withItems(resticErr.Message, failed)
		}
		return summary, err
	}
	if summary == nil {
		return nil, i18n.E("engine.restic.bad_output")
	}
	return summary, nil
}

func withItems(message string, items []ItemError) string {
	const shown = 3
	for i, item := range items {
		if i == shown {
			break
		}
		message += "; " + item.Item + ": " + item.Message
	}
	return message
}

// Ls walks a snapshot, optionally only below the given snapshot paths, and hands
// every node to onNode. Snapshots can hold hundreds of thousands of files, so
// nothing is collected here. restic stops at the direct children of a given path
// unless told otherwise, so the walk asks for the whole tree.
func (r *Restic) Ls(ctx context.Context, snapshot string, paths []string, onNode func(Node)) error {
	args := make([]string, 0, len(paths)+2)
	if len(paths) > 0 {
		args = append(args, "--recursive")
	}
	args = append(append(args, snapshot), paths...)
	return r.run(ctx, r.repoArgs("ls", args...), func(line []byte) {
		if node, ok := parseNode(line); ok {
			onNode(node)
		}
	}, nil)
}

// Snapshots lists snapshots, optionally only those with a tag.
func (r *Restic) Snapshots(ctx context.Context, tag string) ([]Snapshot, error) {
	args := []string{}
	if tag != "" {
		args = append(args, "--tag", tag)
	}
	var output bytes.Buffer
	err := r.run(ctx, r.repoArgs("snapshots", args...), func(line []byte) {
		output.Write(line)
	}, nil)
	if err != nil {
		return nil, err
	}
	return parseSnapshots(output.Bytes())
}
