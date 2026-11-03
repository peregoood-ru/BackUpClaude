//go:build windows

package schedule

import (
	"bytes"
	"os"
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
	return &schtasks{exec: runSchtasks, userID: currentUserSID, workDir: workDir, now: time.Now, quietHost: headlessHost()}
}

// firstHeadlessBuild is Windows 10 1809, which brought the pseudo console that
// conhost.exe --headless is built on.
const firstHeadlessBuild = 17763

// headlessHost returns conhost.exe when this Windows can run a console program
// through it without a window, and an empty string when it cannot.
func headlessHost() string {
	if windows.RtlGetVersion().BuildNumber < firstHeadlessBuild {
		return ""
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return ""
	}
	host := filepath.Join(system, "conhost.exe")
	if _, err := os.Stat(host); err != nil {
		return ""
	}
	return host
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
