package main

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/sandbox"
	"github.com/peregoood-ru/BackUpClaude/internal/sysproc"
	"github.com/peregoood-ru/BackUpClaude/internal/tray"
)

func (a *app) newTrayCommand() *cobra.Command {
	var enable, disable bool
	cmd := &cobra.Command{
		Use:   "tray",
		Short: a.tr.T("cli.tray.short"),
		Long:  a.tr.T("cli.tray.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch {
			case enable && disable:
				return i18n.E("tray.one_of").Hint("tray.one_of.hint").Exit(exitUsage)
			case enable:
				return a.trayAutostart(true)
			case disable:
				return a.trayAutostart(false)
			}
			return a.runTray(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&enable, "enable", false, a.tr.T("flag.tray.enable"))
	cmd.Flags().BoolVar(&disable, "disable", false, a.tr.T("flag.tray.disable"))
	return cmd
}

func (a *app) autostart() tray.Autostart {
	if a.deps.autostart != nil {
		return a.deps.autostart
	}
	return tray.NewAutostart()
}

// trayAutostart switches the start of the icon at logon on or off, and records
// it in the settings when there are any. The icon itself keeps running until it
// is closed from its menu.
func (a *app) trayAutostart(on bool) error {
	autostart := a.autostart()
	if !autostart.Supported() {
		return i18n.E("tray.unsupported").Hint("tray.unsupported.hint").Exit(3)
	}
	layout, err := config.Resolve()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil && !config.IsMissing(err) {
		return err
	}
	if on {
		executable, err := a.deps.executable()
		if err != nil {
			return i18n.E("cli.no_executable").Because(err)
		}
		if err := autostart.Enable(executable); err != nil {
			return err
		}
	} else if err := autostart.Disable(); err != nil {
		return err
	}
	if cfg != nil && cfg.Tray.Enabled != on {
		cfg.Tray.Enabled = on
		if err := config.Save(layout.ConfigFile(), cfg); err != nil {
			return err
		}
	}
	return a.emit(map[string]bool{"autostart": on}, func(w io.Writer) {
		if on {
			fmt.Fprintln(w, a.tr.T("tray.autostart_on"))
			return
		}
		fmt.Fprintln(w, a.tr.T("tray.autostart_off"))
	})
}

// runTray shows the icon until it is closed. Its colour is the verdict of the
// same status the command and the page show.
func (a *app) runTray(ctx context.Context) error {
	if !tray.Supported() {
		return i18n.E("tray.unsupported").Hint("tray.unsupported.hint").Exit(3)
	}
	executable, err := a.deps.executable()
	if err != nil {
		return i18n.E("cli.no_executable").Because(err)
	}
	var lastOpen time.Time
	var mu sync.Mutex
	opts := tray.Options{
		Source:   a.trayState,
		Interval: tray.DefaultInterval,
		Instance: sandbox.Suffix(),
		Labels: tray.Labels{
			Title: a.tr.T("tray.title"), Open: a.tr.T("tray.menu.open"), Refresh: a.tr.T("tray.menu.refresh"),
			Quit: a.tr.T("tray.menu.quit"), Checking: a.tr.T("tray.checking"),
		},
		Open: func() error {
			// A double click must not start two pages.
			mu.Lock()
			defer mu.Unlock()
			if time.Since(lastOpen) < 4*time.Second {
				return nil
			}
			lastOpen = time.Now()
			spawn := a.deps.spawn
			if spawn == nil {
				spawn = sysproc.StartDetached
			}
			return spawn(executable, "ui")
		},
	}
	return tray.Run(ctx, opts)
}

// trayState reads the state for the icon.
func (a *app) trayState(ctx context.Context) tray.State {
	current, err := a.openSession()
	if err != nil {
		return tray.State{Level: tray.LevelUnknown, Text: a.tr.Describe(err).Message}
	}
	report := a.buildStatus(ctx, current, statusWindow)
	level := map[string]tray.Level{
		verdictOK: tray.LevelOK, verdictAttention: tray.LevelAttention, verdictProblem: tray.LevelProblem,
	}[report.Verdict]
	if level == "" {
		level = tray.LevelUnknown
	}
	text := a.tr.T("tray.state." + report.Verdict)
	if len(report.Issues) > 0 {
		text += " " + a.tr.T("status.issue."+report.Issues[0])
	}
	return tray.State{Level: level, Text: text}
}
