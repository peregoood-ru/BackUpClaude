package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/disk"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/notify"
	"github.com/peregoood-ru/BackUpClaude/internal/profile"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/watch"
)

// Verdicts of the status, from good to bad.
const (
	verdictOK        = "ok"
	verdictAttention = "attention"
	verdictProblem   = "problem"
)

// Issue codes. Each one that can end up in the text has a locale key
// status.issue.<code>.
const (
	issueRepoUnreachable = "repository_unreachable"
	issueNoSnapshots     = "no_snapshots"
	issueStale           = "snapshot_stale"
	issueCheckErrors     = "check_errors"
	issueCheckFailed     = "check_failed"
	issueCheckOverdue    = "check_overdue"
	issueJobFailing      = "job_failing"
	issueDiskLow         = "disk_low"
	issueAlert           = "alert"
	issueOffsiteBroken   = "offsite_unreachable"
)

const (
	// staleFactor is how many intervals may pass without a snapshot before the
	// status calls the copy stuck: one missed run is normal (the computer slept).
	staleFactor = 3
	// offsiteEveryHours is how often the external copy is meant to be updated.
	offsiteEveryHours = 24
	// lowDiskBytes is the free space below which the status warns.
	lowDiskBytes = 10 * gigabyte
	// statusLockWait is how long the status waits for a repository that a cleanup
	// holds. Longer is no use to a person waiting for the answer.
	statusLockWait = 2 * time.Minute
	// offsiteWait limits everything asked of the external copy: it may be slow.
	offsiteWait = 3 * time.Minute
	// statusWindow is the look-back of the status; the weekly summary looks a week back.
	statusWindow  = 24 * time.Hour
	summaryWindow = 7 * 24 * time.Hour
	// logLookbackDays is how far back the run log is read: a weekly check must
	// still be found after a month of hourly lines.
	logLookbackDays = 60
)

// profileAge is the newest snapshot of one profile in one copy.
type profileAge struct {
	Profile    string     `json:"profile"`
	LastSnap   *time.Time `json:"last_snapshot,omitempty"`
	AgeSeconds int64      `json:"age_seconds,omitempty"`
	Stale      bool       `json:"stale,omitempty"`
}

// copyStatus is one of the two copies.
type copyStatus struct {
	// Role is "local" or "offsite".
	Role       string `json:"role"`
	Configured bool   `json:"configured"`
	Location   string `json:"location,omitempty"`
	Reachable  bool   `json:"reachable"`
	// Error says why the copy could not be read.
	Error     *i18n.Description `json:"error,omitempty"`
	Profiles  []profileAge      `json:"profiles,omitempty"`
	SizeBytes *int64            `json:"size_bytes,omitempty"`
}

// diskStatus is the free space on one disk and why it matters.
type diskStatus struct {
	Disk  string   `json:"disk"`
	Free  uint64   `json:"free"`
	Total uint64   `json:"total"`
	Roles []string `json:"roles"`
	Low   bool     `json:"low,omitempty"`
}

// checkStatus is the result of the last repository check.
type checkStatus struct {
	// State is "never", "ok", "errors" or "failed".
	State   string     `json:"state"`
	Time    *time.Time `json:"time,omitempty"`
	Errors  int        `json:"errors,omitempty"`
	Overdue bool       `json:"overdue,omitempty"`
}

// jobFailure is a job whose latest run failed.
type jobFailure struct {
	Job     string    `json:"job"`
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

type statusReport struct {
	Verdict     string        `json:"verdict"`
	Time        time.Time     `json:"time"`
	Copies      []copyStatus  `json:"copies"`
	Disks       []diskStatus  `json:"disks"`
	Check       checkStatus   `json:"check"`
	FailingJobs []jobFailure  `json:"failing_jobs"`
	FailedRuns  int           `json:"failed_runs"`
	WindowHours int           `json:"window_hours"`
	Alerts      []watch.Alert `json:"alerts"`
	Sessions    *watch.Count  `json:"sessions,omitempty"`
	Channels    []string      `json:"notify_channels"`
	Issues      []string      `json:"issues"`
}

func (a *app) newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: a.tr.T("cli.status.short"),
		Long:  a.tr.T("cli.status.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.status(cmd.Context())
		},
	}
}

func (a *app) status(ctx context.Context) error {
	current, err := a.openSession()
	if err != nil {
		return err
	}
	report := a.buildStatus(ctx, current, statusWindow)
	return a.emit(report, func(w io.Writer) {
		fmt.Fprint(w, a.renderStatus(report))
	})
}

// buildStatus gathers the facts. It does not fail: what cannot be learned is
// part of the answer, because "I cannot reach the repository" is exactly what
// the user needs to hear.
func (a *app) buildStatus(ctx context.Context, current *session, window time.Duration) *statusReport {
	now := time.Now()
	report := &statusReport{Time: now, WindowHours: int(window / time.Hour)}
	cfg := current.config

	restic := *current.restic
	restic.RetryLock = statusLockWait
	local := a.readCopy(ctx, "local", &restic, cfg.Repositories.Local, cfg, now)
	report.Copies = append(report.Copies, local)

	offsite := copyStatus{Role: "offsite", Configured: cfg.Repositories.Offsite != ""}
	if offsite.Configured {
		other := restic
		other.Repo = cfg.Repositories.Offsite
		offsiteCtx, cancel := context.WithTimeout(ctx, offsiteWait)
		offsite = a.readCopy(offsiteCtx, "offsite", &other, cfg.Repositories.Offsite, cfg, now)
		cancel()
	}
	report.Copies = append(report.Copies, offsite)

	report.Disks = a.readDisks(current)

	logs := readRunLogs(current.layout, now.AddDate(0, 0, -logLookbackDays))
	report.Check = checkOf(logs, cfg, now)
	report.FailingJobs, report.FailedRuns = failuresOf(logs, now.Add(-window))

	if state, _ := watch.Load(current.layout.WatchFile()); state != nil {
		report.Sessions = state.Transcripts
		for _, alert := range state.Alerts {
			if now.Sub(alert.Time) <= window {
				report.Alerts = append(report.Alerts, alert)
			}
		}
	}
	if notifier, _ := a.notifierFor(cfg); notifier != nil {
		for _, channel := range notifier.Channels {
			report.Channels = append(report.Channels, channel.Name())
		}
	}
	report.Issues = issuesOf(report)
	report.Verdict = verdictOf(report)
	// Empty lists, not null, in the JSON.
	if report.FailingJobs == nil {
		report.FailingJobs = []jobFailure{}
	}
	if report.Alerts == nil {
		report.Alerts = []watch.Alert{}
	}
	if report.Channels == nil {
		report.Channels = []string{}
	}
	return report
}

// locationSecret matches a password inside a repository address such as
// rest:https://user:password@host/path.
var locationSecret = regexp.MustCompile(`(://[^/@:]+):[^@/]+@`)

// safeLocation hides a password in a repository address.
func safeLocation(location string) string {
	return locationSecret.ReplaceAllString(location, "$1:***@")
}

// staleAfter is how old the newest snapshot of a profile may be.
func staleAfter(role, name string, cfg *config.Config) time.Duration {
	hours := offsiteEveryHours
	if role == "local" {
		hours = cfg.Schedule.ClaudeEveryHours
		if name == profile.Projects {
			hours = cfg.Schedule.ProjectsEveryHours
		}
	}
	return time.Duration(hours*staleFactor) * time.Hour
}

// readCopy asks one repository for its newest snapshots and its size.
func (a *app) readCopy(ctx context.Context, role string, restic *engine.Restic, location string, cfg *config.Config, now time.Time) copyStatus {
	result := copyStatus{Role: role, Configured: true, Location: safeLocation(location)}
	snapshots, err := restic.Snapshots(ctx, "")
	if err != nil {
		description := a.tr.Describe(err)
		result.Error = &description
		return result
	}
	result.Reachable = true
	for _, name := range profile.Names() {
		age := profileAge{Profile: name}
		for _, snapshot := range snapshots {
			if !hasTag(snapshot.Tags, name) {
				continue
			}
			if age.LastSnap == nil || snapshot.Time.After(*age.LastSnap) {
				moment := snapshot.Time
				age.LastSnap = &moment
			}
		}
		if age.LastSnap != nil {
			age.AgeSeconds = int64(now.Sub(*age.LastSnap).Seconds())
			age.Stale = now.Sub(*age.LastSnap) > staleAfter(role, name, cfg)
		}
		result.Profiles = append(result.Profiles, age)
	}
	if size, ok := repoSize(ctx, restic, location); ok {
		result.SizeBytes = &size
	}
	return result
}

func hasTag(tags []string, name string) bool {
	for _, tag := range tags {
		if tag == name {
			return true
		}
	}
	return false
}

// repoSize measures a repository: a folder is added up directly, which needs no
// lock and works while a backup runs; anything else is asked from restic.
func repoSize(ctx context.Context, restic *engine.Restic, location string) (int64, bool) {
	if filepath.IsAbs(location) {
		var total int64
		err := filepath.WalkDir(location, func(_ string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				if info, infoErr := entry.Info(); infoErr == nil {
					total += info.Size()
				}
			}
			return nil
		})
		return total, err == nil
	}
	stats, err := restic.Stats(ctx)
	if err != nil {
		return 0, false
	}
	return stats.Bytes, true
}

// readDisks reports the disks that hold the copy, the program's data and
// Claude's own folders, once each.
func (a *app) readDisks(current *session) []diskStatus {
	type place struct{ path, role string }
	places := []place{{current.config.Repositories.Local, "repository"}, {current.layout.Root, "data"}}
	if env, err := a.deps.env(); err == nil {
		places = append(places, place{env.Home, "data"})
	}
	var disks []diskStatus
	index := map[string]int{}
	for _, p := range places {
		probe := existingParent(p.path)
		usage, err := disk.Of(probe)
		if err != nil {
			continue
		}
		name := filepath.VolumeName(probe)
		if name == "" {
			name = probe
		}
		key := fmt.Sprintf("%s|%d", strings.ToLower(filepath.VolumeName(probe)), usage.Total)
		if i, seen := index[key]; seen {
			if !containsString(disks[i].Roles, p.role) {
				disks[i].Roles = append(disks[i].Roles, p.role)
			}
			continue
		}
		index[key] = len(disks)
		disks = append(disks, diskStatus{
			Disk: name, Free: usage.Free, Total: usage.Total, Roles: []string{p.role},
			Low: usage.Free < lowDiskBytes,
		})
	}
	return disks
}

func containsString(items []string, item string) bool {
	for _, candidate := range items {
		if candidate == item {
			return true
		}
	}
	return false
}

// existingParent walks up from path to the nearest folder that exists, so a
// repository that is not there yet still tells which disk it will be on.
func existingParent(path string) string {
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

// loggedRun is a line of the run log as the status reads it.
type loggedRun struct {
	Time   string            `json:"time"`
	Job    string            `json:"job"`
	OK     bool              `json:"ok"`
	Result json.RawMessage   `json:"result"`
	Error  *i18n.Description `json:"error"`

	at time.Time
}

// readRunLogs returns the runs since the given moment, oldest first, from the
// logs of this month and the previous ones. A damaged line is skipped: one bad
// line must not hide the rest.
func readRunLogs(layout config.Layout, since time.Time) []loggedRun {
	entries, err := os.ReadDir(layout.LogsDir())
	if err != nil {
		return nil
	}
	var runs []loggedRun
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "runs-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		file, err := os.Open(filepath.Join(layout.LogsDir(), name))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1<<20), 8<<20)
		for scanner.Scan() {
			var run loggedRun
			if json.Unmarshal(scanner.Bytes(), &run) != nil {
				continue
			}
			at, err := time.Parse(time.RFC3339, run.Time)
			if err != nil || at.Before(since) {
				continue
			}
			run.at = at
			runs = append(runs, run)
		}
		_ = file.Close()
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].at.Before(runs[j].at) })
	return runs
}

// checkOf reads the last repository check from the log.
func checkOf(runs []loggedRun, cfg *config.Config, now time.Time) checkStatus {
	status := checkStatus{State: "never"}
	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]
		if run.Job != schedule.JobCheck {
			continue
		}
		moment := run.at
		status.Time = &moment
		var result struct {
			Errors int `json:"errors"`
		}
		_ = json.Unmarshal(run.Result, &result)
		status.Errors = result.Errors
		switch {
		case result.Errors > 0:
			status.State = "errors"
		case !run.OK:
			status.State = "failed"
		default:
			status.State = "ok"
		}
		status.Overdue = now.Sub(run.at) > time.Duration(cfg.Schedule.CheckEveryHours*2)*time.Hour
		break
	}
	return status
}

// failuresOf lists the jobs whose latest run failed, and counts the failed runs
// since the given moment.
func failuresOf(runs []loggedRun, since time.Time) ([]jobFailure, int) {
	latest := map[string]loggedRun{}
	count := 0
	for _, run := range runs {
		latest[run.Job] = run
		if !run.OK && !run.at.Before(since) {
			count++
		}
	}
	var failing []jobFailure
	for _, name := range schedule.JobNames() {
		run, ok := latest[name]
		if !ok || run.OK {
			continue
		}
		message := ""
		if run.Error != nil {
			message = run.Error.Message
		}
		failing = append(failing, jobFailure{Job: name, Time: run.at, Message: message})
	}
	return failing, count
}

// issuesOf lists what is wrong, as codes.
func issuesOf(report *statusReport) []string {
	var issues []string
	add := func(code string) {
		if !containsString(issues, code) {
			issues = append(issues, code)
		}
	}
	for _, cp := range report.Copies {
		if !cp.Configured {
			continue
		}
		if !cp.Reachable {
			if cp.Role == "local" {
				add(issueRepoUnreachable)
			} else {
				add(issueOffsiteBroken)
			}
			continue
		}
		saved := false
		for _, age := range cp.Profiles {
			saved = saved || age.LastSnap != nil
			if age.Stale {
				add(issueStale)
			}
		}
		if !saved {
			add(issueNoSnapshots)
		}
	}
	switch report.Check.State {
	case "errors":
		add(issueCheckErrors)
	case "failed":
		add(issueCheckFailed)
	}
	if report.Check.Overdue {
		add(issueCheckOverdue)
	}
	if len(report.FailingJobs) > 0 {
		add(issueJobFailing)
	}
	for _, d := range report.Disks {
		if d.Low {
			add(issueDiskLow)
		}
	}
	if len(report.Alerts) > 0 {
		add(issueAlert)
	}
	return issues
}

// verdictOf turns the issues into one word. A copy that cannot be read, or a
// repository with errors, means the data may not be safe; everything else asks
// for a look. An external copy that was never set up is not an issue: it is
// said plainly in the text instead.
func verdictOf(report *statusReport) string {
	verdict := verdictOK
	for _, issue := range report.Issues {
		switch issue {
		case issueRepoUnreachable, issueCheckErrors, issueOffsiteBroken:
			return verdictProblem
		default:
			verdict = verdictAttention
		}
	}
	return verdict
}

// renderStatus writes the report for a person: no jargon, the verdict first.
func (a *app) renderStatus(report *statusReport) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	line("%s", a.tr.T("status.verdict."+report.Verdict))
	for _, issue := range report.Issues {
		line("  - %s", a.tr.T("status.issue."+issue))
	}
	line("")

	for _, c := range report.Copies {
		a.renderCopy(&b, c)
	}

	line("%s", a.tr.T("status.disks.title"))
	if len(report.Disks) == 0 {
		line("  %s", a.tr.T("status.disks.unknown"))
	}
	for _, d := range report.Disks {
		roles := make([]string, len(d.Roles))
		for i, role := range d.Roles {
			roles[i] = a.tr.T("status.disk.role." + role)
		}
		text := a.tr.T("status.disk.line", d.Disk, a.formatSize(int64(d.Free)), a.formatSize(int64(d.Total)), strings.Join(roles, ", "))
		if d.Low {
			text += " " + a.tr.T("status.disk.low")
		}
		line("  %s", text)
	}
	line("")

	line("%s", a.checkLine(report.Check))
	if report.Sessions != nil {
		line("%s", a.tr.T("status.sessions", report.Sessions.Files, a.age(report.Time.Sub(report.Sessions.Time))))
	}
	if len(report.FailingJobs) == 0 {
		line("%s", a.tr.T("status.jobs.fine"))
	}
	for _, failure := range report.FailingJobs {
		line("%s", a.tr.T("status.jobs.failing", failure.Job, a.age(report.Time.Sub(failure.Time)), failure.Message))
	}
	line("%s", a.tr.T("status.failed_runs", report.FailedRuns, report.WindowHours))
	for _, alert := range report.Alerts {
		line("%s", a.tr.T("status.alert", a.age(report.Time.Sub(alert.Time)), alert.Text))
	}
	if len(report.Channels) == 0 {
		line("%s", a.tr.T("status.notify.off"))
	} else {
		names := make([]string, len(report.Channels))
		for i, name := range report.Channels {
			names[i] = a.tr.T("status.channel." + name)
		}
		line("%s", a.tr.T("status.notify.on", strings.Join(names, ", ")))
	}
	return b.String()
}

func (a *app) renderCopy(b *strings.Builder, c copyStatus) {
	line := func(format string, args ...any) { fmt.Fprintf(b, format+"\n", args...) }
	if !c.Configured {
		line("%s", a.tr.T("status.offsite.none"))
		line("")
		return
	}
	line("%s", a.tr.T("status.copy."+c.Role, c.Location))
	if !c.Reachable {
		if c.Error != nil {
			line("  %s", a.tr.T("status.copy.unreachable", c.Error.Message))
			if c.Error.Hint != "" {
				line("  %s", c.Error.Hint)
			}
		}
		line("")
		return
	}
	for _, age := range c.Profiles {
		name := a.tr.T("status.profile." + age.Profile)
		switch {
		case age.LastSnap == nil:
			line("  %s", a.tr.T("status.profile.never", name))
		case age.Stale:
			line("  %s %s", a.tr.T("status.profile.last", name, a.age(time.Duration(age.AgeSeconds)*time.Second)), a.tr.T("status.profile.stale"))
		default:
			line("  %s", a.tr.T("status.profile.last", name, a.age(time.Duration(age.AgeSeconds)*time.Second)))
		}
	}
	if c.SizeBytes != nil {
		line("  %s", a.tr.T("status.size", a.formatSize(*c.SizeBytes)))
	}
	line("")
}

func (a *app) checkLine(check checkStatus) string {
	if check.Time == nil {
		return a.tr.T("status.check.never")
	}
	when := check.Time.Local().Format("02.01.2006 15:04")
	text := ""
	switch check.State {
	case "errors":
		text = a.tr.T("status.check.errors", when, check.Errors)
	case "failed":
		text = a.tr.T("status.check.failed", when)
	default:
		text = a.tr.T("status.check.ok", when)
	}
	if check.Overdue {
		text += " " + a.tr.T("status.check.overdue")
	}
	return text
}

// age words a duration briefly, without plurals: minutes, hours or days.
func (a *app) age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return a.tr.T("age.now")
	case d < time.Hour:
		return a.tr.T("age.minutes", int(d/time.Minute))
	case d < 48*time.Hour:
		return a.tr.T("age.hours", int(d/time.Hour))
	}
	return a.tr.T("age.days", int(d/(24*time.Hour)))
}

// formatSize shows a size in the unit a person expects, in the language of the
// interface.
func (a *app) formatSize(size int64) string {
	switch {
	case size >= 1000*gigabyte:
		return a.tr.T("size.tb", float64(size)/(1000*gigabyte))
	case size >= gigabyte:
		return a.tr.T("size.gb", float64(size)/gigabyte)
	case size >= megabyte:
		return a.tr.T("size.mb", float64(size)/megabyte)
	case size >= kilobyte:
		return a.tr.T("size.kb", float64(size)/kilobyte)
	}
	return a.tr.T("size.b", size)
}

// summaryAnswer is the result of the summary job in the run log.
type summaryAnswer struct {
	Verdict   string `json:"verdict"`
	Delivered int    `json:"delivered"`
	Channels  int    `json:"channels"`
}

// sendSummary is the weekly job: the same facts as status, sent to the user. A
// summary goes out even when everything is fine, that is its whole point; it is
// the one message that says "I am alive". Without a channel there is nobody to
// send to, which is not a failure.
func (a *app) sendSummary(ctx context.Context, current *session, notifier *notify.Notifier, record *runRecord) (any, error) {
	report := a.buildStatus(ctx, current, summaryWindow)
	answer := summaryAnswer{Verdict: report.Verdict, Channels: channelCount(notifier)}
	if notifier.Empty() {
		record.Notes = append(record.Notes, "notify: no channel is set up")
		return answer, nil
	}
	message := notify.Message{
		Title: a.tr.T("notify.summary.title."+report.Verdict, hostName()),
		Body:  a.renderStatus(report),
	}
	delivered, failures := notifier.Send(ctx, message)
	answer.Delivered = delivered
	for _, failure := range a.describeFailures(failures) {
		record.Notes = append(record.Notes, "notify "+failure.Channel+": "+failure.Error)
	}
	if delivered == 0 {
		return answer, i18n.E("notify.summary_not_delivered").Hint("notify.summary_not_delivered.hint")
	}
	return answer, nil
}

func channelCount(notifier *notify.Notifier) int {
	if notifier == nil {
		return 0
	}
	return len(notifier.Channels)
}
