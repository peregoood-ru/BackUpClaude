package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
)

func (a *app) newPasswordChangeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "change",
		Short: a.tr.T("cli.password.change.short"),
		Long:  a.tr.T("cli.password.change.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.changePassword(cmd.Context())
		},
	}
}

// changeTarget is one repository whose password changes: the local copy, and the
// external one when it is set up.
type changeTarget struct {
	label  string
	restic engine.Restic
	// hasNew is true when the new password already opens the repository.
	hasNew bool
	// added is true when this run may have added the new key.
	added bool
}

// with returns restic set to open the repository with the given password, which
// reaches it on its standard input.
func (t *changeTarget) with(password string) *engine.Restic {
	r := t.restic
	r.UseTypedPassword(password)
	return &r
}

// opens says whether password opens the repository. A wrong password is an
// answer; a repository that cannot be reached is an error.
func (t *changeTarget) opens(ctx context.Context, password string) (bool, error) {
	err := t.with(password).Probe(ctx)
	switch {
	case err == nil:
		return true, nil
	case engine.IsKind(err, engine.KindWrongPassword):
		return false, nil
	default:
		return false, i18n.E("password.change.unreachable", t.label).Hint("password.change.unreachable.hint").Because(err)
	}
}

// changeState is what the key store says about a change that is under way.
type changeState struct {
	oldPassword, newPassword string
	// resuming is true when an earlier change stopped halfway.
	resuming bool
}

// optionalSecret reads an entry that may be missing.
func optionalSecret(store secrets.Store, key string) (string, error) {
	value, err := store.Get(key)
	if errors.Is(err, secrets.ErrNotFound) {
		return "", nil
	}
	return value, err
}

// loadChangeState decides which passwords are the old and the new one. A change
// that stopped halfway left its two passwords in the key store, and it is
// finished with exactly those: the new password was never shown to anybody yet.
func loadChangeState(store secrets.Store) (changeState, error) {
	active, err := store.Get(secrets.RepoPasswordKey)
	if errors.Is(err, secrets.ErrNotFound) {
		return changeState{}, i18n.E("password.missing").Hint("password.missing.hint")
	}
	if err != nil {
		return changeState{}, err
	}
	previous, err := optionalSecret(store, secrets.PreviousPasswordKey)
	if err != nil {
		return changeState{}, err
	}
	next, err := optionalSecret(store, secrets.NextPasswordKey)
	if err != nil {
		return changeState{}, err
	}
	state := changeState{oldPassword: active, resuming: previous != "" || next != ""}
	switch {
	case next != "":
		state.newPassword = next
		if previous != "" {
			state.oldPassword = previous
		}
	case previous != "":
		state.newPassword, state.oldPassword = active, previous
	}
	return state, nil
}

// setChecked stores a secret and reads it back.
func setChecked(store secrets.Store, key, value string) error {
	if err := store.Set(key, value); err != nil {
		return err
	}
	stored, err := store.Get(key)
	if err != nil {
		return err
	}
	if stored != value {
		return i18n.E("secrets.verify_failed").Hint("secrets.unavailable.hint")
	}
	return nil
}

func (a *app) changeTargets(current *session) ([]*changeTarget, error) {
	local := &changeTarget{label: a.tr.T("password.role.local"), restic: *current.restic}
	local.restic.RetryLock = 0
	targets := []*changeTarget{local}
	if offsite := current.config.Repositories.Offsite; offsite != "" {
		external := &changeTarget{label: a.tr.T("password.role.offsite"), restic: *current.restic}
		external.restic.Repo, external.restic.RetryLock = offsite, 0
		if err := a.useRclone(current.layout, &external.restic); err != nil {
			return nil, err
		}
		targets = append(targets, external)
	}
	return targets, nil
}

// changePassword replaces the repository password in every copy without ever
// leaving a copy without a working password. The order is the safety:
//
//  1. Every copy is reached and opens with the old password. If the external
//     copy is not reachable the command stops here and nothing has changed.
//  2. The new password is added to the copies as one more key and each copy is
//     opened with it. The old key still works.
//  3. The old password goes to the key store under another name, then the entry
//     of the repository is replaced. Scheduled tasks read that entry.
//  4. The copies are opened the way a scheduled task opens them, then the old
//     key is removed from each of them.
//  5. Only then the saved old password is deleted.
//
// A stop after step 2 leaves both passwords valid and both stored, and running
// the command again finishes the change with the same two passwords.
func (a *app) changePassword(ctx context.Context) error {
	if a.jsonOut || !a.deps.terminal() {
		return i18n.E("password.change.terminal_only").Hint("password.change.terminal_only.hint")
	}
	store := a.deps.secrets()
	state, err := loadChangeState(store)
	if err != nil {
		return err
	}
	current, err := a.openSession()
	if err != nil {
		return err
	}
	targets, err := a.changeTargets(current)
	if err != nil {
		return err
	}

	out := a.stdout
	fmt.Fprintln(out, a.tr.T("password.change.warning"))
	if state.resuming {
		fmt.Fprintln(out, a.tr.T("password.change.resuming"))
	}
	fmt.Fprint(out, a.tr.T("password.change.prompt"), " ")
	answer, err := a.input().ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return i18n.E("password.change.cancelled")
	}
	if !confirmed(answer, a.tr.T("password.change.yes")) {
		return i18n.E("password.change.cancelled")
	}

	fresh := !state.resuming
	if fresh {
		if state.newPassword, err = secrets.GeneratePassword(); err != nil {
			return err
		}
		if err := setChecked(store, secrets.NextPasswordKey, state.newPassword); err != nil {
			return err
		}
	}
	// Until a key is added, giving up leaves no trace.
	abandon := func() {
		if fresh {
			_ = store.Delete(secrets.NextPasswordKey)
		}
	}

	// Step 1: reach every copy, with nothing changed yet.
	for _, target := range targets {
		fmt.Fprintln(out, a.tr.T("password.change.checking", target.label))
		if state.resuming {
			if target.hasNew, err = target.opens(ctx, state.newPassword); err != nil {
				return err
			}
		}
		if target.hasNew {
			continue
		}
		ok, err := target.opens(ctx, state.oldPassword)
		if err != nil {
			abandon()
			return err
		}
		if !ok {
			abandon()
			return i18n.E("password.change.old_rejected", target.label).Hint("password.change.old_rejected.hint")
		}
	}

	// Step 2: add the new key to each copy and open the copy with it.
	for _, target := range targets {
		if target.hasNew {
			fmt.Fprintln(out, a.tr.T("password.change.already_new", target.label))
			continue
		}
		if err := a.addNewKey(ctx, current, target, state); err != nil {
			a.rollBackKeys(ctx, targets, state, fresh)
			if fresh && allRolledBack(targets) {
				abandon()
				return err
			}
			return i18n.E("password.change.rollback_failed").Hint("password.change.rollback_failed.hint").Because(err)
		}
		fmt.Fprintln(out, a.tr.T("password.change.added", target.label))
	}

	// Step 3: keep the old password, then replace the entry of the repository.
	if state.oldPassword != state.newPassword {
		if err := setChecked(store, secrets.PreviousPasswordKey, state.oldPassword); err != nil {
			return a.unfinished(err)
		}
	}
	if err := setChecked(store, secrets.RepoPasswordKey, state.newPassword); err != nil {
		return a.unfinished(err)
	}
	fmt.Fprintln(out, a.tr.T("password.change.replaced"))

	// Step 4: open the copies the way a scheduled task does, then remove the old key.
	for _, target := range targets {
		if err := target.restic.Probe(ctx); err != nil {
			return a.unfinished(i18n.E("password.change.task_check_failed", target.label).Hint("password.change.task_check_failed.hint").Because(err))
		}
	}
	if state.oldPassword != state.newPassword {
		for _, target := range targets {
			if err := a.removeOldKey(ctx, target, state); err != nil {
				return a.unfinished(err)
			}
		}
	}

	// Step 5: the old password is no longer needed.
	for _, key := range []string{secrets.NextPasswordKey, secrets.PreviousPasswordKey} {
		if err := store.Delete(key); err != nil && !errors.Is(err, secrets.ErrNotFound) {
			fmt.Fprintln(a.stderr, a.tr.T("password.change.cleanup_failed", key))
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, a.tr.T("password.change.done"))
	fmt.Fprintln(out)
	fmt.Fprintln(out, a.tr.T("password.show.warning"))
	fmt.Fprintln(out)
	fmt.Fprintln(out, state.newPassword)
	fmt.Fprintln(out)
	fmt.Fprintln(out, a.tr.T("password.show.reminder"))
	return nil
}

// unfinished wraps a failure after the new key exists: nothing is lost, and the
// same command finishes the change.
func (a *app) unfinished(err error) error {
	return i18n.E("password.change.unfinished").Hint("password.change.unfinished.hint").Because(err)
}

// addNewKey adds the new password to the repository as one more key. The
// password goes through a file only the current user can read, which is deleted
// at once; the password that opens the repository goes through standard input.
func (a *app) addNewKey(ctx context.Context, current *session, target *changeTarget, state changeState) error {
	path, remove, err := secrets.WriteTemp(current.layout.TempDir(), state.newPassword)
	if err != nil {
		return err
	}
	target.added = true // a key may exist even when restic reports a failure
	err = target.with(state.oldPassword).KeyAdd(ctx, path)
	remove()
	if err != nil {
		return err
	}
	ok, err := target.opens(ctx, state.newPassword)
	if err != nil {
		return err
	}
	if !ok {
		return i18n.E("password.change.new_rejected", target.label).Hint("password.change.new_rejected.hint")
	}
	target.hasNew = true
	return nil
}

// rollBackKeys removes the keys this run added, using the old password, when
// the change stops before the key store is touched. A run that finishes an
// earlier one never rolls back: the new password is already stored.
func (a *app) rollBackKeys(ctx context.Context, targets []*changeTarget, state changeState, fresh bool) {
	if !fresh {
		return
	}
	for _, target := range targets {
		if !target.added {
			continue
		}
		keys, err := target.with(state.newPassword).KeyList(ctx)
		if engine.IsKind(err, engine.KindWrongPassword) {
			target.added = false // the key was never saved
			continue
		}
		if err != nil {
			continue
		}
		for _, key := range keys {
			if key.Current && target.with(state.oldPassword).KeyRemove(ctx, key.ID) == nil {
				target.added = false
			}
		}
	}
}

func allRolledBack(targets []*changeTarget) bool {
	for _, target := range targets {
		if target.added {
			return false
		}
	}
	return true
}

// removeOldKey removes the key of the old password from one copy, using the new
// password, and checks that the copy lists no such key and no longer opens with
// the old password.
func (a *app) removeOldKey(ctx context.Context, target *changeTarget, state changeState) error {
	keys, err := target.with(state.oldPassword).KeyList(ctx)
	if engine.IsKind(err, engine.KindWrongPassword) {
		fmt.Fprintln(a.stdout, a.tr.T("password.change.old_gone", target.label))
		return nil
	}
	if err != nil {
		return err
	}
	var oldID string
	for _, key := range keys {
		if key.Current {
			oldID = key.ID
		}
	}
	if oldID == "" {
		return i18n.E("password.change.old_key_unknown", target.label).Hint("password.change.unfinished.hint")
	}
	if err := target.with(state.newPassword).KeyRemove(ctx, oldID); err != nil {
		return i18n.E("password.change.remove_failed", target.label).Hint("password.change.unfinished.hint").Because(err)
	}
	left, err := target.with(state.newPassword).KeyList(ctx)
	if err != nil {
		return err
	}
	for _, key := range left {
		if key.ID == oldID {
			return i18n.E("password.change.remove_failed", target.label).Hint("password.change.unfinished.hint")
		}
	}
	stillOpens, err := target.opens(ctx, state.oldPassword)
	if err != nil {
		return err
	}
	if stillOpens {
		return i18n.E("password.change.remove_failed", target.label).Hint("password.change.unfinished.hint")
	}
	fmt.Fprintln(a.stdout, a.tr.T("password.change.removed", target.label, len(left)))
	return nil
}
