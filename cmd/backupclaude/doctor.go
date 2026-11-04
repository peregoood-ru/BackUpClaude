package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Check results.
const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "fail"
	statusSkip = "skip"
)

// check is one line of the doctor report.
type check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type doctorReport struct {
	Healthy bool    `json:"healthy"`
	Checks  []check `json:"checks"`
}

func (a *app) newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: a.tr.T("cli.doctor.short"),
		Long:  a.tr.T("cli.doctor.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.doctor(cmd.Context())
		},
	}
}

func (a *app) doctor(ctx context.Context) error {
	var report doctorReport
	add := func(c check) { report.Checks = append(report.Checks, c) }

	add(check{ID: "platform", Status: statusOK, Message: a.tr.T("doctor.platform", version, runtime.GOOS, runtime.GOARCH)})

	layout, layoutErr := config.Resolve()
	if layoutErr != nil {
		add(a.failed("data_dir", layoutErr))
	} else {
		add(a.checkDataDir(layout))
	}

	var cfg *config.Config
	if layoutErr == nil {
		var c check
		cfg, c = a.checkConfig(layout)
		add(c)
	}

	var restic *engine.Installed
	if layoutErr == nil {
		installer, err := a.installer(layout)
		if err != nil {
			add(a.failed("tools", err))
		} else {
			var c check
			restic, c = a.checkRestic(ctx, installer)
			add(c)
			add(a.checkRclone(ctx, installer))
		}
	}

	passwordStored, c := a.checkPassword()
	add(c)
	add(a.checkRepository(ctx, cfg, restic, passwordStored))
	add(a.checkClaudeData())
	add(a.checkDesktopData())

	report.Healthy = true
	for _, item := range report.Checks {
		if item.Status == statusFail {
			report.Healthy = false
		}
	}
	if a.jsonOut {
		if err := a.writeJSON(envelope{OK: report.Healthy, Command: a.command, Data: report}); err != nil {
			return err
		}
	} else {
		a.printReport(a.stdout, report)
	}
	if !report.Healthy {
		return errReported
	}
	return nil
}

func (a *app) printReport(w io.Writer, report doctorReport) {
	for _, item := range report.Checks {
		fmt.Fprintf(w, "[%s] %s\n", a.tr.T("doctor.status."+item.Status), item.Message)
		if item.Hint != "" {
			fmt.Fprintf(w, "      %s %s\n", a.tr.T("cli.hint_prefix"), item.Hint)
		}
	}
	if report.Healthy {
		fmt.Fprintln(w, a.tr.T("doctor.summary.healthy"))
	} else {
		fmt.Fprintln(w, a.tr.T("doctor.summary.problems"))
	}
}

// failed turns an error into a failed check.
func (a *app) failed(id string, err error) check {
	return a.fromError(id, statusFail, err)
}

func (a *app) fromError(id, status string, err error) check {
	description := a.tr.Describe(err)
	message := description.Message
	if len(description.Details) > 0 {
		message += " (" + strings.Join(description.Details, "; ") + ")"
	}
	return check{ID: id, Status: status, Message: message, Hint: description.Hint}
}

func (a *app) checkDataDir(layout config.Layout) check {
	_, err := os.Stat(layout.Root)
	if errors.Is(err, fs.ErrNotExist) {
		return check{ID: "data_dir", Status: statusWarn,
			Message: a.tr.T("doctor.data_dir.absent", layout.Root), Hint: a.tr.T("doctor.run_setup.hint")}
	}
	probe, err := os.CreateTemp(layout.Root, ".doctor-*")
	if err != nil {
		return check{ID: "data_dir", Status: statusFail,
			Message: a.tr.T("doctor.data_dir.readonly", layout.Root), Hint: a.tr.T("doctor.data_dir.readonly.hint")}
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return check{ID: "data_dir", Status: statusOK, Message: a.tr.T("doctor.data_dir.ok", layout.Root)}
}

func (a *app) checkConfig(layout config.Layout) (*config.Config, check) {
	cfg, err := config.Load(layout.ConfigFile())
	switch {
	case err == nil:
		return cfg, check{ID: "config", Status: statusOK, Message: a.tr.T("doctor.config.ok", layout.ConfigFile())}
	case config.IsMissing(err):
		return nil, a.fromError("config", statusWarn, err)
	default:
		return nil, a.failed("config", err)
	}
}

func (a *app) checkRestic(ctx context.Context, installer *engine.Installer) (*engine.Installed, check) {
	tool, err := installer.Locate(engine.ToolRestic)
	if err != nil {
		return nil, a.toolProblem(engine.ToolRestic, err)
	}
	info, err := (&engine.Restic{Binary: tool.Path}).Version(ctx)
	if err != nil {
		return nil, a.failed(engine.ToolRestic, err)
	}
	return tool, check{ID: engine.ToolRestic, Status: statusOK, Message: a.tr.T("doctor.tool.ok", engine.ToolRestic, info.Version, tool.Path)}
}

func (a *app) checkRclone(ctx context.Context, installer *engine.Installer) check {
	tool, err := installer.Locate(engine.ToolRclone)
	if err != nil {
		return a.toolProblem(engine.ToolRclone, err)
	}
	found, err := engine.RcloneVersion(ctx, tool.Path)
	if err != nil {
		return a.failed(engine.ToolRclone, err)
	}
	return check{ID: engine.ToolRclone, Status: statusOK, Message: a.tr.T("doctor.tool.ok", engine.ToolRclone, found, tool.Path)}
}

func (a *app) toolProblem(id string, err error) check {
	if engine.IsNotInstalled(err) {
		return a.fromError(id, statusWarn, err)
	}
	return a.failed(id, err)
}

func (a *app) checkPassword() (bool, check) {
	_, err := a.deps.secrets().Get(secrets.RepoPasswordKey)
	switch {
	case err == nil:
		return true, check{ID: "password", Status: statusOK, Message: a.tr.T("doctor.password.ok")}
	case errors.Is(err, secrets.ErrNotFound):
		return false, check{ID: "password", Status: statusWarn,
			Message: a.tr.T("password.missing"), Hint: a.tr.T("password.missing.hint")}
	default:
		return false, a.failed("password", err)
	}
}

func (a *app) checkRepository(ctx context.Context, cfg *config.Config, restic *engine.Installed, passwordStored bool) check {
	if cfg == nil || restic == nil || !passwordStored {
		return check{ID: "repository", Status: statusSkip, Message: a.tr.T("doctor.repository.skipped")}
	}
	executable, err := a.deps.executable()
	if err != nil {
		return a.failed("repository", err)
	}
	r := &engine.Restic{
		Binary:          restic.Path,
		Repo:            cfg.Repositories.Local,
		PasswordCommand: engine.PasswordCommand(executable, resticPasswordCommand),
	}
	if err := r.Probe(ctx); err != nil {
		if engine.IsKind(err, engine.KindRepoMissing) {
			return a.fromError("repository", statusWarn, err)
		}
		return a.failed("repository", err)
	}
	return check{ID: "repository", Status: statusOK, Message: a.tr.T("doctor.repository.ok", r.Repo)}
}

func (a *app) checkClaudeData() check {
	manifest, err := manifests.Load(runtime.GOOS)
	if err != nil {
		return a.failed("claude_data", err)
	}
	env, err := a.deps.env()
	if err != nil {
		return a.failed("claude_data", err)
	}
	found := discover.Locate(manifest, env)
	if !found.Home.Exists {
		return check{ID: "claude_data", Status: statusWarn,
			Message: a.tr.T("doctor.claude.absent", found.Home.Path), Hint: a.tr.T("doctor.claude.absent.hint")}
	}
	return check{ID: "claude_data", Status: statusOK, Message: a.tr.T("doctor.claude.ok", found.Home.Path)}
}

func (a *app) checkDesktopData() check {
	manifest, err := manifests.Load(runtime.GOOS)
	if err != nil {
		return a.failed("desktop_data", err)
	}
	env, err := a.deps.env()
	if err != nil {
		return a.failed("desktop_data", err)
	}
	found := discover.Locate(manifest, env)
	if len(found.Desktop) == 0 {
		return check{ID: "desktop_data", Status: statusSkip, Message: a.tr.T("doctor.desktop.absent")}
	}
	paths := make([]string, len(found.Desktop))
	for i, location := range found.Desktop {
		paths[i] = location.Path
	}
	return check{ID: "desktop_data", Status: statusOK, Message: a.tr.T("doctor.desktop.ok", strings.Join(paths, "; "))}
}
