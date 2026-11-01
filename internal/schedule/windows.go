//go:build windows

package schedule

import "github.com/peregoood-ru/BackUpClaude/internal/i18n"

const stage = 3

type taskScheduler struct{}

// New returns the scheduler of this OS.
func New() Scheduler { return taskScheduler{} }

func (taskScheduler) Install(Task) error    { return i18n.NotReady("schedule", stage) }
func (taskScheduler) Remove(string) error   { return i18n.NotReady("schedule", stage) }
func (taskScheduler) List() ([]Task, error) { return nil, i18n.NotReady("schedule", stage) }
