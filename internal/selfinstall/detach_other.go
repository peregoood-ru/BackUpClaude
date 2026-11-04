//go:build !windows

package selfinstall

import "os"

// DeleteLater removes the files at once: on this OS a running program can be
// deleted. Empty folders are removed too.
func DeleteLater(files []string, folders []string) error {
	for _, file := range files {
		_ = os.Remove(file)
	}
	for _, folder := range folders {
		_ = os.Remove(folder)
	}
	return nil
}
