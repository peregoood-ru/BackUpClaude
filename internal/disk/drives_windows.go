//go:build windows

package disk

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// Drives lists the fixed local disks (not removable, network or optical ones)
// with their free space.
func Drives() []Drive {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	system := strings.ToUpper(strings.TrimSuffix(os.Getenv("SystemDrive"), `\`))
	var drives []Drive
	for letter := 'A'; letter <= 'Z'; letter++ {
		if mask&(1<<(letter-'A')) == 0 {
			continue
		}
		root := string(letter) + `:\`
		name, err := windows.UTF16PtrFromString(root)
		if err != nil || windows.GetDriveType(name) != windows.DRIVE_FIXED {
			continue
		}
		usage, err := Of(root)
		if err != nil {
			continue
		}
		drives = append(drives, Drive{
			Root: root, Free: usage.Free, Total: usage.Total,
			System: strings.EqualFold(string(letter)+":", system),
		})
	}
	return drives
}
