//go:build !windows

package tray

import "github.com/peregoood-ru/BackUpClaude/internal/i18n"

type noAutostart struct{}

// NewAutostart returns an autostart that does nothing: only Windows has one.
func NewAutostart() Autostart { return noAutostart{} }

func (noAutostart) Supported() bool { return false }

func (noAutostart) Enable(string) error {
	return i18n.E("tray.unsupported").Hint("tray.unsupported.hint").Exit(3)
}

func (noAutostart) Disable() error { return nil }

func (noAutostart) Enabled() (bool, error) { return false, nil }
