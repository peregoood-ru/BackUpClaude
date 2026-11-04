package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/notify"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
	"github.com/peregoood-ru/BackUpClaude/internal/selfinstall"
	"github.com/peregoood-ru/BackUpClaude/internal/skill"
)

type uninstallOptions struct {
	yes, purge bool
}

func (a *app) newUninstallCommand() *cobra.Command {
	var o uninstallOptions
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: a.tr.T("cli.uninstall.short"),
		Long:  a.tr.T("cli.uninstall.long"),
		Args:  a.noArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.uninstall(o)
		},
	}
	cmd.Flags().BoolVarP(&o.yes, "yes", "y", false, a.tr.T("flag.uninstall.yes"))
	cmd.Flags().BoolVar(&o.purge, "purge", false, a.tr.T("flag.uninstall.purge"))
	return cmd
}

// uninstallReport says what was removed and what was left. The copies and the
// password are always left.
type uninstallReport struct {
	TasksRemoved  []string `json:"tasks_removed"`
	SchedulerNote string   `json:"scheduler_note,omitempty"`
	SkillRemoved  bool     `json:"skill_removed"`
	PathRemoved   bool     `json:"path_removed"`
	ToolsRemoved  []string `json:"tools_removed"`
	ProgramPath   string   `json:"program_path,omitempty"`
	// ProgramRemoved is true when the program file is gone; ProgramPending is
	// true when it is running and will be deleted a few seconds after it ends.
	ProgramRemoved bool `json:"program_removed"`
	ProgramPending bool `json:"program_pending"`
	Purged         bool `json:"purged"`
	// Kept lists what stays on the disk and in the key store.
	KeptRepository string `json:"kept_repository,omitempty"`
	KeptOffsite    string `json:"kept_offsite,omitempty"`
	KeptSettings   string `json:"kept_settings,omitempty"`
	PasswordKept   bool   `json:"password_kept"`
	PasswordWhere  string `json:"password_where"`
}

func (a *app) uninstall(o uninstallOptions) error {
	interactive := !o.yes && !a.jsonOut && a.deps.terminal()
	if !o.yes && !interactive {
		return i18n.E("uninstall.needs_terminal").Hint("uninstall.needs_terminal.hint").Exit(exitUsage)
	}
	layout, err := config.Resolve()
	if err != nil {
		return err
	}
	cfg, cfgErr := config.Load(layout.ConfigFile())
	report := uninstallReport{
		TasksRemoved: []string{}, ToolsRemoved: []string{},
		PasswordKept: true, PasswordWhere: a.tr.T("uninstall.password_where"),
	}
	if cfgErr == nil {
		report.KeptRepository, report.KeptOffsite = cfg.Repositories.Local, cfg.Repositories.Offsite
	}

	if interactive {
		a.printUninstallPlan(layout, cfg, o)
		yes, err := a.newPrompter().yesNo(a.tr.T("uninstall.q.confirm"), false)
		if err != nil {
			return err
		}
		if !yes {
			return i18n.E("uninstall.cancelled")
		}
	}

	a.removeTasks(layout, &report)
	a.removeSkill(&report)
	a.removeFromPath(layout, &report)
	a.removeTools(layout, &report)
	if o.purge {
		if err := a.purge(layout, &report); err != nil {
			return err
		}
	} else if cfgErr == nil {
		report.KeptSettings = layout.Root
	}
	if err := a.removeProgram(layout, &report); err != nil {
		return err
	}

	return a.emit(report, func(w io.Writer) { a.printUninstall(w, report) })
}

func (a *app) printUninstallPlan(layout config.Layout, cfg *config.Config, o uninstallOptions) {
	w := a.stdout
	fmt.Fprintln(w, a.tr.T("uninstall.plan.title"))
	fmt.Fprintln(w, "  - "+a.tr.T("uninstall.plan.tasks"))
	fmt.Fprintln(w, "  - "+a.tr.T("uninstall.plan.program", layout.BinDir()))
	if o.purge {
		fmt.Fprintln(w, "  - "+a.tr.T("uninstall.plan.purge", layout.Root))
	} else {
		fmt.Fprintln(w, "  - "+a.tr.T("uninstall.plan.settings_kept", layout.Root))
	}
	fmt.Fprintln(w)
	if cfg != nil {
		fmt.Fprintln(w, a.tr.T("uninstall.plan.copies_kept", cfg.Repositories.Local))
	} else {
		fmt.Fprintln(w, a.tr.T("uninstall.plan.copies_kept_unknown"))
	}
	fmt.Fprintln(w, a.tr.T("uninstall.plan.password"))
	fmt.Fprintln(w)
}

// removeTasks deletes the tasks of the scheduler.
func (a *app) removeTasks(layout config.Layout, report *uninstallReport) {
	scheduler := a.scheduler(layout)
	if _, err := scheduler.List(); err != nil {
		report.SchedulerNote = a.tr.Describe(err).Message
		return
	}
	for _, name := range schedule.JobNames() {
		if err := scheduler.Remove(name); err != nil {
			report.SchedulerNote = a.tr.Describe(err).Message
			continue
		}
		report.TasksRemoved = append(report.TasksRemoved, name)
	}
}

func (a *app) removeSkill(report *uninstallReport) {
	home, err := a.claudeHome()
	if err != nil || !home.Exists {
		return
	}
	// A skill of the same name that this program did not write stays.
	removed, err := skill.Remove(home.Path)
	report.SkillRemoved = err == nil && removed
}

func (a *app) removeFromPath(layout config.Layout, report *uninstallReport) {
	manager := a.pathManager()
	if !manager.Supported() {
		return
	}
	changed, err := manager.Remove(layout.BinDir())
	report.PathRemoved = err == nil && changed
}

// toolPrefixes name the folders and files the installer of the tools creates.
var toolPrefixes = []string{"restic-", "rclone-", ".install-"}

// removeTools deletes the programs restic and rclone and the restic cache.
func (a *app) removeTools(layout config.Layout, report *uninstallReport) {
	entries, err := os.ReadDir(layout.BinDir())
	if err == nil {
		for _, entry := range entries {
			name := entry.Name()
			known := name == "tools.json" || name == "tools.json.tmp"
			for _, prefix := range toolPrefixes {
				known = known || strings.HasPrefix(name, prefix)
			}
			if !known {
				continue
			}
			if os.RemoveAll(filepath.Join(layout.BinDir(), name)) == nil {
				report.ToolsRemoved = append(report.ToolsRemoved, name)
			}
		}
	}
	_ = os.RemoveAll(layout.CacheDir())
	_ = os.RemoveAll(layout.TempDir())
}

// purge removes the settings and the state, and the secrets of the
// notification channels. The password of the repository and rclone.conf stay:
// without them the copies cannot be opened.
func (a *app) purge(layout config.Layout, report *uninstallReport) error {
	for _, path := range []string{layout.ConfigFile(), layout.ProjectsFile(), layout.WatchFile()} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return i18n.E("uninstall.remove_failed", path).Because(err)
		}
	}
	if err := os.RemoveAll(layout.LogsDir()); err != nil {
		return i18n.E("uninstall.remove_failed", layout.LogsDir()).Because(err)
	}
	store := a.deps.secrets()
	for _, key := range []string{notify.KeyTelegramToken, notify.KeyWebhookURL} {
		if err := store.Delete(key); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return err
		}
	}
	report.Purged = true
	report.KeptSettings = ""
	// The data folder goes when nothing but rclone.conf, which may hold the
	// login to an external copy, would be left in it.
	_ = os.Remove(layout.Root)
	return nil
}

// removeProgram deletes the program file from the folder of the application.
// A program outside that folder was not put there by the installer and stays.
func (a *app) removeProgram(layout config.Layout, report *uninstallReport) error {
	target := selfinstall.Target(layout.BinDir())
	report.ProgramPath = target
	if _, err := os.Stat(target); err != nil {
		_ = os.Remove(layout.BinDir())
		return nil
	}
	leftover, err := selfinstall.Remove(target)
	if err != nil {
		return err
	}
	if leftover == "" {
		report.ProgramRemoved = true
		_ = os.Remove(layout.BinDir())
		_ = os.Remove(layout.Root)
		return nil
	}
	// The running program cannot delete itself on Windows: a helper removes the
	// renamed file a few seconds after this process ends.
	report.ProgramPending = true
	return selfinstall.DeleteLater([]string{leftover}, []string{layout.BinDir(), layout.Root})
}

func (a *app) printUninstall(w io.Writer, report uninstallReport) {
	fmt.Fprintln(w, a.tr.T("uninstall.done"))
	fmt.Fprintln(w, "  - "+a.tr.T("uninstall.done.tasks", len(report.TasksRemoved)))
	if report.SchedulerNote != "" {
		fmt.Fprintln(w, "    "+report.SchedulerNote)
	}
	if report.SkillRemoved {
		fmt.Fprintln(w, "  - "+a.tr.T("uninstall.done.skill"))
	}
	if report.PathRemoved {
		fmt.Fprintln(w, "  - "+a.tr.T("uninstall.done.path"))
	}
	fmt.Fprintln(w, "  - "+a.tr.T("uninstall.done.tools", len(report.ToolsRemoved)))
	switch {
	case report.ProgramRemoved:
		fmt.Fprintln(w, "  - "+a.tr.T("uninstall.done.program", report.ProgramPath))
	case report.ProgramPending:
		fmt.Fprintln(w, "  - "+a.tr.T("uninstall.done.program_pending", report.ProgramPath))
	}
	if report.Purged {
		fmt.Fprintln(w, "  - "+a.tr.T("uninstall.done.purged"))
	} else if report.KeptSettings != "" {
		fmt.Fprintln(w, "  - "+a.tr.T("uninstall.done.settings_kept", report.KeptSettings))
	}
	fmt.Fprintln(w)
	if report.KeptRepository != "" {
		fmt.Fprintln(w, a.tr.T("uninstall.done.repository", report.KeptRepository))
	}
	if report.KeptOffsite != "" {
		fmt.Fprintln(w, a.tr.T("uninstall.done.offsite", report.KeptOffsite))
	}
	fmt.Fprintln(w, a.tr.T("uninstall.done.password", report.PasswordWhere))
}
