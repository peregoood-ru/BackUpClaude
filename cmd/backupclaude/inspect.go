package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/profile"
)

// newMeasureCommand is a service command: it answers how large a profile is and
// where the heavy media sits.
func (a *app) newMeasureCommand() *cobra.Command {
	var name string
	var heavyGB int64
	cmd := &cobra.Command{
		Use:    "measure",
		Short:  a.tr.T("cli.measure.short"),
		Long:   a.tr.T("cli.measure.long"),
		Hidden: true,
		Args:   a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.measure(cmd.Context(), name, heavyGB*gigabyte)
		},
	}
	cmd.Flags().StringVar(&name, "profile", profile.Projects, a.tr.T("flag.profile"))
	cmd.Flags().Int64Var(&heavyGB, "heavy-gb", 1, a.tr.T("flag.measure.heavy"))
	return cmd
}

func (a *app) measure(ctx context.Context, name string, threshold int64) error {
	names, err := profileNames(name)
	if err != nil {
		return err
	}
	layout, err := config.Resolve()
	if err != nil {
		return err
	}
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil {
		return err
	}
	in, err := a.planInputs(layout, cfg)
	if err != nil {
		return err
	}
	results := map[string]*profile.Measurement{}
	for _, name := range names {
		plan, err := profile.Build(name, in)
		if err != nil {
			return err
		}
		result, err := plan.Measure(ctx, threshold)
		if err != nil {
			return i18n.E("measure.failed", name).Because(err)
		}
		results[name] = result
	}
	return a.emit(results, func(w io.Writer) {
		for _, name := range names {
			result := results[name]
			fmt.Fprintln(w, a.tr.T("measure.total", name, result.Files, formatBytes(result.Bytes), formatBytes(result.Media), result.CloudOnly))
			for _, folder := range result.Heavy {
				fmt.Fprintln(w, "  "+a.tr.T("measure.heavy", folder.Path, formatBytes(folder.Media), formatBytes(folder.Bytes), folder.Files))
			}
		}
	})
}

// newVerifyCommand is a service command: it reads what a snapshot really holds.
func (a *app) newVerifyCommand() *cobra.Command {
	var name, snapshot string
	var find []string
	cmd := &cobra.Command{
		Use:    "verify",
		Short:  a.tr.T("cli.verify.short"),
		Long:   a.tr.T("cli.verify.long"),
		Hidden: true,
		Args:   a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.verify(cmd.Context(), name, snapshot, find)
		},
	}
	cmd.Flags().StringVar(&name, "profile", profileAll, a.tr.T("flag.profile"))
	cmd.Flags().StringVar(&snapshot, "snapshot", "", a.tr.T("flag.verify.snapshot"))
	cmd.Flags().StringArrayVar(&find, "find", nil, a.tr.T("flag.verify.find"))
	return cmd
}

func (a *app) verify(ctx context.Context, value, snapshot string, find []string) error {
	names, err := profileNames(value)
	if err != nil {
		return err
	}
	current, err := a.openSession()
	if err != nil {
		return err
	}
	in, err := a.planInputs(current.layout, current.config)
	if err != nil {
		return err
	}
	snapshots, err := current.restic.Snapshots(ctx, "")
	if err != nil {
		return err
	}
	checks := []*profile.Check{}
	ok := true
	for _, name := range names {
		id := snapshot
		if id == "" {
			latest := latestOf(snapshots, name)
			if latest == nil {
				return i18n.E("verify.no_snapshot", name).Hint("verify.no_snapshot.hint")
			}
			id = latest.ShortID
		}
		plan, err := profile.Build(name, in)
		if err != nil {
			return err
		}
		check, err := plan.Verify(ctx, id, find, current.restic.Ls)
		if err != nil {
			return err
		}
		ok = ok && check.OK
		checks = append(checks, check)
	}
	if a.jsonOut {
		if err := a.writeJSON(envelope{OK: ok, Command: a.command, Data: map[string]any{"checks": checks}}); err != nil {
			return err
		}
	} else {
		for _, check := range checks {
			a.printCheck(a.stdout, check)
		}
	}
	if !ok {
		return errReported
	}
	return nil
}

func (a *app) printCheck(w io.Writer, check *profile.Check) {
	fmt.Fprintln(w, a.tr.T("verify.header", check.Profile, check.Snapshot, check.Files, formatBytes(check.Bytes)))
	for _, row := range check.Rows {
		fmt.Fprintf(w, "  [%s] %s: %d, %s\n", a.tr.T("verify.status."+row.Status), row.Path, row.Files, formatBytes(row.Bytes))
	}
	for name, count := range check.Sought {
		fmt.Fprintln(w, "  "+a.tr.T("verify.sought", name, count))
	}
	for _, item := range check.Forbidden {
		fmt.Fprintln(w, "  "+a.tr.T("verify.forbidden", item.Path))
	}
}
