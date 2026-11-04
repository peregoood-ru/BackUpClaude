package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/profile"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

const profileAll = "all"

const (
	kilobyte = 1000
	megabyte = 1000 * kilobyte
	gigabyte = 1000 * megabyte
)

// Statuses of one profile's run.
const (
	backupOK         = "ok"
	backupLocked     = "locked_files"
	backupIncomplete = "incomplete"
	backupFailed     = "failed"
	backupPlanned    = "dry_run"
)

func (a *app) newBackupCommand() *cobra.Command {
	var name string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "backup",
		Short: a.tr.T("cli.backup.short"),
		Long:  a.tr.T("cli.backup.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.backup(cmd.Context(), name, dryRun)
		},
	}
	cmd.Flags().StringVar(&name, "profile", profileAll, a.tr.T("flag.profile"))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, a.tr.T("flag.backup.dry_run"))
	return cmd
}

// profileNames expands the --profile value.
func profileNames(value string) ([]string, error) {
	if value == profileAll {
		return profile.Names(), nil
	}
	for _, name := range profile.Names() {
		if value == name {
			return []string{name}, nil
		}
	}
	return nil, i18n.E("backup.unknown_profile", value, strings.Join(profile.Names(), ", ")).Hint("backup.unknown_profile.hint").Exit(exitUsage)
}

// enabledProfiles narrows the profiles of a request to those the settings keep
// on. A full run takes only those; a request for one profile that is off is
// answered with the reason and the way to switch it on.
func enabledProfiles(value string, names []string, cfg *config.Config) ([]string, error) {
	if value == profileAll {
		return cfg.Profiles.Names(), nil
	}
	for _, name := range names {
		if !cfg.Profiles.Enabled(name) {
			return nil, i18n.E("backup.profile_off", name).Hint("backup.profile_off.hint")
		}
	}
	return names, nil
}

// planInputs gathers what a plan depends on.
func (a *app) planInputs(layout config.Layout, cfg *config.Config) (profile.Inputs, error) {
	manifest, err := manifests.Load(runtime.GOOS)
	if err != nil {
		return profile.Inputs{}, err
	}
	env, err := a.deps.env()
	if err != nil {
		return profile.Inputs{}, i18n.E("cli.no_home").Because(err)
	}
	return profile.Inputs{Manifest: manifest, Env: env, Config: cfg, Layout: layout, Now: time.Now().UTC()}, nil
}

// profileResult is the outcome of one profile.
type profileResult struct {
	Profile      string             `json:"profile"`
	Status       string             `json:"status"`
	Plan         *profile.Plan      `json:"plan,omitempty"`
	SnapshotID   string             `json:"snapshot_id,omitempty"`
	FilesNew     int64              `json:"files_new"`
	FilesChanged int64              `json:"files_changed"`
	Files        int64              `json:"files"`
	Bytes        int64              `json:"bytes"`
	DataAdded    int64              `json:"data_added"`
	Seconds      float64            `json:"seconds"`
	LockedFiles  []engine.ItemError `json:"locked_files,omitempty"`
	// MetadataFiles are saved, but without some attributes.
	MetadataFiles []engine.ItemError `json:"metadata_files,omitempty"`
	SkippedFiles  []engine.ItemError `json:"skipped_files,omitempty"`
	Error         *i18n.Description  `json:"error,omitempty"`
}

type backupReport struct {
	DryRun   bool            `json:"dry_run"`
	Profiles []profileResult `json:"profiles"`
}

func (a *app) backup(ctx context.Context, value string, dryRun bool) error {
	names, err := profileNames(value)
	if err != nil {
		return err
	}
	current, err := a.openSession()
	if err != nil {
		return err
	}
	if names, err = enabledProfiles(value, names, current.config); err != nil {
		return err
	}
	report, failed, err := a.saveProfiles(ctx, current, names, dryRun)
	if err != nil {
		return err
	}
	if a.jsonOut {
		if err := a.writeJSON(envelope{OK: !failed, Command: a.command, Data: report}); err != nil {
			return err
		}
	} else {
		a.printBackup(a.stdout, report)
	}
	if failed {
		return errReported
	}
	return nil
}

// saveProfiles removes the locks a crashed run left behind and runs the
// profiles one after another. failed is true when a profile has no complete
// snapshot. A dry run writes nothing, so it leaves the locks alone.
func (a *app) saveProfiles(ctx context.Context, current *session, names []string, dryRun bool) (report backupReport, failed bool, err error) {
	in, err := a.planInputs(current.layout, current.config)
	if err != nil {
		return report, false, err
	}
	if !dryRun {
		if err := current.restic.Unlock(ctx); err != nil {
			return report, false, err
		}
	}
	report.DryRun = dryRun
	for _, name := range names {
		result := a.runProfile(ctx, current, name, in, dryRun)
		failed = failed || result.Status == backupFailed || result.Status == backupIncomplete
		report.Profiles = append(report.Profiles, result)
	}
	return report, failed, nil
}

// runProfile never stops the other profile: a failure of one is reported in its
// result, and the other still runs.
func (a *app) runProfile(ctx context.Context, current *session, name string, in profile.Inputs, dryRun bool) profileResult {
	result := profileResult{Profile: name}
	failed := func(err error) profileResult {
		description := a.tr.Describe(err)
		result.Status = backupFailed
		result.Error = &description
		return result
	}
	plan, err := profile.Build(name, in)
	if err != nil {
		return failed(err)
	}
	result.Plan = plan
	if len(plan.Items) == 0 {
		return failed(i18n.E("backup.nothing_to_save", name).Hint("backup.nothing_to_save.hint"))
	}
	if !dryRun {
		// Before restic starts: a crash must not cost the newly found projects.
		if err := plan.SaveState(in); err != nil {
			return failed(err)
		}
	}
	excludeFile, err := plan.WriteExcludeFile(current.layout.TempDir())
	if err != nil {
		return failed(err)
	}
	defer os.Remove(excludeFile)

	backup, err := current.restic.Backup(ctx, plan.Options(excludeFile, dryRun))
	if backup != nil && backup.Summary != nil {
		fillSummary(&result, backup.Summary)
	}
	if backup != nil {
		result.LockedFiles, result.MetadataFiles, result.SkippedFiles = plan.SortSkipped(backup.Skipped)
	}
	switch {
	case err != nil && !engine.IsKind(err, engine.KindIncomplete):
		return failed(err)
	case len(result.SkippedFiles) > 0, err != nil && len(result.LockedFiles)+len(result.MetadataFiles) == 0:
		result.Status = backupIncomplete
	case dryRun:
		result.Status = backupPlanned
	case len(result.LockedFiles) > 0:
		result.Status = backupLocked
	default:
		result.Status = backupOK
	}
	return result
}

func fillSummary(result *profileResult, summary *engine.BackupSummary) {
	result.SnapshotID = summary.SnapshotID
	result.FilesNew = summary.FilesNew
	result.FilesChanged = summary.FilesChanged
	result.Files = summary.TotalFilesProcessed
	result.Bytes = summary.TotalBytesProcessed
	result.DataAdded = summary.DataAddedPacked
	result.Seconds = summary.TotalDuration
}

func (a *app) printBackup(w io.Writer, report backupReport) {
	for _, result := range report.Profiles {
		fmt.Fprintln(w, a.tr.T("backup.profile", result.Profile, a.tr.T("backup.status."+result.Status)))
		if result.Plan != nil {
			a.printPlan(w, result.Plan)
		}
		if result.Status != backupFailed {
			fmt.Fprintln(w, "  "+a.tr.T("backup.totals", result.Files, formatBytes(result.Bytes), result.FilesNew, result.FilesChanged, formatBytes(result.DataAdded), result.Seconds))
		}
		if result.SnapshotID != "" {
			fmt.Fprintln(w, "  "+a.tr.T("backup.snapshot", result.SnapshotID))
		}
		for _, item := range result.LockedFiles {
			fmt.Fprintln(w, "  "+a.tr.T("backup.locked", item.Item))
		}
		if count := len(result.MetadataFiles); count > 0 {
			fmt.Fprintln(w, "  "+a.tr.T("backup.metadata", count))
		}
		for _, item := range result.SkippedFiles {
			fmt.Fprintln(w, "  "+a.tr.T("backup.skipped", item.Item, item.Message))
		}
		if result.Error != nil {
			fmt.Fprintln(w, "  "+a.tr.T("cli.error_prefix"), result.Error.Message)
			if result.Error.Hint != "" {
				fmt.Fprintln(w, "  "+a.tr.T("cli.hint_prefix"), result.Error.Hint)
			}
		}
	}
}

func (a *app) printPlan(w io.Writer, plan *profile.Plan) {
	fmt.Fprintln(w, "  "+a.tr.T("backup.plan.counts", len(plan.Items), len(plan.Excludes)))
	for _, item := range plan.Items {
		fmt.Fprintln(w, "    "+item.Path)
	}
	for _, left := range plan.Left {
		if left.Inside != "" {
			fmt.Fprintln(w, "  "+a.tr.T("backup.plan.inside", left.Path, left.Inside))
		} else {
			fmt.Fprintln(w, "  "+a.tr.T("backup.plan.excluded", left.Path))
		}
	}
	for _, skipped := range plan.Skipped {
		fmt.Fprintln(w, "  "+a.tr.T("backup.plan.not_project", skipped.Path, a.tr.T("backup.skip."+skipped.Reason), skipped.Sessions))
	}
	a.printWarnings(w, plan.Warnings)
}

func (a *app) printWarnings(w io.Writer, warnings []profile.Warning) {
	for _, warning := range warnings {
		text := a.tr.T("plan.warning." + warning.Code)
		if warning.Path != "" {
			text += " " + warning.Path
		}
		fmt.Fprintln(w, "  "+text)
	}
}

// formatBytes shows a size in the unit a person expects.
func formatBytes(size int64) string {
	switch {
	case size >= gigabyte:
		return fmt.Sprintf("%.2f GB", float64(size)/gigabyte)
	case size >= megabyte:
		return fmt.Sprintf("%.1f MB", float64(size)/megabyte)
	case size >= kilobyte:
		return fmt.Sprintf("%.1f KB", float64(size)/kilobyte)
	}
	return fmt.Sprintf("%d B", size)
}
