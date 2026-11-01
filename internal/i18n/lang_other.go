//go:build !windows

package i18n

// osLanguage returns "": on macOS and Linux the LANG family of variables,
// which Detect reads first, carries the language.
func osLanguage() string { return "" }
