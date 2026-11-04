package secrets

import (
	"os"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// WriteTemp writes a secret to a new file in dir that only the current user can
// read, and returns its path and a function that wipes and deletes it. The file
// exists only so that a program which takes a password from a file (restic key
// add --new-password-file) can read it; an argument or a variable would be seen
// by other processes. The rights are set before the secret is written, so it is
// never in a file others could open. Call remove as soon as the program is done.
func WriteTemp(dir, secret string) (path string, remove func(), err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, i18n.E("secrets.tempfile_failed").Because(err)
	}
	file, err := os.CreateTemp(dir, "pw-*")
	if err != nil {
		return "", nil, i18n.E("secrets.tempfile_failed").Because(err)
	}
	name := file.Name()
	remove = func() { wipe(name, len(secret)) }
	if err := restrictToUser(name); err != nil {
		_ = file.Close()
		remove()
		return "", nil, i18n.E("secrets.tempfile_failed").Because(err)
	}
	if _, err := file.WriteString(secret + "\n"); err != nil {
		_ = file.Close()
		remove()
		return "", nil, i18n.E("secrets.tempfile_failed").Because(err)
	}
	if err := file.Close(); err != nil {
		remove()
		return "", nil, i18n.E("secrets.tempfile_failed").Because(err)
	}
	return name, remove, nil
}

// wipe overwrites the file with zeros and deletes it. It does the best it can:
// the file is short-lived and in the user's own data folder.
func wipe(name string, size int) {
	if file, err := os.OpenFile(name, os.O_WRONLY, 0); err == nil {
		_, _ = file.Write(make([]byte, size+2))
		_ = file.Sync()
		_ = file.Close()
	}
	_ = os.Remove(name)
}
