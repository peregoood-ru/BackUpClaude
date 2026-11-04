package main

import (
	"fmt"
	"io"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/selfinstall"
	"github.com/peregoood-ru/BackUpClaude/internal/skill"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// claudeHome finds the folder of Claude Code from the manifest.
func (a *app) claudeHome() (discover.Location, error) {
	manifest, err := manifests.Load(runtime.GOOS)
	if err != nil {
		return discover.Location{}, err
	}
	env, err := a.deps.env()
	if err != nil {
		return discover.Location{}, i18n.E("cli.no_home").Because(err)
	}
	return discover.Locate(manifest, env).Home, nil
}

// skillCommand is how the skill calls the program: by name when a terminal
// finds it, otherwise by the full path.
func (a *app) skillCommand(layout config.Layout) string {
	executable, err := a.deps.executable()
	if err != nil {
		return "backupclaude"
	}
	onPath := false
	if a.pathManager().Supported() {
		onPath, _ = a.pathManager().Contains(filepath.Dir(selfinstall.Target(layout.BinDir())))
		// A program that does not live in the folder on the PATH is not found by name.
		if !selfinstall.SameFile(executable, selfinstall.Target(layout.BinDir())) {
			onPath = false
		}
	}
	return skill.Command(executable, onPath)
}

type skillAnswer struct {
	skill.State
	Changed bool `json:"changed"`
}

func (a *app) newSkillCommand() *cobra.Command {
	group := &cobra.Command{
		Use:   "skill",
		Short: a.tr.T("cli.skill.short"),
		Long:  a.tr.T("cli.skill.long"),
		Args:  a.noArgs,
		RunE:  a.showGroup,
	}
	group.AddCommand(
		&cobra.Command{
			Use: "install", Short: a.tr.T("cli.skill.install.short"), Args: a.noArgs,
			RunE: func(*cobra.Command, []string) error { return a.skillInstall() },
		},
		&cobra.Command{
			Use: "remove", Short: a.tr.T("cli.skill.remove.short"), Args: a.noArgs,
			RunE: func(*cobra.Command, []string) error { return a.skillRemove() },
		},
		&cobra.Command{
			Use: "status", Short: a.tr.T("cli.skill.status.short"), Args: a.noArgs,
			RunE: func(*cobra.Command, []string) error { return a.skillStatus() },
		},
	)
	return group
}

func (a *app) skillInstall() error {
	layout, err := config.Resolve()
	if err != nil {
		return err
	}
	home, err := a.claudeHome()
	if err != nil {
		return err
	}
	if !home.Exists {
		return i18n.E("skill.no_claude", home.Path).Hint("skill.no_claude.hint")
	}
	command := a.skillCommand(layout)
	changed, err := skill.Install(home.Path, command)
	if err != nil {
		return err
	}
	state, err := skill.Status(home.Path, command)
	if err != nil {
		return err
	}
	return a.emit(skillAnswer{State: state, Changed: changed}, func(w io.Writer) {
		if changed {
			fmt.Fprintln(w, a.tr.T("skill.installed", state.Path))
		} else {
			fmt.Fprintln(w, a.tr.T("skill.up_to_date", state.Path))
		}
		fmt.Fprintln(w, a.tr.T("skill.how_to_use"))
	})
}

func (a *app) skillRemove() error {
	home, err := a.claudeHome()
	if err != nil {
		return err
	}
	removed, err := skill.Remove(home.Path)
	if err != nil {
		return err
	}
	state, _ := skill.Status(home.Path, "")
	return a.emit(skillAnswer{State: state, Changed: removed}, func(w io.Writer) {
		if removed {
			fmt.Fprintln(w, a.tr.T("skill.removed"))
		} else {
			fmt.Fprintln(w, a.tr.T("skill.not_installed"))
		}
	})
}

func (a *app) skillStatus() error {
	layout, err := config.Resolve()
	if err != nil {
		return err
	}
	home, err := a.claudeHome()
	if err != nil {
		return err
	}
	state, err := skill.Status(home.Path, a.skillCommand(layout))
	if err != nil {
		return err
	}
	return a.emit(skillAnswer{State: state}, func(w io.Writer) {
		switch {
		case !state.Installed:
			fmt.Fprintln(w, a.tr.T("skill.not_installed"))
		case !state.Ours:
			fmt.Fprintln(w, a.tr.T("skill.foreign", state.Path))
		case state.Current:
			fmt.Fprintln(w, a.tr.T("skill.up_to_date", state.Path))
		default:
			fmt.Fprintln(w, a.tr.T("skill.outdated", state.Path))
		}
	})
}
