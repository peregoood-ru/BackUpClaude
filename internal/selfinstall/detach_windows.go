//go:build windows

package selfinstall

import (
	"os/exec"
	"strings"
	"syscall"
)

const (
	detachedProcess   = 0x00000008
	createNoWindow    = 0x08000000
	newProcessGroup   = 0x00000200
	delayBeforeDelete = "4"
)

// DeleteLater starts a hidden command that waits a few seconds, until this
// program has ended, and then deletes the files. Folders that are left empty
// are removed too. It is the only way to remove the running program on Windows.
func DeleteLater(files []string, folders []string) error {
	var parts []string
	parts = append(parts, "ping -n "+delayBeforeDelete+" 127.0.0.1 >nul")
	for _, file := range files {
		parts = append(parts, `del /f /q "`+file+`"`)
	}
	for _, folder := range folders {
		parts = append(parts, `rmdir "`+folder+`"`)
	}
	cmd := exec.Command("cmd.exe", "/d", "/c", strings.Join(parts, " & "))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | createNoWindow | newProcessGroup,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
