package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/disk"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/pathenv"
	"github.com/peregoood-ru/BackUpClaude/internal/selfinstall"
	"github.com/peregoood-ru/BackUpClaude/internal/syncdir"
	"github.com/peregoood-ru/BackUpClaude/internal/update"
)

// systemPath is the PATH of the user, as the OS keeps it.
type systemPath struct{}

func (systemPath) Add(dir string) (bool, error)      { return pathenv.Add(dir) }
func (systemPath) Remove(dir string) (bool, error)   { return pathenv.Remove(dir) }
func (systemPath) Contains(dir string) (bool, error) { return pathenv.Contains(dir) }
func (systemPath) Supported() bool                   { return pathenv.Supported() }

func (a *app) pathManager() pathManager {
	if a.deps.path != nil {
		return a.deps.path
	}
	return systemPath{}
}

func (a *app) driveList() []disk.Drive {
	if a.deps.drives != nil {
		return a.deps.drives()
	}
	return disk.Drives()
}

func (a *app) syncFolders() syncdir.Probe {
	if a.deps.syncProbe != nil {
		return a.deps.syncProbe()
	}
	return syncdir.SystemProbe()
}

func (a *app) releaseClient() *update.Client {
	token := os.Getenv(update.EnvToken)
	if a.deps.releases != nil {
		return a.deps.releases(token)
	}
	return &update.Client{Token: token}
}

// execProgram runs a program and returns what it printed.
func (a *app) execProgram(ctx context.Context, program string, args ...string) ([]byte, error) {
	if a.deps.runProgram != nil {
		return a.deps.runProgram(ctx, program, args...)
	}
	return exec.CommandContext(ctx, program, args...).CombinedOutput()
}

// programReport says where the program is and whether a terminal finds it.
type programReport struct {
	Path string `json:"path"`
	// Placed is true when this run copied the program there.
	Placed bool `json:"placed"`
	InPath bool `json:"in_path"`
	// PathChanged is true when this run added the folder to the PATH.
	PathChanged bool `json:"path_changed"`
	// PathSupported is false where the application cannot change the PATH.
	PathSupported bool `json:"path_supported"`
}

// installProgram copies the running program into the folder of the application
// and puts that folder into the PATH of the user. Nothing needs administrator
// rights. withPath false leaves the PATH alone.
func (a *app) installProgram(layout config.Layout, withPath bool) (programReport, error) {
	target := selfinstall.Target(layout.BinDir())
	report := programReport{Path: target, PathSupported: a.pathManager().Supported()}
	current, err := a.deps.executable()
	if err != nil {
		return report, i18n.E("cli.no_executable").Because(err)
	}
	if report.Placed, err = selfinstall.Place(current, target); err != nil {
		return report, err
	}
	if !withPath {
		return report, nil
	}
	manager := a.pathManager()
	if !manager.Supported() {
		return report, nil
	}
	if has, err := manager.Contains(layout.BinDir()); err != nil {
		return report, err
	} else if has {
		report.InPath = true
		return report, nil
	}
	if report.PathChanged, err = manager.Add(layout.BinDir()); err != nil {
		return report, err
	}
	report.InPath = true
	return report, nil
}

func (a *app) newInstallCommand() *cobra.Command {
	var noPath bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: a.tr.T("cli.install.short"),
		Long:  a.tr.T("cli.install.long"),
		Args:  a.noArgs,
		RunE: func(*cobra.Command, []string) error {
			layout, err := config.Resolve()
			if err != nil {
				return err
			}
			report, err := a.installProgram(layout, !noPath)
			if err != nil {
				return err
			}
			return a.emit(report, func(w io.Writer) { a.printProgram(w, report) })
		},
	}
	cmd.Flags().BoolVar(&noPath, "no-path", false, a.tr.T("flag.install.no_path"))
	return cmd
}

func (a *app) printProgram(w io.Writer, report programReport) {
	if report.Placed {
		fmt.Fprintln(w, a.tr.T("install.placed", report.Path))
	} else {
		fmt.Fprintln(w, a.tr.T("install.already", report.Path))
	}
	switch {
	case report.PathChanged:
		fmt.Fprintln(w, a.tr.T("install.path_added", filepath.Dir(report.Path)))
	case report.InPath:
		fmt.Fprintln(w, a.tr.T("install.path_present"))
	case !report.PathSupported:
		fmt.Fprintln(w, a.tr.T("install.path_manual", filepath.Dir(report.Path)))
	}
	fmt.Fprintln(w, a.tr.T("install.next"))
}
