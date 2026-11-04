//go:build windows

package sysproc

import (
	"os/exec"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("ping.exe", "-n", "30", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

func processValue(t *testing.T, handle windows.Handle, class int32) uint32 {
	t.Helper()
	var value uint32
	if err := windows.NtQueryInformationProcess(handle, class, unsafe.Pointer(&value), uint32(unsafe.Sizeof(value)), nil); err != nil {
		t.Fatalf("class %d: %v", class, err)
	}
	return value
}

// Priorities set by Lower must reach restic, which is started afterwards.
func TestLowerIsInheritedByChildren(t *testing.T) {
	if err := Lower(); err != nil {
		t.Fatalf("Lower: %v", err)
	}
	child := startSleeper(t)
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(child.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)

	class, err := windows.GetPriorityClass(handle)
	if err != nil || class != windows.BELOW_NORMAL_PRIORITY_CLASS {
		t.Errorf("CPU priority class = %#x, err=%v", class, err)
	}
	if got := processValue(t, handle, processIoPriority); got != ioPriorityLow {
		t.Errorf("disk priority = %d, want %d", got, ioPriorityLow)
	}
	if got := processValue(t, handle, processPagePriority); got != memoryPriorityVeryLow {
		t.Errorf("memory priority = %d, want %d", got, memoryPriorityVeryLow)
	}
}

func TestContainTracksChildren(t *testing.T) {
	group, err := Contain()
	if err != nil {
		t.Skipf("this process cannot join a job here: %v", err)
	}
	defer func() {
		// Close would end this test process too: leave the job to the exit.
		group.handle = 0
	}()
	startSleeper(t)
	if peak := group.PeakMemory(); peak == 0 {
		t.Error("the peak memory of the group is zero")
	}
}
