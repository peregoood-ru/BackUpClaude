//go:build !windows

package disk

import "golang.org/x/sys/unix"

// Of returns the usage of the disk that holds path. The path must exist.
func Of(path string) (Usage, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return Usage{}, err
	}
	block := uint64(stat.Bsize)                                                              //nolint:unconvert // the type differs between systems
	return Usage{Free: uint64(stat.Bavail) * block, Total: uint64(stat.Blocks) * block}, nil //nolint:unconvert
}
