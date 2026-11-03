//go:build windows

package tray

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/peregoood-ru/BackUpClaude/internal/sandbox"
)

// TestAutostartRoundTrip works in a sandbox key that Windows never reads at
// logon, never in the real Run key.
func TestAutostartRoundTrip(t *testing.T) {
	t.Setenv(sandbox.Env, "traytest")
	if keyPath() == runKey || !strings.Contains(keyPath(), "traytest") {
		t.Fatalf("the sandbox would touch the real key: %s", keyPath())
	}
	cleanup := func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\BackUpClaude-traytest\Run`)
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\BackUpClaude-traytest`)
	}
	cleanup()
	t.Cleanup(cleanup)

	autostart := NewAutostart()
	if !autostart.Supported() {
		t.Fatal("Windows must support it")
	}
	if on, err := autostart.Enabled(); err != nil || on {
		t.Fatalf("fresh key: %v %v", on, err)
	}
	if err := autostart.Disable(); err != nil {
		t.Fatalf("removing what is not there: %v", err)
	}
	exe := `D:\Программы и данные\backupclaude.exe`
	if err := autostart.Enable(exe); err != nil {
		t.Fatal(err)
	}
	if on, err := autostart.Enabled(); err != nil || !on {
		t.Fatalf("after enable: %v %v", on, err)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, keyPath(), registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := key.GetStringValue(valueName)
	key.Close()
	if err != nil || !strings.Contains(value, `"`+exe+`" tray`) {
		t.Fatalf("command: %q %v", value, err)
	}
	if err := autostart.Disable(); err != nil {
		t.Fatal(err)
	}
	if on, err := autostart.Enabled(); err != nil || on {
		t.Fatalf("after disable: %v %v", on, err)
	}
}
