//go:build windows

package selfinstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func waitGone(t *testing.T, path string) bool {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err != nil {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// TestRunningProgramIsRemovedAfterItEnds starts a copy of a system program, so
// that its file is in use, the way the application is while it uninstalls.
func TestRunningProgramIsRemovedAfterItEnds(t *testing.T) {
	system := os.Getenv("SystemRoot")
	if system == "" {
		t.Skip("no SystemRoot")
	}
	data, err := os.ReadFile(filepath.Join(system, "System32", "ping.exe"))
	if err != nil {
		t.Skip("no ping.exe to copy")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "running.exe")
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(target, "-n", "40", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot run the copy: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	time.Sleep(500 * time.Millisecond)

	if err := os.Remove(target); err == nil {
		t.Fatal("a running program was deleted: the test proves nothing")
	}
	leftover, err := Remove(target)
	if err != nil || leftover == "" {
		t.Fatalf("Remove: %q %v", leftover, err)
	}
	if _, err := os.Stat(target); err == nil && leftover != target {
		t.Error("the program still stands at its place")
	}
	if err := DeleteLater([]string{leftover}, []string{dir}); err != nil {
		t.Fatal(err)
	}
	// Still running: the helper keeps trying and does not remove it yet.
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(leftover); err != nil {
		t.Fatalf("the file went while it was in use: %v", err)
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	if !waitGone(t, leftover) {
		t.Fatalf("%s was not removed after the program ended", leftover)
	}
}

func TestDeleteLaterQuotesPathsWithSpacesAndCyrillic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "папка с пробелами")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "файл программы.exe")
	if err := os.WriteFile(file, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := DeleteLater([]string{file}, []string{dir}); err != nil {
		t.Fatal(err)
	}
	if !waitGone(t, file) {
		t.Fatal("the file with a Cyrillic name and spaces was not removed")
	}
	if !waitGone(t, dir) {
		t.Error("the empty folder was not removed")
	}
}
