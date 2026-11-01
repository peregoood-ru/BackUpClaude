//go:build windows

package schedule

import (
	"bytes"
	"os/exec"
	"os/user"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// New returns the scheduler of this OS. workDir is a folder that scheduler tools
// can read, for the definition of a task being installed.
func New(workDir string) Scheduler {
	return &schtasks{exec: runSchtasks, userID: currentUserSID, workDir: workDir, now: time.Now}
}

// currentUserSID returns the security identifier of the user: it works with
// names in any language, which a "COMPUTER\name" string does not always do.
func currentUserSID() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", err
	}
	return current.Uid, nil
}

func runSchtasks(args ...string) ([]byte, error) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(filepath.Join(system, "schtasks.exe"), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err = cmd.Run()
	return []byte(oemToUTF8(output.Bytes())), err
}

// cpOEM is the code page of console programs.
const cpOEM = 1

// oemToUTF8 converts what a console program wrote: schtasks answers in the OEM
// code page, which is not UTF-8 on any Windows with national letters.
func oemToUTF8(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	size, err := windows.MultiByteToWideChar(cpOEM, 0, &data[0], int32(len(data)), nil, 0)
	if err != nil || size == 0 {
		return string(data)
	}
	wide := make([]uint16, size)
	if _, err := windows.MultiByteToWideChar(cpOEM, 0, &data[0], int32(len(data)), &wide[0], size); err != nil {
		return string(data)
	}
	return windows.UTF16ToString(wide)
}
