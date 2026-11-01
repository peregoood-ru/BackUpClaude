package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	backupclaude "github.com/peregoood-ru/BackUpClaude"
	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
)

func (a *app) installer(layout config.Layout) (*engine.Installer, error) {
	lock, err := engine.ParseLock(backupclaude.ToolsLock)
	if err != nil {
		return nil, err
	}
	return &engine.Installer{Lock: lock, Dir: layout.BinDir()}, nil
}

func (a *app) newToolsCommand() *cobra.Command {
	tools := &cobra.Command{
		Use:    "tools",
		Short:  a.tr.T("cli.tools.short"),
		Hidden: true,
		Args:   a.noArgs,
		RunE:   a.showGroup,
	}
	tools.AddCommand(&cobra.Command{
		Use:   "install",
		Short: a.tr.T("cli.tools.install.short"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout, err := config.Resolve()
			if err != nil {
				return err
			}
			installer, err := a.installer(layout)
			if err != nil {
				return err
			}
			var installed []*engine.Installed
			for _, name := range []string{engine.ToolRestic, engine.ToolRclone} {
				tool, err := installer.Install(cmd.Context(), name)
				if err != nil {
					return err
				}
				installed = append(installed, tool)
			}
			return a.emit(map[string]any{"tools": installed}, func(w io.Writer) {
				for _, tool := range installed {
					fmt.Fprintln(w, a.tr.T("tools.installed", tool.Tool, tool.Version, tool.Path))
				}
			})
		},
	})
	return tools
}
