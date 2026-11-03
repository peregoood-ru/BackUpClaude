package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/notify"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/sysproc"
)

// maintenanceLockWait is how long a cleanup or check waits for backups to
// finish: they hold the repository, and the first run of a profile is long.
const maintenanceLockWait = 3 * time.Hour

func (a *app) newRunCommand() *cobra.Command {
	var job string
	cmd := &cobra.Command{
		Use:    "run",
		Short:  a.tr.T("cli.run.short"),
		Long:   a.tr.T("cli.run.long"),
		Hidden: true,
		Args:   a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runScheduled(cmd.Context(), job)
		},
	}
	cmd.Flags().StringVar(&job, "job", "", a.tr.T("flag.run.job"))
	_ = cmd.MarkFlagRequired("job")
	return cmd
}

// runRecord is one line of the run log.
type runRecord struct {
	Time    string  `json:"time"`
	Job     string  `json:"job"`
	OK      bool    `json:"ok"`
	Seconds float64 `json:"seconds"`
	// PeakCommitMB is the most memory this run and restic held together.
	PeakCommitMB float64           `json:"peak_commit_mb,omitempty"`
	Notes        []string          `json:"notes,omitempty"`
	Result       any               `json:"result,omitempty"`
	Error        *i18n.Description `json:"error,omitempty"`
}

// profileSummary is the part of a profile's result worth keeping in the log.
type profileSummary struct {
	Profile      string  `json:"profile"`
	Status       string  `json:"status"`
	SnapshotID   string  `json:"snapshot_id,omitempty"`
	Files        int64   `json:"files"`
	FilesNew     int64   `json:"files_new"`
	FilesChanged int64   `json:"files_changed"`
	Bytes        int64   `json:"bytes"`
	DataAdded    int64   `json:"data_added"`
	ResticSecond float64 `json:"restic_seconds"`
	LockedFiles  int     `json:"locked_files,omitempty"`
	SkippedFiles int     `json:"skipped_files,omitempty"`
	ErrorCode    string  `json:"error_code,omitempty"`
}

// jobKnown reports whether job is a value of --job.
func jobKnown(job string) bool {
	for _, name := range schedule.JobNames() {
		if job == name {
			return true
		}
	}
	return false
}

// run is what a scheduled task does. It makes itself polite first (low CPU,
// disk and memory priority, which restic inherits), ties restic to itself so a
// killed run leaves no stray process, removes the locks of a crashed run, does
// the job, and writes one line to the run log whatever happened.
func (a *app) runScheduled(ctx context.Context, job string) error {
	if !jobKnown(job) {
		return i18n.E("run.unknown_job", job, joinNames(schedule.JobNames())).Hint("run.unknown_job.hint").Exit(exitUsage)
	}
	record := runRecord{Time: time.Now().Format(time.RFC3339), Job: job}
	started := time.Now()

	if a.deps.lower != nil {
		if err := a.deps.lower(); err != nil {
			record.Notes = append(record.Notes, "priority: "+err.Error())
		}
	}
	var group *sysproc.Group
	if a.deps.contain != nil {
		var err error
		if group, err = a.deps.contain(); err != nil {
			record.Notes = append(record.Notes, "job object: "+err.Error())
		}
	}

	current, err := a.openSession()
	layout, layoutErr := config.Resolve()
	notifier, notifyNote := a.notifierOf(current, layout, layoutErr)
	if notifyNote != "" {
		record.Notes = append(record.Notes, notifyNote)
	}
	if err == nil {
		if job == schedule.JobClaude {
			a.guardClaude(ctx, current, notifier, &record)
		}
		record.Result, err = a.runJob(ctx, current, job, notifier, &record)
	}

	record.Seconds = time.Since(started).Seconds()
	record.OK = err == nil
	record.PeakCommitMB = float64(group.PeakMemory()) / (1 << 20)
	if err != nil {
		description := a.tr.Describe(err)
		record.Error = &description
		if layoutErr == nil {
			a.tellFailure(ctx, layout.WatchFile(), notifier, job, err, &record)
		}
	} else if layoutErr == nil {
		if clearErr := clearFailure(layout.WatchFile(), job); clearErr != nil {
			record.Notes = append(record.Notes, "watch: "+clearErr.Error())
		}
	}
	if layoutErr == nil {
		if logErr := appendRunLog(layout, record); logErr != nil {
			fmt.Fprintln(a.stderr, a.tr.T("run.log_failed", logErr))
		}
	}
	if err != nil {
		return err
	}
	return a.emit(record, func(w io.Writer) {
		fmt.Fprintln(w, a.tr.T("run.done", job, record.Seconds))
	})
}

func joinNames(names []string) string {
	text := ""
	for i, name := range names {
		if i > 0 {
			text += ", "
		}
		text += name
	}
	return text
}

// runJob does the work of one job and returns what to write to the log.
func (a *app) runJob(ctx context.Context, current *session, job string, notifier *notify.Notifier, record *runRecord) (any, error) {
	switch job {
	case schedule.JobSummary:
		return a.sendSummary(ctx, current, notifier, record)

	case schedule.JobClaude, schedule.JobProjects:
		report, failed, err := a.saveProfiles(ctx, current, []string{job}, false)
		if err != nil {
			return nil, err
		}
		summaries := make([]profileSummary, 0, len(report.Profiles))
		for _, result := range report.Profiles {
			summary := profileSummary{
				Profile: result.Profile, Status: result.Status, SnapshotID: result.SnapshotID,
				Files: result.Files, FilesNew: result.FilesNew, FilesChanged: result.FilesChanged,
				Bytes: result.Bytes, DataAdded: result.DataAdded, ResticSecond: result.Seconds,
				LockedFiles: len(result.LockedFiles), SkippedFiles: len(result.SkippedFiles),
			}
			if result.Error != nil {
				summary.ErrorCode = result.Error.Code
			}
			summaries = append(summaries, summary)
		}
		if failed {
			if len(report.Profiles) == 1 && report.Profiles[0].Error != nil {
				return summaries, i18n.E("run.profile_failed", job, report.Profiles[0].Error.Message)
			}
			return summaries, i18n.E("run.profile_incomplete", job).Hint("run.profile_incomplete.hint")
		}
		return summaries, nil

	case schedule.JobRestoreTest:
		return a.runRestoreTest(ctx, current)

	case schedule.JobCleanup:
		current.restic.RetryLock = maintenanceLockWait
		if err := current.restic.Unlock(ctx); err != nil {
			return nil, err
		}
		retention := current.config.Retention
		result, err := current.restic.Forget(ctx, engine.Retention{
			Hourly: retention.Hourly, Daily: retention.Daily, Weekly: retention.Weekly, Monthly: retention.Monthly,
		}, engine.ForgetOptions{Prune: true})
		return result, err

	default: // schedule.JobCheck
		current.restic.RetryLock = maintenanceLockWait
		if err := current.restic.Unlock(ctx); err != nil {
			return nil, err
		}
		result, err := current.restic.Check(ctx)
		if err == nil && result != nil && result.Errors > 0 {
			err = i18n.E("run.check_errors", result.Errors).Hint("run.check_errors.hint")
		}
		return result, err
	}
}

// notifierOf makes the channels for a run. The settings are enough: a run that
// cannot open the repository (no restic yet, no password) must still be able to
// say so. The note, if any, goes to the run log.
func (a *app) notifierOf(current *session, layout config.Layout, layoutErr error) (*notify.Notifier, string) {
	var cfg *config.Config
	switch {
	case current != nil:
		cfg = current.config
	case layoutErr == nil:
		loaded, err := config.Load(layout.ConfigFile())
		if err != nil {
			return nil, ""
		}
		cfg = loaded
	default:
		return nil, ""
	}
	notifier, err := a.notifierFor(cfg)
	if err != nil {
		return notifier, "notify: " + a.tr.Describe(err).Message
	}
	return notifier, ""
}

// appendRunLog adds one line to the log of the month. One small file per month
// keeps the log readable and never needs cleaning up.
func appendRunLog(layout config.Layout, record runRecord) error {
	if err := os.MkdirAll(layout.LogsDir(), 0o700); err != nil {
		return err
	}
	name := "runs-" + time.Now().Format("2006-01") + ".jsonl"
	file, err := os.OpenFile(filepath.Join(layout.LogsDir(), name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	line, err := json.Marshal(record)
	if err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
