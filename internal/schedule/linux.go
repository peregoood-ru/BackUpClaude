//go:build linux

package schedule

import "github.com/peregoood-ru/BackUpClaude/internal/i18n"

const stage = 9

type systemdTimers struct{}

// New returns the scheduler of this OS.
func New() Scheduler { return systemdTimers{} }

func (systemdTimers) Install(Task) error    { return i18n.NotReady("schedule", stage) }
func (systemdTimers) Remove(string) error   { return i18n.NotReady("schedule", stage) }
func (systemdTimers) List() ([]Task, error) { return nil, i18n.NotReady("schedule", stage) }
