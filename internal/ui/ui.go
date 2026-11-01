// Package ui serves the local browser page. It arrives at stage 8.
package ui

import (
	"context"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

const stage = 8

// Serve runs the page until the context ends.
func Serve(context.Context) error {
	return i18n.NotReady("ui", stage)
}
