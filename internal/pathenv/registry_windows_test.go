//go:build windows

package pathenv

import (
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/peregoood-ru/BackUpClaude/internal/sandbox"
)

// TestRegistryRoundTrip works in a sandbox key, never in the real Environment.
func TestRegistryRoundTrip(t *testing.T) {
	t.Setenv(sandbox.Env, "pathtest")
	cleanup := func() { _ = registry.DeleteKey(registry.CURRENT_USER, `Software\BackUpClaude-pathtest\Environment`) }
	cleanup()
	t.Cleanup(func() {
		cleanup()
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\BackUpClaude-pathtest`)
	})

	dir := `C:\Users\Ivan\.backupclaude\bin`
	if has, err := Contains(dir); err != nil || has {
		t.Fatalf("fresh key: %v %v", has, err)
	}
	if changed, err := Add(dir); err != nil || !changed {
		t.Fatalf("add: %v %v", changed, err)
	}
	if changed, err := Add(dir); err != nil || changed {
		t.Fatalf("second add: %v %v", changed, err)
	}
	if has, err := Contains(dir); err != nil || !has {
		t.Fatalf("contains: %v %v", has, err)
	}
	// An existing entry with a variable stays as written.
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\BackUpClaude-pathtest\Environment`, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err := key.SetExpandStringValue("Path", `%SystemRoot%\system32;`+dir); err != nil {
		t.Fatal(err)
	}
	key.Close()
	if changed, err := Remove(dir); err != nil || !changed {
		t.Fatalf("remove: %v %v", changed, err)
	}
	read, err := registry.OpenKey(registry.CURRENT_USER, `Software\BackUpClaude-pathtest\Environment`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	value, kind, err := read.GetStringValue("Path")
	if err != nil || value != `%SystemRoot%\system32` || kind != registry.EXPAND_SZ {
		t.Errorf("value %q kind %d err %v", value, kind, err)
	}
}
