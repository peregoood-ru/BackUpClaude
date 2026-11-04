//go:build darwin

package ui

import "os/exec"

// Open shows the address in the default browser.
func Open(address string) error {
	cmd := exec.Command("open", address)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
