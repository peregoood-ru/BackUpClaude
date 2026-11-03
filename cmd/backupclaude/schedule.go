package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
)

func (a *app) newScheduleCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "schedule",
		Short: a.tr.T("cli.schedule.short"),
		Long:  a.tr.T("cli.schedule.long"),
		Args:  a.noArgs,
		RunE:  a.showGroup,
	}
	group.AddCommand(
		&cobra.Command{
			Use:   "install",
			Short: a.tr.T("cli.schedule.install.short"),
			Args:  a.noArgs,
			RunE:  func(*cobra.Command, []string) error { return a.scheduleInstall() },
		},
		&cobra.Command{
			Use:   "remove",
			Short: a.tr.T("cli.schedule.remove.short"),
			Args:  a.noArgs,
			RunE:  func(*cobra.Command, []string) error { return a.scheduleRemove() },
		},
		&cobra.Command{
			Use:   "status",
			Short: a.tr.T("cli.schedule.status.short"),
			Args:  a.noArgs,
			RunE:  func(*cobra.Command, []string) error { return a.scheduleStatus() },
		},
	)
	return group
}

// scheduler returns the scheduler of this OS, working in the data folder.
func (a *app) scheduler(layout config.Layout) schedule.Scheduler {
	if a.deps.scheduler != nil {
		return a.deps.scheduler(layout.TempDir())
	}
	return schedule.New(layout.TempDir())
}

// scheduleJob is one task in the answer of the schedule commands.
type scheduleJob struct {
	Job             string   `json:"job"`
	Installed       bool     `json:"installed"`
	InSync          bool     `json:"in_sync"`
	EveryHours      float64  `json:"every_hours,omitempty"`
	WantEveryHours  float64  `json:"want_every_hours"`
	StartAt         string   `json:"start_at,omitempty"`
	Weekday         string   `json:"weekday,omitempty"`
	Command         string   `json:"command,omitempty"`
	Args            []string `json:"args,omitempty"`
	CatchUp         bool     `json:"run_missed_after_power_on"`
	Hidden          bool     `json:"hidden"`
	Priority        int      `json:"priority,omitempty"`
	OnBattery       bool     `json:"runs_on_battery"`
	TimeLimitHours  float64  `json:"time_limit_hours,omitempty"`
	Status          string   `json:"status,omitempty"`
	NextRun         string   `json:"next_run,omitempty"`
	LastRun         string   `json:"last_run,omitempty"`
	LastResult      string   `json:"last_result,omitempty"`
	LastResultWords string   `json:"last_result_words,omitempty"`
}

type scheduleReport struct {
	Jobs []scheduleJob `json:"jobs"`
}

func (a *app) loadForSchedule() (config.Layout, *config.Config, error) {
	layout, err := config.Resolve()
	if err != nil {
		return layout, nil, err
	}
	cfg, err := config.Load(layout.ConfigFile())
	return layout, cfg, err
}

func (a *app) scheduleInstall() error {
	layout, cfg, err := a.loadForSchedule()
	if err != nil {
		return err
	}
	executable, err := a.deps.executable()
	if err != nil {
		return i18n.E("cli.no_executable").Because(err)
	}
	scheduler := a.scheduler(layout)
	for _, task := range schedule.Plan(cfg, executable) {
		if err := scheduler.Install(task); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(layout.LogsDir(), 0o700); err != nil {
		return i18n.E("schedule.work_failed", layout.LogsDir()).Because(err)
	}
	return a.printSchedule(cfg, scheduler, "schedule.installed")
}

func (a *app) scheduleRemove() error {
	layout, _, err := a.loadForSchedule()
	if err != nil {
		return err
	}
	scheduler := a.scheduler(layout)
	for _, name := range schedule.JobNames() {
		if err := scheduler.Remove(name); err != nil {
			return err
		}
	}
	return a.emit(scheduleReport{Jobs: []scheduleJob{}}, func(w io.Writer) {
		fmt.Fprintln(w, a.tr.T("schedule.removed"))
	})
}

func (a *app) scheduleStatus() error {
	layout, cfg, err := a.loadForSchedule()
	if err != nil {
		return err
	}
	return a.printSchedule(cfg, a.scheduler(layout), "")
}

// printSchedule reads the tasks back from the scheduler, so what it shows is
// what the scheduler holds and not what this program meant to write.
func (a *app) printSchedule(cfg *config.Config, scheduler schedule.Scheduler, headline string) error {
	executable, err := a.deps.executable()
	if err != nil {
		return i18n.E("cli.no_executable").Because(err)
	}
	installed, err := scheduler.List()
	if err != nil {
		return err
	}
	byName := map[string]schedule.Info{}
	for _, info := range installed {
		byName[info.Name] = info
	}
	report := scheduleReport{}
	for _, want := range schedule.Plan(cfg, executable) {
		job := scheduleJob{Job: want.Name, WantEveryHours: want.Every.Hours()}
		if info, ok := byName[want.Name]; ok {
			job = a.describeTask(want, info)
		}
		report.Jobs = append(report.Jobs, job)
	}
	return a.emit(report, func(w io.Writer) {
		if headline != "" {
			fmt.Fprintln(w, a.tr.T(headline))
		}
		for _, job := range report.Jobs {
			a.printJob(w, job)
		}
	})
}

func (a *app) describeTask(want schedule.Task, info schedule.Info) scheduleJob {
	job := scheduleJob{
		Job: info.Name, Installed: true, WantEveryHours: want.Every.Hours(),
		EveryHours: info.Every.Hours(), StartAt: clock(info.At),
		Command: info.Command, Args: info.Args,
		CatchUp: info.StartWhenAvailable, Hidden: info.Hidden, Priority: info.Priority,
		OnBattery: info.OnBattery, TimeLimitHours: info.TimeLimit.Hours(),
		Status: info.Status, NextRun: info.NextRun, LastRun: info.LastRun, LastResult: info.LastResult,
	}
	if info.Every >= 7*24*time.Hour {
		job.Weekday = info.Weekday.String()
	}
	job.InSync = info.Every == want.Every && info.Enabled && info.Command == want.Command
	if info.LastResult != "" {
		job.LastResultWords = a.tr.T(lastResultKey(info.LastResult))
	}
	return job
}

// lastResultKey names the text for a result code of the Task Scheduler.
func lastResultKey(code string) string {
	switch code {
	case "0":
		return "schedule.result.ok"
	case "267011":
		return "schedule.result.never"
	case "267009":
		return "schedule.result.running"
	case "267014":
		return "schedule.result.stopped"
	case "1":
		return "schedule.result.failed"
	}
	return "schedule.result.other"
}

func clock(offset time.Duration) string {
	return fmt.Sprintf("%02d:%02d", int(offset.Hours()), int(offset.Minutes())%60)
}

func (a *app) yesNo(value bool) string {
	if value {
		return a.tr.T("schedule.yes")
	}
	return a.tr.T("schedule.no")
}

func (a *app) printJob(w io.Writer, job scheduleJob) {
	if !job.Installed {
		fmt.Fprintln(w, a.tr.T("schedule.job.missing", job.Job))
		return
	}
	every := a.tr.T("schedule.every.hours", job.EveryHours)
	if job.Weekday != "" {
		every = a.tr.T("schedule.every.weekly", job.EveryHours, a.tr.T("schedule.weekday."+strings.ToLower(job.Weekday)), job.StartAt)
	}
	fmt.Fprintln(w, a.tr.T("schedule.job.line", job.Job, every, job.Status))
	fmt.Fprintln(w, "  "+a.tr.T("schedule.job.runs", job.NextRun, job.LastRun, job.LastResultWords, job.LastResult))
	fmt.Fprintln(w, "  "+a.tr.T("schedule.job.flags", a.yesNo(job.CatchUp), a.yesNo(job.Hidden), job.Priority, a.yesNo(job.OnBattery)))
	if !job.InSync {
		fmt.Fprintln(w, "  "+a.tr.T("schedule.job.out_of_sync", job.WantEveryHours))
	}
}
