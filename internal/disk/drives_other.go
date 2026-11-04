//go:build !windows

package disk

// Drives has no list of lettered disks to give here.
func Drives() []Drive { return nil }
