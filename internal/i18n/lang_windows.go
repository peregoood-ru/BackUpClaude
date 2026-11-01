//go:build windows

package i18n

import (
	"syscall"
	"unsafe"
)

var procGetUserDefaultLocaleName = syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserDefaultLocaleName")

// osLanguage returns the Windows user locale, for example "ru-RU".
func osLanguage() string {
	const localeNameMaxLength = 85
	buf := make([]uint16, localeNameMaxLength)
	n, _, _ := procGetUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
