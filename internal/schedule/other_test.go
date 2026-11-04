//go:build !windows

package schedule

import "testing"

func TestSchedulerIsNotReadyOutsideWindows(t *testing.T) {
	scheduler := New(t.TempDir())
	if err := scheduler.Install(Task{Name: "claude"}); err == nil {
		t.Error("Install should say it is not ready yet")
	}
	if err := scheduler.Remove("claude"); err == nil {
		t.Error("Remove should say it is not ready yet")
	}
	if _, err := scheduler.List(); err == nil {
		t.Error("List should say it is not ready yet")
	}
	if err := scheduler.Run("claude"); err == nil {
		t.Error("Run should say it is not ready yet")
	}
}
