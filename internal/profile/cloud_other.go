//go:build !windows

package profile

import "os"

// cloudOnly reports a file whose content is not on this computer.
func cloudOnly(os.FileInfo) bool { return false }
