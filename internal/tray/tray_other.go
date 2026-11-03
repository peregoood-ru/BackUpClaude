//go:build !windows

package tray

import (
	"context"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Supported reports whether this OS can show the icon. Only Windows can yet.
func Supported() bool { return false }

// Run is not available here.
func Run(context.Context, Options) error {
	return i18n.E("tray.unsupported").Hint("tray.unsupported.hint").Exit(3)
}
