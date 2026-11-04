// Command backupclaude keeps two encrypted copies of Claude's data and the
// user's projects.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"

	"golang.org/x/term"

	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/disk"
	"github.com/peregoood-ru/BackUpClaude/internal/notify"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
	"github.com/peregoood-ru/BackUpClaude/internal/selfinstall"
	"github.com/peregoood-ru/BackUpClaude/internal/syncdir"
	"github.com/peregoood-ru/BackUpClaude/internal/sysproc"
	"github.com/peregoood-ru/BackUpClaude/internal/tray"
	"github.com/peregoood-ru/BackUpClaude/internal/update"
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
	// A file left by the last update may be removed now that its program ended.
	if self, err := os.Executable(); err == nil {
		selfinstall.CleanOld(self)
	}
	deps := dependencies{
		secrets:    func() secrets.Store { return secrets.NewKeyring() },
		executable: os.Executable,
		terminal: func() bool {
			return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
		},
		stdoutTerminal: func() bool { return term.IsTerminal(int(os.Stdout.Fd())) },
		env:            discover.CurrentEnv,
		readSecret: func() (string, error) {
			line, err := term.ReadPassword(int(os.Stdin.Fd()))
			return string(line), err
		},
		lower:   sysproc.Lower,
		contain: sysproc.Contain,
		spawn:   sysproc.StartDetached,
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
	// env gives the home folder and environment variables.
	env func() (discover.Env, error)
	// resticPath replaces the installed restic when set.
	resticPath func() string
	// scheduler replaces the scheduler of the OS when set.
	scheduler func(workDir string) schedule.Scheduler
	// lower lowers the priority of a scheduled run; contain ties the processes
	// it starts to it. Tests leave both empty: they must not change the test
	// process.
	lower   func() error
	contain func() (*sysproc.Group, error)
	// notify tunes the network side of the notification channels; tests point
	// them at a local server.
	notify notify.Options
	// readSecret reads a line from the terminal without showing it.
	readSecret func() (string, error)
	// running lists processes by name; nil means asking the OS.
	running func(names, markers []string) ([]sysproc.Process, error)
	// path changes the PATH of the user; nil means the real one.
	path pathManager
	// drives lists the local disks; nil asks the OS.
	drives func() []disk.Drive
	// syncProbe says where the sync services keep their folders; nil asks the
	// computer.
	syncProbe func() syncdir.Probe
	// releases makes the client of the release API; nil means GitHub.
	releases func(token string) *update.Client
	// openBrowser shows an address in the browser; nil means the default browser.
	openBrowser func(address string) error
	// spawn starts a program that outlives this one (the page from the icon, the
	// icon itself); nil starts nothing.
	spawn func(program string, args ...string) error
	// autostart is the start of the icon at logon; nil means the real one.
	autostart tray.Autostart
	// runProgram starts a program and returns what it printed; nil means the OS.
	runProgram func(ctx context.Context, program string, args ...string) ([]byte, error)
}

// pathManager is the PATH of the user.
type pathManager interface {
	Add(dir string) (bool, error)
	Remove(dir string) (bool, error)
	Contains(dir string) (bool, error)
	Supported() bool
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, deps dependencies) int {
	a := newApp(args, stdin, stdout, stderr, deps)
	root := a.newRootCommand()
	root.SetArgs(args)
	return a.finish(root.ExecuteContext(ctx))
}
