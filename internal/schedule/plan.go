package schedule

import (
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
)

// Job names. They are the values of `run --job` and the names of the tasks.
const (
	JobClaude   = "claude"
	JobProjects = "projects"
	JobCleanup  = "cleanup"
	JobCheck    = "check"
	// JobSummary sends the weekly summary of the state.
	JobSummary = "summary"
	// JobRestoreTest proves every four weeks that the copies can be restored.
	JobRestoreTest = "restore-test"
)

// JobNames lists the jobs in the order they are shown.
func JobNames() []string {
	return []string{JobClaude, JobProjects, JobCleanup, JobCheck, JobRestoreTest, JobSummary}
}

// Plan turns the settings into the tasks to install. executable is the full
// path of the program the tasks run.
func Plan(cfg *config.Config, executable string) []Task {
	hours := func(n int) time.Duration { return time.Duration(n) * time.Hour }
	at := func(h, m int) time.Duration { return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute }
	job := func(name string, every, start, limit time.Duration) Task {
		return Task{
			Name: name, Every: every, At: start, Weekday: time.Sunday,
			Command: executable, Args: []string{"run", "--job", name}, TimeLimit: limit,
		}
	}
	return []Task{
		// Minute 7 and not the full hour: the full hour is when other programs wake up.
		job(JobClaude, hours(cfg.Schedule.ClaudeEveryHours), at(0, 7), 2*time.Hour),
		job(JobProjects, hours(cfg.Schedule.ProjectsEveryHours), at(3, 30), 12*time.Hour),
		job(JobCleanup, hours(cfg.Schedule.CleanupEveryHours), at(4, 0), 12*time.Hour),
		job(JobCheck, hours(cfg.Schedule.CheckEveryHours), at(7, 0), 6*time.Hour),
		// After the check and before the summary: the summary reports the result.
		job(JobRestoreTest, hours(cfg.Schedule.RestoreTestEveryHours), at(8, 0), 6*time.Hour),
		// After the weekly check, so that the summary can report its result.
		job(JobSummary, hours(cfg.Schedule.SummaryEveryHours), at(9, 0), time.Hour),
	}
}
