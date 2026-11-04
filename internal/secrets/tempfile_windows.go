//go:build windows

package secrets

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
)

// restrictToUser drops every inherited right from the file and grants full
// access to the current user alone. On Windows the mode bits of os.Chmod say
// nothing about who can read a file, so the access list is set with icacls.
func restrictToUser(path string) error {
	current, err := user.Current()
	if err != nil {
		return err
	}
	icacls := filepath.Join(os.Getenv("SystemRoot"), "System32", "icacls.exe")
	// *SID is how icacls takes a security identifier, whatever the user's name.
	out, err := exec.Command(icacls, path, "/inheritance:r", "/grant:r", "*"+current.Uid+":F").CombinedOutput()
	if err != nil {
		return &icaclsError{err: err, output: string(out)}
	}
	return nil
}

type icaclsError struct {
	err    error
	output string
}

func (e *icaclsError) Error() string { return "icacls: " + e.err.Error() + ": " + e.output }
func (e *icaclsError) Unwrap() error { return e.err }
