//go:build windows

package tray

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/sandbox"
)

const (
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	valueName = "BackUpClaude Tray"
	// firstHeadlessBuild is Windows 10 1809, which brought conhost.exe --headless.
	firstHeadlessBuild = 17763
)

type windowsAutostart struct{}

// NewAutostart returns the autostart of this OS: a value of the Run key of the
// user, which needs no administrator rights. A sandbox uses a key Windows never
// reads, so that trying the application starts nothing at logon.
func NewAutostart() Autostart { return windowsAutostart{} }

func (windowsAutostart) Supported() bool { return true }

func keyPath() string {
	if name := sandbox.Name(); name != "" {
		return `Software\BackUpClaude-` + name + `\Run`
	}
	return runKey
}

// command is the line Windows runs at logon. conhost --headless keeps the
// console window of this console program from flashing on the screen.
func command(executable string) string {
	line := `"` + executable + `" tray`
	if windows.RtlGetVersion().BuildNumber < firstHeadlessBuild {
		return line
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return line
	}
	host := filepath.Join(system, "conhost.exe")
	if _, err := os.Stat(host); err != nil {
		return line
	}
	return `"` + host + `" --headless ` + line
}

func (windowsAutostart) Enable(executable string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath(), registry.SET_VALUE)
	if err != nil {
		return i18n.E("tray.autostart_failed").Hint("tray.autostart_failed.hint").Because(err)
	}
	defer key.Close()
	if err := key.SetStringValue(valueName, command(executable)); err != nil {
		return i18n.E("tray.autostart_failed").Hint("tray.autostart_failed.hint").Because(err)
	}
	return nil
}

func (windowsAutostart) Disable() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, keyPath(), registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return i18n.E("tray.autostart_failed").Hint("tray.autostart_failed.hint").Because(err)
	}
	defer key.Close()
	if err := key.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return i18n.E("tray.autostart_failed").Hint("tray.autostart_failed.hint").Because(err)
	}
	return nil
}

func (windowsAutostart) Enabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, keyPath(), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, i18n.E("tray.autostart_failed").Hint("tray.autostart_failed.hint").Because(err)
	}
	defer key.Close()
	value, _, err := key.GetStringValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, i18n.E("tray.autostart_failed").Hint("tray.autostart_failed.hint").Because(err)
	}
	return strings.TrimSpace(value) != "", nil
}
