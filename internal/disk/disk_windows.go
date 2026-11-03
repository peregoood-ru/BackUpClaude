//go:build windows

package disk

import "golang.org/x/sys/windows"

// Of returns the usage of the disk that holds path. The path must exist.
func Of(path string) (Usage, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Usage{}, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(name, &free, &total, &totalFree); err != nil {
		return Usage{}, err
	}
	return Usage{Free: free, Total: total}, nil
}
