//go:build darwin

package schedule

import "github.com/peregoood-ru/BackUpClaude/internal/i18n"

const stage = 9

type launchd struct{}

// New returns the scheduler of this OS.
func New(string) Scheduler { return launchd{} }

func (launchd) Install(Task) error    { return i18n.NotReady("schedule", stage) }
func (launchd) Remove(string) error   { return i18n.NotReady("schedule", stage) }
func (launchd) List() ([]Info, error) { return nil, i18n.NotReady("schedule", stage) }
func (launchd) Run(string) error      { return i18n.NotReady("schedule", stage) }
