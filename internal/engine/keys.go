package engine

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Key is one way into a repository: restic keeps one entry per password.
type Key struct {
	ID string `json:"id"`
	// Current marks the key the password of this run opened.
	Current  bool   `json:"current"`
	UserName string `json:"userName"`
	HostName string `json:"hostName"`
	Created  string `json:"created"`
}

// KeyList lists the keys of the repository. The password must open it.
func (r *Restic) KeyList(ctx context.Context) ([]Key, error) {
	var keys []Key
	var found bool
	err := r.run(ctx, r.repoArgs("key", "list"), func(line []byte) {
		if !strings.HasPrefix(string(line), "[") {
			return
		}
		var parsed []Key
		if json.Unmarshal(line, &parsed) == nil {
			keys, found = parsed, true
		}
	}, nil)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, i18n.E("engine.restic.bad_output")
	}
	return keys, nil
}

// KeyAdd adds a key whose password is the first line of newPasswordFile. The
// password that opens the repository comes the usual way (UseTypedPassword
// sends it on standard input), so the new one never travels in an argument or a
// variable: only the name of the file does.
func (r *Restic) KeyAdd(ctx context.Context, newPasswordFile string) error {
	return r.run(ctx, r.repoArgs("key", "add", "--new-password-file", newPasswordFile), nil, nil)
}

// KeyRemove removes the key with this id. restic refuses to remove the key the
// run itself opened, so it cannot lock the repository for good.
func (r *Restic) KeyRemove(ctx context.Context, id string) error {
	return r.run(ctx, r.repoArgs("key", "remove", id), nil, nil)
}
