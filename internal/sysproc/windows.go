//go:build windows

package sysproc

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Information classes of NtSetInformationProcess.
const (
	processIoPriority   = 33
	processPagePriority = 39
)

// Priorities: IoPriorityLow and MEMORY_PRIORITY_VERY_LOW. Both are allowed
// without special rights, unlike raising them.
const (
	ioPriorityLow         = 1
	memoryPriorityVeryLow = 1
)

// Lower makes this program below-normal for the CPU, low for the disk and very
// low for the memory cache. Programs started afterwards inherit all three, so
// restic works in the background without slowing down Claude. Every step is
// tried even if one fails; the first error is returned.
func Lower() error {
	self := windows.CurrentProcess()
	errs := []error{windows.SetPriorityClass(self, windows.BELOW_NORMAL_PRIORITY_CLASS)}
	for _, item := range []struct {
		class int32
		value uint32
	}{{processIoPriority, ioPriorityLow}, {processPagePriority, memoryPriorityVeryLow}} {
		value := item.value
		errs = append(errs, windows.NtSetInformationProcess(self, item.class, unsafe.Pointer(&value), uint32(unsafe.Sizeof(value))))
	}
	return errors.Join(errs...)
}

// Contain puts this program into a job object that kills every member when the
// last handle closes. A killed or crashed program then takes its restic with
// it, and the next run does not meet a half-dead child. Call Close only when
// the run is over: it ends every process in the group, this one included.
func Contain() (*Group, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return &Group{handle: uintptr(job)}, nil
}

func (g *Group) peak() uint64 {
	if g.handle == 0 {
		return 0
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(windows.Handle(g.handle), windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return 0
	}
	return uint64(info.PeakJobMemoryUsed)
}

// Close releases the job, which ends every process in it.
func (g *Group) Close() {
	if g != nil && g.handle != 0 {
		_ = windows.CloseHandle(windows.Handle(g.handle))
		g.handle = 0
	}
}
