//go:build !windows

package sysproc

import (
	"os/exec"
	"syscall"
)

// StartDetached starts a program that goes on when this one ends.
func StartDetached(program string, args ...string) error {
	cmd := exec.Command(program, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
