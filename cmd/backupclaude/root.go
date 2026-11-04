package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/locales"
)

const usageTemplate = `{USAGE}{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if .HasAvailableSubCommands}}

{COMMANDS}{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{FLAGS}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

{GLOBAL_FLAGS}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}
`

func (a *app) newRootCommand() *cobra.Command {
	var showVersion bool
	root := &cobra.Command{
		Use:           "backupclaude",
		Short:         a.tr.T("cli.root.short"),
		Long:          a.tr.T("cli.root.long"),
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return i18n.E("cli.unknown_command", args[0]).Hint("cli.unknown_command.hint").Exit(exitUsage)
			}
			return nil
		},
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			a.command = strings.TrimPrefix(strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()), " ")
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showVersion {
				a.command = "version"
				return a.printVersion()
			}
			return a.showGroup(cmd, nil)
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.Flags().BoolVarP(&showVersion, "version", "V", false, a.tr.T("flag.version"))
	root.PersistentFlags().BoolVar(&a.jsonOut, "json", a.jsonOut, a.tr.T("flag.json"))
	root.PersistentFlags().StringVar(&a.flagLang, "lang", "", a.tr.T("flag.lang", strings.Join(locales.Languages(), ", ")))

	root.SetUsageTemplate(strings.NewReplacer(
		"{USAGE}", a.tr.T("help.usage"),
		"{COMMANDS}", a.tr.T("help.commands"),
		"{FLAGS}", a.tr.T("help.flags"),
		"{GLOBAL_FLAGS}", a.tr.T("help.global_flags"),
	).Replace(usageTemplate))
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return i18n.E("cli.bad_flag", err.Error()).Hint("cli.bad_flag.hint").Exit(exitUsage)
	})
	root.SetHelpCommand(&cobra.Command{
		Use:   "help [command]",
		Short: a.tr.T("cli.help.short"),
		RunE: func(_ *cobra.Command, args []string) error {
			target, _, err := root.Find(args)
			if err != nil {
				return err
			}
			return target.Help()
		},
	})

	root.AddCommand(a.stubCommands()...)
	root.AddCommand(
		a.newBackupCommand(),
		a.newScheduleCommand(),
		a.newRunCommand(),
		a.newStatusCommand(),
		a.newNotifyCommand(),
		a.newListCommand(),
		a.newRestoreCommand(),
		a.newDoctorCommand(),
		a.newPasswordCommand(),
		a.newResticPasswordCommand(),
		a.newToolsCommand(),
		a.newRepoCommand(),
		a.newSelftestCommand(),
		a.newMeasureCommand(),
		a.newVerifyCommand(),
	)
	localizeHelpFlag(root, a.tr.T("flag.help"))
	return root
}

// localizeHelpFlag defines --help everywhere; Cobra keeps a flag defined ahead
// of its own English one.
func localizeHelpFlag(cmd *cobra.Command, usage string) {
	cmd.Flags().BoolP("help", "h", false, usage)
	for _, child := range cmd.Commands() {
		localizeHelpFlag(child, usage)
	}
}

// stubCommands declares the planned commands with the stage that delivers them.
func (a *app) stubCommands() []*cobra.Command {
	planned := []struct {
		name  string
		stage int
	}{
		{"setup", 7},
		{"ui", 8},
		{"update", 7},
		{"uninstall", 7},
	}
	commands := make([]*cobra.Command, 0, len(planned))
	for _, item := range planned {
		commands = append(commands, &cobra.Command{
			Use:   item.name,
			Short: a.tr.T(fmt.Sprintf("cli.%s.short", item.name)),
			Args:  a.noArgs,
			RunE: func(*cobra.Command, []string) error {
				return i18n.NotReady(item.name, item.stage)
			},
		})
	}
	return commands
}

// showGroup answers a command that only groups subcommands.
func (a *app) showGroup(cmd *cobra.Command, _ []string) error {
	if a.jsonOut {
		return a.listCommands(cmd)
	}
	return cmd.Help()
}

func (a *app) noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return i18n.E("cli.unexpected_argument", args[0]).Hint("cli.bad_flag.hint").Exit(exitUsage)
	}
	return nil
}
