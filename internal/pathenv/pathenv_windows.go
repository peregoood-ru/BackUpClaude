//go:build windows

package pathenv

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/sandbox"
)

const (
	pathValue = "Path"
	// realKey holds the variables of the user. A sandbox uses a key of its own
	// that Windows never reads, so that trying the application does not change
	// the PATH of the person who tries it.
	realKey = `Environment`
)

func environmentKey() string {
	if name := sandbox.Name(); name != "" {
		return `Software\BackUpClaude-` + name + `\Environment`
	}
	return realKey
}

func expandVariables(value string) string {
	expanded, err := registry.ExpandString(value)
	if err != nil {
		return value
	}
	return expanded
}

// read gives the current value and its type; a missing value is empty.
func read(key registry.Key) (string, uint32, error) {
	value, kind, err := key.GetStringValue(pathValue)
	if errors.Is(err, registry.ErrNotExist) {
		return "", registry.EXPAND_SZ, nil
	}
	return value, kind, err
}

func openKey() (registry.Key, error) {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, environmentKey(), registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return 0, i18n.E("path.registry_failed").Hint("path.registry_failed.hint").Because(err)
	}
	return key, nil
}

// write stores the value as REG_EXPAND_SZ: entries such as %USERPROFILE%\bin
// stay as they are.
func write(key registry.Key, value string) error {
	if err := key.SetExpandStringValue(pathValue, value); err != nil {
		return i18n.E("path.registry_failed").Hint("path.registry_failed.hint").Because(err)
	}
	if sandbox.Name() == "" {
		broadcastChange()
	}
	return nil
}

// Add puts dir into the PATH of the user. It reports whether anything changed.
// Programs started afterwards see it; a terminal that is already open does not.
func Add(dir string) (bool, error) {
	key, err := openKey()
	if err != nil {
		return false, err
	}
	defer key.Close()
	current, _, err := read(key)
	if err != nil {
		return false, i18n.E("path.registry_failed").Hint("path.registry_failed.hint").Because(err)
	}
	updated, changed := add(current, dir, expandVariables)
	if !changed {
		return false, nil
	}
	return true, write(key, updated)
}

// Remove takes dir out of the PATH of the user.
func Remove(dir string) (bool, error) {
	key, err := openKey()
	if err != nil {
		return false, err
	}
	defer key.Close()
	current, _, err := read(key)
	if err != nil {
		return false, i18n.E("path.registry_failed").Hint("path.registry_failed.hint").Because(err)
	}
	updated, changed := remove(current, dir, expandVariables)
	if !changed {
		return false, nil
	}
	return true, write(key, updated)
}

// Contains reports whether dir is in the PATH of the user.
func Contains(dir string) (bool, error) {
	key, err := openKey()
	if err != nil {
		return false, err
	}
	defer key.Close()
	current, _, err := read(key)
	if err != nil {
		return false, i18n.E("path.registry_failed").Hint("path.registry_failed.hint").Because(err)
	}
	return contains(current, dir, expandVariables), nil
}

// Supported is true where the PATH can be changed without help.
func Supported() bool { return true }

// broadcastChange tells running programs, Explorer first of all, that the
// environment changed, so that a new terminal opened from it sees the folder.
func broadcastChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
		timeoutMs       = 3000
	)
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("SendMessageTimeoutW")
	name, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	// Best effort: a program that does not answer must not hold the setup up.
	_, _, _ = proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(name)), smtoAbortIfHung, timeoutMs, uintptr(unsafe.Pointer(&result)))
}
