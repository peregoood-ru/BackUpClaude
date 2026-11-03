//go:build windows

package syncdir

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// syncRootManager lists every folder the Cloud Files API of Windows keeps in
// sync: OneDrive, and the services that use the same interface.
const syncRootManager = `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\SyncRootManager`

// systemRoots reads the registered synchronised folders. A key that cannot be
// read is skipped: the answer is then only less complete.
func systemRoots() []Match {
	var found []Match
	manager, err := registry.OpenKey(registry.LOCAL_MACHINE, syncRootManager, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer manager.Close()
	names, err := manager.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	for _, name := range names {
		service := Other
		lower := strings.ToLower(name)
		switch {
		case strings.HasPrefix(lower, "onedrive"):
			service = OneDrive
		case strings.Contains(lower, "dropbox"):
			service = Dropbox
		case strings.Contains(lower, "yandex"):
			service = YandexDisk
		case strings.Contains(lower, "google"):
			service = GoogleDrive
		}
		key, err := registry.OpenKey(manager, name+`\UserSyncRoots`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		values, err := key.ReadValueNames(-1)
		if err == nil {
			for _, value := range values {
				if root, _, err := key.GetStringValue(value); err == nil && root != "" {
					found = append(found, Match{Service: service, Root: root})
				}
			}
		}
		key.Close()
	}
	return found
}
