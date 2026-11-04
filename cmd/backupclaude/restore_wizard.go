package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
)

// wizardSessions is how many recent sessions the questions show.
const wizardSessions = 15

// wizard asks questions on the terminal. Every question has an answer that is
// taken when the person just presses Enter.
type wizard struct {
	a  *app
	in *bufio.Reader
}

func (z *wizard) ask(prompt, fallback string) (string, error) {
	if fallback != "" {
		fmt.Fprintf(z.a.stdout, "%s [%s]: ", prompt, fallback)
	} else {
		fmt.Fprintf(z.a.stdout, "%s: ", prompt)
	}
	line, err := z.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", i18n.E("restore.wizard.cancelled")
	}
	line = strings.TrimSpace(line)
	if line == "" {
		if errors.Is(err, io.EOF) && fallback == "" {
			return "", i18n.E("restore.wizard.cancelled")
		}
		return fallback, nil
	}
	return line, nil
}

// choose asks for one of count numbered answers.
func (z *wizard) choose(prompt string, count, fallback int) (int, error) {
	for range 3 {
		answer, err := z.ask(prompt, strconv.Itoa(fallback))
		if err != nil {
			return 0, err
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= count {
			return n, nil
		}
		fmt.Fprintln(z.a.stdout, z.a.tr.T("restore.wizard.bad_choice", count))
	}
	return 0, i18n.E("restore.wizard.cancelled")
}

// restoreWizard is restore without flags: the person answers a few questions
// and sees the latest sessions to pick from.
func (a *app) restoreWizard(ctx context.Context, options restoreOptions) error {
	z := &wizard{a: a, in: bufio.NewReader(a.stdin)}
	fmt.Fprintln(a.stdout, a.tr.T("restore.wizard.intro"))

	if options.repo == "" {
		layout, err := config.Resolve()
		if err != nil {
			return err
		}
		cfg, err := config.Load(layout.ConfigFile())
		switch {
		case config.IsMissing(err):
			fmt.Fprintln(a.stdout, a.tr.T("restore.wizard.no_settings"))
			if options.repo, err = z.ask(a.tr.T("restore.wizard.repo"), ""); err != nil {
				return err
			}
		case err != nil:
			return err
		case cfg.Repositories.Offsite != "":
			fmt.Fprintln(a.stdout, a.tr.T("restore.wizard.from_choices"))
			n, err := z.choose(a.tr.T("restore.wizard.from"), 2, 1)
			if err != nil {
				return err
			}
			if n == 2 {
				options.from = fromOffsite
			}
		default:
			fmt.Fprintln(a.stdout, a.tr.T("restore.wizard.offsite_none"))
		}
	}
	source, err := a.openRestoreSource(ctx, options)
	if err != nil {
		return err
	}
	snapshots, err := source.restic.Snapshots(ctx, "")
	if err != nil {
		return err
	}

	fmt.Fprintln(a.stdout, a.tr.T("restore.wizard.what_choices"))
	what, err := z.choose(a.tr.T("restore.wizard.what"), 3, 1)
	if err != nil {
		return err
	}
	mode := []string{restore.ModeSession, restore.ModeProject, restore.ModeAll}[what-1]
	switch mode {
	case restore.ModeSession:
		if options.session, err = z.pickSession(ctx, source, snapshots); err != nil {
			return err
		}
	case restore.ModeProject:
		if options.project, err = z.pickProject(snapshots); err != nil {
			return err
		}
	default:
		options.all = true
	}

	base := restore.DefaultTarget(source.base, "…")
	fmt.Fprintln(a.stdout, a.tr.T("restore.wizard.where_choices", base))
	where, err := z.choose(a.tr.T("restore.wizard.where"), 2, 1)
	if err != nil {
		return err
	}
	options.apply = where == 2

	report, err := a.runRestore(ctx, source, mode, options)
	if err != nil {
		return err
	}
	a.printRestore(a.stdout, report)
	return nil
}

// snapshotSession is a session the latest claude snapshot holds.
type snapshotSession struct {
	ID       string
	Kind     string
	Folder   string
	Modified time.Time
	Size     int64
	OnDisk   bool
	Label    string
}

func (z *wizard) pickSession(ctx context.Context, source *restoreSource, snapshots []engine.Snapshot) (string, error) {
	latest := restore.Latest(snapshots, restore.TagClaude)
	if latest == nil {
		return "", i18n.E("restore.no_snapshots", restore.TagClaude).Hint("restore.no_snapshots.hint")
	}
	sessions, err := snapshotSessions(ctx, source, *latest)
	if err != nil {
		return "", err
	}
	shown := limited(sessions, wizardSessions)
	fmt.Fprintln(z.a.stdout, z.a.tr.T("restore.wizard.sessions", len(sessions), latest.ShortID, latest.Time.Local().Format("02.01.2006 15:04")))
	for i, item := range shown {
		state := z.a.tr.T("restore.wizard.state_disk")
		if !item.OnDisk {
			state = z.a.tr.T("restore.wizard.state_missing")
		}
		fmt.Fprintf(z.a.stdout, "  %2d. %s  %s  %-8s %-9s %s  %s\n", i+1, item.Modified.Local().Format("02.01 15:04"),
			item.ID[:min(8, len(item.ID))], item.Kind, formatBytes(item.Size), state, item.Label)
	}
	answer, err := z.ask(z.a.tr.T("restore.wizard.session"), "1")
	if err != nil {
		return "", err
	}
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(shown) {
		return shown[n-1].ID, nil
	}
	return answer, nil // an id or its beginning, typed or pasted
}

// snapshotSessions lists the sessions of a claude snapshot, newest first, and
// marks those that are on this computer. It works from the snapshot alone, so
// it also works on a new computer.
func snapshotSessions(ctx context.Context, source *restoreSource, snapshot engine.Snapshot) ([]snapshotSession, error) {
	origin := restore.OriginOf(source.manifest, snapshot)
	transcripts := origin.Transcripts(source.manifest)
	var roots []string
	if transcripts != "" {
		roots = append(roots, transcripts)
	}
	kinds := map[string]string{}
	for _, root := range origin.Desktop {
		for _, dir := range source.manifest.Desktop.SessionDirs {
			p := path.Join(root, dir)
			roots = append(roots, p)
			kinds[p] = discover.KindDesktop
			if dir == source.manifest.Desktop.CoworkDir {
				kinds[p] = discover.KindCowork
			}
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}
	found := discover.Locate(source.manifest, source.env)
	here := discover.ReadSessions(source.manifest, found, discover.NewFilter(source.manifest, source.env))
	titles := map[string]string{}
	for _, item := range here.List {
		titles[item.ID] = firstNonEmpty(item.Title, item.Cwd)
		if item.DescriptionID != "" {
			titles[item.DescriptionID] = titles[item.ID]
		}
	}
	var sessions []snapshotSession
	err := source.restic.Nodes(ctx, snapshot.ID, roots, true, func(node engine.Node) {
		if node.Type != engine.NodeFile {
			return
		}
		parent := path.Dir(node.Path)
		if id, ok := strings.CutSuffix(node.Name, transcriptSuffix); ok && transcripts != "" && path.Dir(parent) == transcripts {
			sessions = append(sessions, snapshotSession{ID: id, Kind: discover.KindCode, Folder: path.Base(parent), Modified: node.Mtime, Size: node.Size})
			return
		}
		for root, kind := range kinds {
			if strings.HasPrefix(node.Path, root+"/") {
				if id, ok := discover.DescriptionID(node.Name); ok {
					sessions = append(sessions, snapshotSession{ID: id, Kind: kind, Modified: node.Mtime, Size: node.Size})
				}
				return
			}
		}
	})
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		title, ok := titles[sessions[i].ID]
		sessions[i].OnDisk = ok
		sessions[i].Label = firstNonEmpty(title, sessions[i].Folder)
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].Modified.After(sessions[j].Modified) })
	return sessions, nil
}

func (z *wizard) pickProject(snapshots []engine.Snapshot) (string, error) {
	latest := restore.Latest(snapshots, restore.TagProjects)
	if latest == nil {
		return "", i18n.E("restore.no_snapshots", restore.TagProjects).Hint("restore.no_snapshots.hint")
	}
	roots := restore.RootsOf(*latest)
	sort.Strings(roots)
	fmt.Fprintln(z.a.stdout, z.a.tr.T("restore.wizard.projects", len(roots), latest.ShortID, latest.Time.Local().Format("02.01.2006 15:04")))
	for i, root := range roots {
		fmt.Fprintf(z.a.stdout, "  %2d. %s\n", i+1, restore.LocalPath(root))
	}
	answer, err := z.ask(z.a.tr.T("restore.wizard.project"), "")
	if err != nil {
		return "", err
	}
	if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(roots) {
		return restore.LocalPath(roots[n-1]), nil
	}
	return answer, nil
}
