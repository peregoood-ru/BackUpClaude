package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
	"github.com/peregoood-ru/BackUpClaude/internal/tray"
	"github.com/peregoood-ru/BackUpClaude/internal/ui"
)

func (a *app) newUICommand() *cobra.Command {
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "ui",
		Short: a.tr.T("cli.ui.short"),
		Long:  a.tr.T("cli.ui.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.serveUI(cmd.Context(), noOpen)
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, a.tr.T("flag.ui.no_open"))
	return cmd
}

// uiStarted is the answer of ui: where the page is. The command goes on
// running after it, until the page is closed.
type uiStarted struct {
	URL  string `json:"url"`
	Port string `json:"port"`
}

// serveUI starts the page and opens it. It returns when the page is closed, so
// nothing stays in the background.
func (a *app) serveUI(ctx context.Context, noOpen bool) error {
	server, err := ui.New(ui.Options{Backend: uiBackend{deps: a.deps}, Language: a.tr.Lang(), Version: version})
	if err != nil {
		return err
	}
	address := server.URL()
	started := uiStarted{URL: address, Port: server.Addr()}
	if a.jsonOut {
		if err := a.writeJSON(envelope{OK: true, Command: a.command, Data: started}); err != nil {
			return err
		}
	} else if noOpen {
		fmt.Fprintln(a.stdout, a.tr.T("ui.started_manual", address))
	} else {
		fmt.Fprintln(a.stdout, a.tr.T("ui.started", address))
	}
	if !noOpen {
		open := a.deps.openBrowser
		if open == nil {
			open = ui.Open
		}
		if err := open(address); err != nil {
			fmt.Fprintln(a.stderr, a.tr.T("ui.open_failed", address))
		}
	}
	if err := server.Serve(ctx); err != nil {
		return err
	}
	if !a.jsonOut {
		fmt.Fprintln(a.stdout, a.tr.T("ui.closed"))
	}
	return nil
}

// uiBackend gives the page the application: the commands themselves, run in
// this process, and the settings. The page cannot ask for anything else.
type uiBackend struct {
	deps dependencies
}

// Run runs a command with --json, exactly as the command line would.
func (b uiBackend) Run(ctx context.Context, args []string) ui.Reply {
	var out bytes.Buffer
	code := run(ctx, append(slices.Clone(args), "--json"), strings.NewReader(""), &out, io.Discard, b.deps)
	return ui.Reply{Code: code, Body: out.Bytes()}
}

func (b uiBackend) app() *app {
	return newApp(nil, strings.NewReader(""), io.Discard, io.Discard, b.deps)
}

func (b uiBackend) autostart() tray.Autostart {
	if b.deps.autostart != nil {
		return b.deps.autostart
	}
	return tray.NewAutostart()
}

// Settings reads the settings for the page.
func (b uiBackend) Settings(context.Context) (ui.Settings, error) {
	layout, err := config.Resolve()
	if err != nil {
		return ui.Settings{}, err
	}
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil {
		return ui.Settings{}, err
	}
	return b.view(layout, cfg), nil
}

func (b uiBackend) view(layout config.Layout, cfg *config.Config) ui.Settings {
	a := b.app()
	settings := ui.Settings{
		Language: cfg.Language, DataFolder: layout.Root, Local: cfg.Repositories.Local,
		Offsite: safeLocation(cfg.Repositories.Offsite), Schedule: cfg.Schedule, Retention: cfg.Retention,
		DropPercent: cfg.Notify.DeletionDropPercent,
		Extra:       append([]string{}, cfg.Sources.Extra...), Exclude: append([]string{}, cfg.Sources.Exclude...),
		Channels: []string{}, Tray: cfg.Tray.Enabled, TraySupported: b.autostart().Supported(),
	}
	if notifier, _ := a.notifierFor(cfg); notifier != nil {
		for _, channel := range notifier.Channels {
			settings.Channels = append(settings.Channels, channel.Name())
		}
	}
	_, err := b.deps.secrets().Get(secrets.RepoPasswordKey)
	settings.PasswordStored = err == nil
	return settings
}

// cleanList trims what a person typed or pasted: spaces and the quotes that
// "Copy as path" adds.
func cleanList(items []string) []string {
	cleaned := []string{}
	for _, item := range items {
		item = strings.Trim(strings.TrimSpace(item), `"`)
		if item != "" {
			cleaned = append(cleaned, item)
		}
	}
	return cleaned
}

// Save applies the changes of the settings page. The settings file is checked
// and written by the same code as everywhere else; a wrong value changes
// nothing.
func (b uiBackend) Save(_ context.Context, patch ui.Patch) (ui.Settings, error) {
	layout, err := config.Resolve()
	if err != nil {
		return ui.Settings{}, err
	}
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil {
		return ui.Settings{}, err
	}
	previous := *cfg
	if patch.Language != nil {
		cfg.Language = strings.TrimSpace(*patch.Language)
	}
	if patch.Retention != nil {
		cfg.Retention = *patch.Retention
	}
	if patch.DropPercent != nil {
		cfg.Notify.DeletionDropPercent = *patch.DropPercent
	}
	if patch.Extra != nil {
		cfg.Sources.Extra = cleanList(*patch.Extra)
	}
	if patch.Exclude != nil {
		cfg.Sources.Exclude = cleanList(*patch.Exclude)
	}
	trayChanged := patch.Tray != nil && *patch.Tray != cfg.Tray.Enabled
	if patch.Tray != nil {
		cfg.Tray.Enabled = *patch.Tray
	}
	if err := config.Save(layout.ConfigFile(), cfg); err != nil {
		return ui.Settings{}, err
	}
	if trayChanged {
		if err := b.applyTray(cfg.Tray.Enabled); err != nil {
			// The registration failed: the setting must not claim it is on.
			_ = config.Save(layout.ConfigFile(), &previous)
			return ui.Settings{}, err
		}
	}
	return b.view(layout, cfg), nil
}

// applyTray registers or removes the start of the icon at logon, and shows the
// icon at once when it was switched on.
func (b uiBackend) applyTray(on bool) error {
	autostart := b.autostart()
	if !on {
		return autostart.Disable()
	}
	executable, err := b.deps.executable()
	if err != nil {
		return i18n.E("cli.no_executable").Because(err)
	}
	if err := autostart.Enable(executable); err != nil {
		return err
	}
	if err := b.startDetached(executable, "tray"); err != nil {
		return i18n.E("tray.start_failed").Hint("tray.start_failed.hint").Because(err)
	}
	return nil
}

func (b uiBackend) startDetached(program string, args ...string) error {
	if b.deps.spawn != nil {
		return b.deps.spawn(program, args...)
	}
	return nil
}
