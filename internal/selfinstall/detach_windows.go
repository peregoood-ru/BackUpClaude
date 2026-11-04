//go:build windows

package selfinstall

import (
	"os/exec"
	"strings"
	"syscall"
)

const (
	detachedProcess = 0x00000008
	createNoWindow  = 0x08000000
	newProcessGroup = 0x00000200
	// attempts is how many times, a second apart, the helper tries to delete a
	// file that is still in use. A program that has just ended can be held for a
	// moment by the system, a virus scanner or the indexer.
	attempts = "90"
)

// DeleteLater starts a hidden command that deletes the files as soon as nothing
// holds them any more, trying for about a minute and a half, and then removes
// the folders if they are empty. It is the only way to remove the running
// program on Windows, because the file is in use until this process ends.
func DeleteLater(files []string, folders []string) error {
	var parts []string
	for _, file := range files {
		parts = append(parts, `for /l %i in (1,1,`+attempts+`) do @if exist "`+file+`" (del /f /q "`+file+`" >nul 2>&1 & ping -n 2 127.0.0.1 >nul)`)
	}
	for _, folder := range folders {
		parts = append(parts, `rmdir "`+folder+`" >nul 2>&1`)
	}
	// /s makes cmd strip the outer quotes and keep the rest as it is, which is
	// the only reliable way to pass a line that holds quotes of its own. The
	// command line is given raw because Go would escape the inner quotes.
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CmdLine:       `cmd.exe /d /s /c "` + strings.Join(parts, " & ") + `"`,
		CreationFlags: detachedProcess | createNoWindow | newProcessGroup,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
