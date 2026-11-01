package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
)

const resticPasswordCommand = "restic-password"

func (a *app) newPasswordCommand() *cobra.Command {
	var show bool
	cmd := &cobra.Command{
		Use:   "password",
		Short: a.tr.T("cli.password.short"),
		Long:  a.tr.T("cli.password.long"),
		Args:  a.noArgs,
		RunE: func(*cobra.Command, []string) error {
			if show {
				return a.showPassword()
			}
			return a.passwordStatus()
		},
	}
	cmd.Flags().BoolVar(&show, "show", false, a.tr.T("flag.password.show"))
	return cmd
}

// passwordStatus says whether a password exists, never what it is.
func (a *app) passwordStatus() error {
	_, err := a.deps.secrets().Get(secrets.RepoPasswordKey)
	stored := err == nil
	if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}
	return a.emit(map[string]bool{"stored": stored}, func(w io.Writer) {
		if stored {
			fmt.Fprintln(w, a.tr.T("password.status.stored"))
			return
		}
		fmt.Fprintln(w, a.tr.T("password.status.missing"))
	})
}

// showPassword prints the password for copying to paper. It works only for a
// person at a terminal: scripts, logs and agents get a refusal.
func (a *app) showPassword() error {
	if a.jsonOut || !a.deps.terminal() {
		return i18n.E("password.show.terminal_only").Hint("password.show.terminal_only.hint")
	}
	password, err := a.deps.secrets().Get(secrets.RepoPasswordKey)
	if errors.Is(err, secrets.ErrNotFound) {
		return i18n.E("password.missing").Hint("password.missing.hint")
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(a.stdout, a.tr.T("password.show.warning"))
	fmt.Fprint(a.stdout, a.tr.T("password.show.prompt"), " ")
	answer, err := bufio.NewReader(a.stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return i18n.E("password.show.cancelled")
	}
	if !confirmed(answer, a.tr.T("password.show.yes")) {
		return i18n.E("password.show.cancelled")
	}
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, password)
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, a.tr.T("password.show.reminder"))
	return nil
}

func confirmed(answer, localizedYes string) bool {
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "yes" || answer == localizedYes
}

// newResticPasswordCommand is restic's --password-command. It prints only when
// started by restic on behalf of this program, and never to a screen.
func (a *app) newResticPasswordCommand() *cobra.Command {
	return &cobra.Command{
		Use:    resticPasswordCommand,
		Short:  a.tr.T("cli.restic_password.short"),
		Hidden: true,
		Args:   a.noArgs,
		RunE: func(*cobra.Command, []string) error {
			if os.Getenv(engine.EnvPasswordHelper) != "1" || a.deps.stdoutTerminal() {
				return i18n.E("password.helper.refused").Hint("password.show.terminal_only.hint")
			}
			password, err := a.deps.secrets().Get(secrets.RepoPasswordKey)
			if errors.Is(err, secrets.ErrNotFound) {
				return i18n.E("password.missing").Hint("password.missing.hint")
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(a.stdout, password)
			return err
		},
	}
}
