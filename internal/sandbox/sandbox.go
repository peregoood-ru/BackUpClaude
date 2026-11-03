// Package sandbox lets the application run next to a working installation
// without touching it. BACKUPCLAUDE_SANDBOX=name renames the three things that
// live outside the data folder: the folder of tasks in the scheduler, the group
// of entries in the key store and the key of the user's environment variables
// in the registry. Tests and the end-to-end script use it, and so can anyone
// who wants to try the wizard on a computer that already has working copies.
package sandbox

import (
	"os"
	"regexp"
	"strings"
)

// Env is the variable that switches the sandbox on.
const Env = "BACKUPCLAUDE_SANDBOX"

var valid = regexp.MustCompile(`^[A-Za-z0-9]{1,16}$`)

// Name returns the sandbox name, or "" when the application works for real. A
// value with anything but letters and digits is ignored: it ends up in task and
// registry names.
func Name() string {
	value := strings.TrimSpace(os.Getenv(Env))
	if !valid.MatchString(value) {
		return ""
	}
	return value
}

// Suffix returns "" for a real installation and "-name" inside a sandbox.
func Suffix() string {
	if name := Name(); name != "" {
		return "-" + name
	}
	return ""
}
