package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/selfinstall"
	"github.com/peregoood-ru/BackUpClaude/internal/update"
)

type updateOptions struct {
	check, pre, force bool
	fromFile, sha256  string
}

func (a *app) newUpdateCommand() *cobra.Command {
	var o updateOptions
	cmd := &cobra.Command{
		Use:   "update",
		Short: a.tr.T("cli.update.short"),
		Long:  a.tr.T("cli.update.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.update(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&o.check, "check", false, a.tr.T("flag.update.check"))
	f.BoolVar(&o.pre, "pre", false, a.tr.T("flag.update.pre"))
	f.BoolVar(&o.force, "force", false, a.tr.T("flag.update.force"))
	f.StringVar(&o.fromFile, "from-file", "", a.tr.T("flag.update.from_file"))
	f.StringVar(&o.sha256, "sha256", "", a.tr.T("flag.update.sha256"))
	return cmd
}

// updateReport is the answer of update. Settings, tasks and copies are not in
// it because the command does not touch them.
type updateReport struct {
	Current         string `json:"current"`
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Updated         bool   `json:"updated"`
	Path            string `json:"path"`
	// Source is "release" or "file".
	Source string `json:"source"`
	// Verified says the archive matched the published checksum.
	Verified bool `json:"verified"`
	// ToolsOK is false when the new program could not fetch the tools it pins.
	ToolsOK *bool `json:"tools_ok,omitempty"`
}

func (a *app) update(ctx context.Context, o updateOptions) error {
	layout, err := config.Resolve()
	if err != nil {
		return err
	}
	executable, err := a.deps.executable()
	if err != nil {
		return i18n.E("cli.no_executable").Because(err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	report := updateReport{Current: version, Path: executable, Source: "release"}
	if o.fromFile != "" {
		report.Source = "file"
		return a.updateFromFile(ctx, layout, o, &report)
	}

	client := a.releaseClient()
	release, err := client.Latest(ctx, o.pre || strings.Contains(version, "-"))
	if err != nil {
		return err
	}
	report.Latest = release.Version()
	report.UpdateAvailable = update.Newer(version, report.Latest)
	if !update.Comparable(version) && !o.force {
		// A development build has no number to compare. Offer, do not replace.
		report.UpdateAvailable = true
		if !o.check {
			return i18n.E("update.dev_build", version, report.Latest).Hint("update.dev_build.hint")
		}
	}
	if o.check || (!report.UpdateAvailable && !o.force) {
		return a.emit(report, func(w io.Writer) { a.printUpdate(w, report, o.check) })
	}

	name := update.ArchiveName(report.Latest, runtime.GOOS, runtime.GOARCH)
	archive, ok := release.Asset(name)
	if !ok {
		return i18n.E("update.no_asset", report.Latest, name).Hint("update.no_asset.hint")
	}
	sumsAsset, ok := release.Asset(update.ChecksumsFile)
	if !ok {
		return i18n.E("update.no_checksums", report.Latest).Hint("update.no_checksum.hint")
	}
	work, err := os.MkdirTemp(a.workRoot(layout), "update-")
	if err != nil {
		return i18n.E("update.write_failed", a.workRoot(layout)).Because(err)
	}
	defer os.RemoveAll(work)

	sumsFile := filepath.Join(work, update.ChecksumsFile)
	if _, err := client.Fetch(ctx, sumsAsset, sumsFile); err != nil {
		return err
	}
	text, err := os.ReadFile(sumsFile)
	if err != nil {
		return i18n.E("update.write_failed", sumsFile).Because(err)
	}
	archiveFile := filepath.Join(work, name)
	got, err := client.Fetch(ctx, archive, archiveFile)
	if err != nil {
		return err
	}
	if err := update.Verify(name, got, update.ParseChecksums(string(text))); err != nil {
		return err
	}
	report.Verified = true
	return a.installNewProgram(ctx, archiveFile, executable, report.Latest, &report)
}

// updateFromFile installs a program or an archive from the disk. It is for
// checking an update before a release exists and for a computer that is not
// connected: the hash must be given to be verified.
func (a *app) updateFromFile(ctx context.Context, layout config.Layout, o updateOptions, report *updateReport) error {
	got, err := update.HashFile(o.fromFile)
	if err != nil {
		return i18n.E("update.read_failed", o.fromFile).Because(err)
	}
	if o.sha256 != "" {
		if err := update.Verify(filepath.Base(o.fromFile), got, map[string]string{filepath.Base(o.fromFile): strings.ToLower(o.sha256)}); err != nil {
			return err
		}
		report.Verified = true
	}
	return a.installNewProgram(ctx, o.fromFile, report.Path, "", report)
}

// workRoot is a folder for downloads; the data folder is used when it exists.
func (a *app) workRoot(layout config.Layout) string {
	dir := layout.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.TempDir()
	}
	return dir
}

// installNewProgram unpacks the program next to the running one, checks that
// it starts, and swaps the files. On Windows the running file cannot be
// overwritten, only renamed, so the old one goes aside and is removed at the
// next start.
func (a *app) installNewProgram(ctx context.Context, archive, executable, want string, report *updateReport) error {
	candidate, err := os.CreateTemp(filepath.Dir(executable), ".backupclaude-new-*")
	if err != nil {
		return i18n.E("update.write_failed", filepath.Dir(executable)).Hint("update.write_failed.hint").Because(err)
	}
	candidatePath := candidate.Name()
	_ = candidate.Close()
	defer os.Remove(candidatePath)

	if err := update.ExtractProgram(archive, selfinstall.Name(), candidatePath); err != nil {
		return err
	}
	found, err := a.programVersion(ctx, candidatePath)
	if err != nil {
		return err
	}
	if want != "" && found != want {
		return i18n.E("update.wrong_version", found, want).Hint("update.no_checksum.hint")
	}
	if want == "" {
		report.Latest = found
	}
	if err := selfinstall.Swap(candidatePath, executable); err != nil {
		return err
	}
	report.Updated = true

	// The new program may pin other versions of restic and rclone; scheduled
	// runs would fail until they are there.
	_, toolsErr := a.execProgram(ctx, executable, "tools", "install", "--json")
	ok := toolsErr == nil
	report.ToolsOK = &ok
	return a.emit(*report, func(w io.Writer) { a.printUpdate(w, *report, false) })
}

// programVersion starts a program with --version --json and reads its version.
func (a *app) programVersion(ctx context.Context, program string) (string, error) {
	out, err := a.execProgram(ctx, program, "--version", "--json")
	if err != nil {
		return "", i18n.E("update.smoke_failed").Hint("update.no_checksum.hint").Because(err)
	}
	var answer struct {
		Data versionInfo `json:"data"`
	}
	if err := json.Unmarshal(out, &answer); err != nil || answer.Data.Version == "" {
		return "", i18n.E("update.smoke_failed").Hint("update.no_checksum.hint")
	}
	return answer.Data.Version, nil
}

func (a *app) printUpdate(w io.Writer, report updateReport, checkOnly bool) {
	switch {
	case report.Updated:
		fmt.Fprintln(w, a.tr.T("update.done", report.Current, report.Latest, report.Path))
		if report.ToolsOK != nil && !*report.ToolsOK {
			fmt.Fprintln(w, a.tr.T("update.tools_failed"))
		}
		fmt.Fprintln(w, a.tr.T("update.nothing_else_changed"))
	case report.UpdateAvailable && checkOnly:
		fmt.Fprintln(w, a.tr.T("update.available", report.Current, report.Latest))
	default:
		fmt.Fprintln(w, a.tr.T("update.latest", report.Current))
	}
}
