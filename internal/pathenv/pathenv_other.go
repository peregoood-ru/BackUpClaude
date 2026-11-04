//go:build !windows

package pathenv

import "github.com/peregoood-ru/BackUpClaude/internal/i18n"

const stage = 9

// Add is not done automatically here: the PATH of a shell lives in its profile.
func Add(string) (bool, error) { return false, i18n.NotReady("path", stage) }

// Remove is not done automatically here.
func Remove(string) (bool, error) { return false, i18n.NotReady("path", stage) }

// Contains cannot tell here.
func Contains(string) (bool, error) { return false, i18n.NotReady("path", stage) }

// Supported is false where the PATH cannot be changed by the application.
func Supported() bool { return false }
