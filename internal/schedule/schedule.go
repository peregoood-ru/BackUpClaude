// Package schedule puts backup runs into the scheduler of the OS, so that no
// background process stays in memory between runs. The per-OS code lives in
// windows.go (Task Scheduler), and darwin.go and linux.go arrive at stage 9.
package schedule

import (
	"regexp"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Task is one recurring run of the application.
type Task struct {
	// Name is the job name; it is also the name of the task in the scheduler.
	Name string
	// Every is the interval between runs. A whole number of weeks gives a weekly
	// task on Weekday, a whole number of days a daily one, and anything else a
	// task that repeats from At.
	Every time.Duration
	// At is the time of day of the first run, as an offset from midnight in
	// local time. Tasks differ in it so that they do not start together.
	At time.Duration
	// Weekday is the day of a weekly task.
	Weekday time.Weekday
	// Command is the full path of the program; Args are its arguments.
	Command string
	Args    []string
	// TimeLimit stops a run that hangs, so that it does not block the next one.
	TimeLimit time.Duration
}

// Info is a task as the scheduler has it.
type Info struct {
	Task
	// Enabled is false for a task someone switched off.
	Enabled bool
	// StartWhenAvailable means a run missed while the computer was off starts
	// as soon as it is on again.
	StartWhenAvailable bool
	// Hidden means the run shows no window.
	Hidden bool
	// Priority is the scheduler's own scale, 0 to 10; 7 and 8 are below normal.
	Priority int
	// OnBattery is true when the task also starts on battery.
	OnBattery bool
	// Status, NextRun, LastRun and LastResult are what the scheduler reports, as
	// it words them: the formats depend on the language of the OS.
	Status     string
	NextRun    string
	LastRun    string
	LastResult string
}

// Scheduler is implemented once per OS.
type Scheduler interface {
	// Install creates the task or replaces the one with the same name.
	Install(task Task) error
	// Remove deletes the task; a task that does not exist is not an error.
	Remove(name string) error
	// List returns the application's tasks that exist.
	List() ([]Info, error)
	// Run starts a task now, outside its schedule.
	Run(name string) error
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

func checkName(name string) error {
	if !validName.MatchString(name) {
		return i18n.E("schedule.bad_name", name)
	}
	return nil
}
