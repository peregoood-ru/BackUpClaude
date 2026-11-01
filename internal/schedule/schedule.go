// Package schedule puts backup runs into the scheduler of the OS, so that no
// background process stays in memory between runs. The per-OS code lives in
// windows.go, darwin.go and linux.go and arrives at stages 3 and 9.
package schedule

import "time"

// Task is one recurring run of the application.
type Task struct {
	Name  string
	Every time.Duration
	Args  []string
}

// Scheduler is implemented once per OS.
type Scheduler interface {
	Install(task Task) error
	Remove(name string) error
	List() ([]Task, error)
}
