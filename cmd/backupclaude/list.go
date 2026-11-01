package main

import (
	"context"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/profile"
)

const (
	transcriptSuffix = ".jsonl"
	defaultListLimit = 20
	timeLayout       = "2006-01-02 15:04"
)

type listOptions struct {
	snapshots, sessions, projects bool
	limit                         int
}

func (a *app) newListCommand() *cobra.Command {
	var options listOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: a.tr.T("cli.list.short"),
		Long:  a.tr.T("cli.list.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !options.snapshots && !options.sessions && !options.projects {
				options.snapshots, options.sessions, options.projects = true, true, true
			}
			return a.list(cmd.Context(), options)
		},
	}
	cmd.Flags().BoolVar(&options.snapshots, "snapshots", false, a.tr.T("flag.list.snapshots"))
	cmd.Flags().BoolVar(&options.sessions, "sessions", false, a.tr.T("flag.list.sessions"))
	cmd.Flags().BoolVar(&options.projects, "projects", false, a.tr.T("flag.list.projects"))
	cmd.Flags().IntVar(&options.limit, "limit", defaultListLimit, a.tr.T("flag.list.limit"))
	return cmd
}

// snapshotRow is one snapshot.
type snapshotRow struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Profile string    `json:"profile"`
	Host    string    `json:"host"`
	Paths   int       `json:"paths"`
	Files   int64     `json:"files"`
	Bytes   int64     `json:"bytes"`
}

// sessionRow is one session on disk, in the latest snapshot, or in both.
type sessionRow struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Title      string    `json:"title,omitempty"`
	Cwd        string    `json:"cwd,omitempty"`
	Size       int64     `json:"size"`
	Modified   time.Time `json:"modified"`
	OnDisk     bool      `json:"on_disk"`
	InSnapshot bool      `json:"in_snapshot"`
}

// projectRow is one project or own folder.
type projectRow struct {
	Path     string     `json:"path"`
	Source   string     `json:"source"`
	Sessions int        `json:"sessions"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
	// Saved says whether the profile saves it. Inside names the folder that
	// holds it when it has no path of its own.
	Saved  bool   `json:"saved"`
	Inside string `json:"inside,omitempty"`
}

type listResult struct {
	Snapshots *[]snapshotRow    `json:"snapshots,omitempty"`
	Sessions  *[]sessionRow     `json:"sessions,omitempty"`
	Projects  *[]projectRow     `json:"projects,omitempty"`
	Warnings  []profile.Warning `json:"warnings,omitempty"`
}

func (a *app) list(ctx context.Context, options listOptions) error {
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
	var result listResult
	var snapshots []engine.Snapshot
	var current *session
	if options.snapshots || options.sessions {
		if current, err = a.openSession(); err != nil {
			return err
		}
		if snapshots, err = current.restic.Snapshots(ctx, ""); err != nil {
			return err
		}
	}
	if options.snapshots {
		rows := snapshotRows(snapshots)
		result.Snapshots = &rows
	}
	if options.sessions {
		rows, err := a.sessionRows(ctx, current, snapshots, in)
		if err != nil {
			return err
		}
		result.Sessions = &rows
	}
	if options.projects {
		rows, warnings, err := projectRows(in)
		if err != nil {
			return err
		}
		result.Projects = &rows
		result.Warnings = warnings
	}
	return a.emit(result, func(w io.Writer) { a.printList(w, result, options) })
}

func snapshotRows(snapshots []engine.Snapshot) []snapshotRow {
	rows := make([]snapshotRow, 0, len(snapshots))
	for i := len(snapshots) - 1; i >= 0; i-- {
		snapshot := snapshots[i]
		row := snapshotRow{ID: snapshot.ShortID, Time: snapshot.Time, Host: snapshot.Hostname, Paths: len(snapshot.Paths), Profile: snapshotProfile(snapshot)}
		if snapshot.Summary != nil {
			row.Files, row.Bytes = snapshot.Summary.TotalFilesProcessed, snapshot.Summary.TotalBytesProcessed
		}
		rows = append(rows, row)
	}
	return rows
}

func snapshotProfile(snapshot engine.Snapshot) string {
	for _, name := range profile.Names() {
		if slices.Contains(snapshot.Tags, name) {
			return name
		}
	}
	return strings.Join(snapshot.Tags, ",")
}

func latestOf(snapshots []engine.Snapshot, name string) *engine.Snapshot {
	var latest *engine.Snapshot
	for i := range snapshots {
		if slices.Contains(snapshots[i].Tags, name) && (latest == nil || snapshots[i].Time.After(latest.Time)) {
			latest = &snapshots[i]
		}
	}
	return latest
}

// sessionRows joins sessions on disk with transcripts in the latest snapshot, so
// a session Claude has deleted still shows up.
func (a *app) sessionRows(ctx context.Context, current *session, snapshots []engine.Snapshot, in profile.Inputs) ([]sessionRow, error) {
	found := discover.Locate(in.Manifest, in.Env)
	sessions := discover.ReadSessions(in.Manifest, found, discover.NewFilter(in.Manifest, in.Env))
	rows := make([]sessionRow, 0, len(sessions.List))
	byID := make(map[string]int, len(sessions.List))
	for _, item := range sessions.List {
		byID[item.ID] = len(rows)
		rows = append(rows, sessionRow{
			ID: item.ID, Kind: item.Kind, Title: item.Title, Cwd: item.Cwd,
			Size: item.Size, Modified: item.Modified, OnDisk: true,
		})
	}
	latest := latestOf(snapshots, profile.Claude)
	if latest == nil || !found.Home.Exists {
		return rows, nil
	}
	root := profile.TranscriptsInSnapshot(in.Manifest, found)
	err := current.restic.Ls(ctx, latest.ShortID, []string{root}, func(node engine.Node) {
		if node.Type != engine.NodeFile || !strings.HasSuffix(node.Name, transcriptSuffix) ||
			path.Dir(path.Dir(node.Path)) != root {
			return
		}
		id := strings.TrimSuffix(node.Name, transcriptSuffix)
		if index, ok := byID[id]; ok {
			rows[index].InSnapshot = true
			return
		}
		byID[id] = len(rows)
		rows = append(rows, sessionRow{ID: id, Kind: discover.KindCode, Size: node.Size, Modified: node.Mtime, InSnapshot: true})
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(rows, func(x, y sessionRow) int { return y.Modified.Compare(x.Modified) })
	return rows, nil
}

// projectRows shows the remembered list and the plan side by side. Transcripts
// are read, so a project found since the last backup shows up, but nothing is
// saved.
func projectRows(in profile.Inputs) ([]projectRow, []profile.Warning, error) {
	plan, err := profile.Build(profile.Projects, in)
	if err != nil {
		return nil, nil, err
	}
	saved := map[string]string{}
	for _, item := range plan.Items {
		saved[item.Path] = item.Source
	}
	inside := map[string]string{}
	excluded := map[string]bool{}
	for _, left := range plan.Left {
		if left.Inside == "" {
			excluded[left.Path] = true
		} else {
			inside[left.Path] = left.Inside
		}
	}
	rows := []projectRow{}
	seen := map[string]bool{}
	for _, project := range plan.Known() {
		seen[project.Path] = true
		lastSeen := project.LastSeen
		rows = append(rows, projectRow{
			Path: project.Path, Source: profile.SourceProject, Sessions: project.Sessions, LastSeen: &lastSeen,
			Saved: saved[project.Path] != "" || inside[project.Path] != "", Inside: inside[project.Path],
		})
	}
	for path, source := range saved {
		if !seen[path] {
			rows = append(rows, projectRow{Path: path, Source: source, Saved: true})
		}
	}
	for path, outer := range inside {
		if !seen[path] {
			rows = append(rows, projectRow{Path: path, Source: profile.SourceExtra, Saved: true, Inside: outer})
		}
	}
	slices.SortFunc(rows, func(x, y projectRow) int { return strings.Compare(x.Path, y.Path) })
	return rows, plan.Warnings, nil
}

func (a *app) printList(w io.Writer, result listResult, options listOptions) {
	if result.Snapshots != nil {
		fmt.Fprintln(w, a.tr.T("list.snapshots", len(*result.Snapshots)))
		for _, row := range limited(*result.Snapshots, options.limit) {
			fmt.Fprintf(w, "  %s  %s  %-9s %s\n", row.ID, row.Time.Local().Format(timeLayout), row.Profile,
				a.tr.T("list.snapshot_size", row.Files, formatBytes(row.Bytes), row.Paths))
		}
	}
	if result.Sessions != nil {
		fmt.Fprintln(w, a.tr.T("list.sessions", len(*result.Sessions)))
		for _, row := range limited(*result.Sessions, options.limit) {
			state := a.tr.T("list.session.both")
			switch {
			case !row.OnDisk:
				state = a.tr.T("list.session.snapshot_only")
			case !row.InSnapshot:
				state = a.tr.T("list.session.disk_only")
			}
			fmt.Fprintf(w, "  %s  %s  %-8s %-10s %s  %s\n", row.ID, row.Modified.Local().Format(timeLayout), row.Kind, formatBytes(row.Size), state, firstNonEmpty(row.Title, row.Cwd))
		}
	}
	if result.Projects != nil {
		fmt.Fprintln(w, a.tr.T("list.projects", len(*result.Projects)))
		for _, row := range *result.Projects {
			switch {
			case row.Inside != "":
				fmt.Fprintln(w, "  "+a.tr.T("list.project.inside", row.Path, row.Inside))
			case row.Saved:
				fmt.Fprintln(w, "  "+a.tr.T("list.project.saved", row.Path, row.Sessions))
			default:
				fmt.Fprintln(w, "  "+a.tr.T("list.project.not_saved", row.Path, row.Sessions))
			}
		}
		a.printWarnings(w, result.Warnings)
	}
}

func limited[T any](rows []T, limit int) []T {
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
