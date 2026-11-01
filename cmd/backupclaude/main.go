// Command backupclaude keeps two encrypted copies of Claude's data and the
// user's projects.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"

	"golang.org/x/term"

	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
)

// Set by the release build.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	deps := dependencies{
		secrets:    func() secrets.Store { return secrets.NewKeyring() },
		executable: os.Executable,
		terminal: func() bool {
			return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
		},
		stdoutTerminal: func() bool { return term.IsTerminal(int(os.Stdout.Fd())) },
	}
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, deps)
	stop()
	os.Exit(code)
}

// dependencies are what tests replace.
type dependencies struct {
	secrets func() secrets.Store
	// executable returns the path of this program.
	executable func() (string, error)
	// terminal reports whether a person sits at stdin and stdout.
	terminal func() bool
	// stdoutTerminal reports whether stdout is shown on a screen.
	stdoutTerminal func() bool
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, deps dependencies) int {
	a := newApp(args, stdin, stdout, stderr, deps)
	root := a.newRootCommand()
	root.SetArgs(args)
	return a.finish(root.ExecuteContext(ctx))
}
