package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
	"github.com/peregoood-ru/BackUpClaude/internal/sysproc"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// Sources of a restore.
const (
	fromLocal   = "local"
	fromOffsite = "offsite"
	// fromRepo is a repository named on the command line, for a computer that
	// has no settings yet.
	fromRepo = "repository"
)

// restoreLockWait is how long a restore waits for a cleanup that holds the
// repository: a person is waiting.
const restoreLockWait = 10 * time.Minute

// stampLayout names a restore folder after the moment.
const stampLayout = "2006-01-02_150405"

type restoreOptions struct {
	session, project string
	all              bool
	from             string
	snapshot         string
	to               string
	apply, dryRun    bool
	maps             []string
	repo             string
	passwordStdin    bool
}

func (a *app) newRestoreCommand() *cobra.Command {
	var options restoreOptions
	cmd := &cobra.Command{
		Use:   "restore",
		Short: a.tr.T("cli.restore.short"),
		Long:  a.tr.T("cli.restore.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.restoreCommand(cmd.Context(), options)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&options.session, "session", "", a.tr.T("flag.restore.session"))
	flags.StringVar(&options.project, "project", "", a.tr.T("flag.restore.project"))
	flags.BoolVar(&options.all, "all", false, a.tr.T("flag.restore.all"))
	flags.StringVar(&options.from, "from", fromLocal, a.tr.T("flag.restore.from"))
	flags.StringVar(&options.snapshot, "snapshot", "", a.tr.T("flag.restore.snapshot"))
	flags.StringVar(&options.to, "to", "", a.tr.T("flag.restore.to"))
	flags.BoolVar(&options.apply, "apply", false, a.tr.T("flag.restore.apply"))
	flags.BoolVar(&options.dryRun, "dry-run", false, a.tr.T("flag.restore.dry_run"))
	flags.StringArrayVar(&options.maps, "map", nil, a.tr.T("flag.restore.map"))
	flags.StringVar(&options.repo, "repo", "", a.tr.T("flag.restore.repo"))
	flags.BoolVar(&options.passwordStdin, "password-stdin", false, a.tr.T("flag.restore.password_stdin"))
	return cmd
}

func (o restoreOptions) mode() (string, error) {
	var modes []string
	if o.session != "" {
		modes = append(modes, restore.ModeSession)
	}
	if o.project != "" {
		modes = append(modes, restore.ModeProject)
	}
	if o.all {
		modes = append(modes, restore.ModeAll)
	}
	switch len(modes) {
	case 0:
		return "", nil
	case 1:
		return modes[0], nil
	}
	return "", i18n.E("restore.one_mode").Hint("restore.one_mode.hint").Exit(exitUsage)
}

func (a *app) restoreCommand(ctx context.Context, options restoreOptions) error {
	mode, err := options.mode()
	if err != nil {
		return err
	}
	if options.dryRun && !options.apply {
		return i18n.E("restore.dry_run_needs_apply").Hint("restore.dry_run_needs_apply.hint").Exit(exitUsage)
	}
	if options.from != fromLocal && options.from != fromOffsite {
		return i18n.E("restore.bad_from", options.from).Hint("restore.bad_from.hint").Exit(exitUsage)
	}
	if mode == "" {
		if a.jsonOut || !a.deps.terminal() {
			return i18n.E("restore.mode_required").Hint("restore.mode_required.hint").Exit(exitUsage)
		}
		return a.restoreWizard(ctx, options)
	}
	source, err := a.openRestoreSource(ctx, options)
	if err != nil {
		return err
	}
	report, err := a.runRestore(ctx, source, mode, options)
	if err != nil {
		return err
	}
	return a.emit(report, func(w io.Writer) { a.printRestore(w, report) })
}

// restoreSource is the repository a restore reads.
type restoreSource struct {
	role     string
	location string
	restic   *engine.Restic
	manifest *manifests.Manifest
	env      discover.Env
	// base is where the restore folder goes when the person names none.
	base string
}

// openRestoreSource opens the copy to restore from. A repository given with
// --repo needs no settings: that is the computer that replaced a lost one, with
// the password written on paper. The password is then typed, and goes to
// restic on its input only.
func (a *app) openRestoreSource(ctx context.Context, options restoreOptions) (*restoreSource, error) {
	manifest, err := manifests.Load(runtime.GOOS)
	if err != nil {
		return nil, err
	}
	env, err := a.deps.env()
	if err != nil {
		return nil, i18n.E("cli.no_home").Because(err)
	}
	source := &restoreSource{manifest: manifest, env: env}
	if options.repo != "" {
		return a.openNamedRepo(ctx, source, options)
	}
	current, err := a.openSession()
	if err != nil {
		if config.IsMissing(err) {
			return nil, i18n.E("restore.no_settings").Hint("restore.no_settings.hint").Because(err)
		}
		return nil, err
	}
	source.restic = current.restic
	source.restic.RetryLock = restoreLockWait
	source.base = filepath.Dir(current.config.Repositories.Local)
	source.role, source.location = fromLocal, current.config.Repositories.Local
	if options.from == fromOffsite {
		if current.config.Repositories.Offsite == "" {
			return nil, i18n.E("restore.offsite_missing").Hint("restore.offsite_missing.hint")
		}
		source.role, source.location = fromOffsite, current.config.Repositories.Offsite
		source.restic.Repo = source.location
		if err := a.useRclone(current.layout, source.restic); err != nil {
			return nil, err
		}
	}
	return source, nil
}

func (a *app) openNamedRepo(ctx context.Context, source *restoreSource, options restoreOptions) (*restoreSource, error) {
	layout, err := config.Resolve()
	if err != nil {
		return nil, err
	}
	binary, err := a.resticForRestore(ctx, layout)
	if err != nil {
		return nil, err
	}
	location := options.repo
	if filepath.IsAbs(location) {
		location = filepath.Clean(location)
	}
	source.role, source.location = fromRepo, location
	source.restic = &engine.Restic{Binary: binary, Repo: location, CacheDir: layout.CacheDir(), RetryLock: restoreLockWait}
	if err := a.useRclone(layout, source.restic); err != nil {
		return nil, err
	}
	password, err := a.typedPassword(options.passwordStdin)
	if err != nil {
		return nil, err
	}
	source.restic.UseTypedPassword(password)
	if err := source.restic.Probe(ctx); err != nil {
		if engine.IsKind(err, engine.KindWrongPassword) {
			return nil, i18n.E("restore.wrong_password").Hint("restore.wrong_password.hint")
		}
		return nil, err
	}
	source.base = filepath.Join(source.env.Home, "BackUpClaude")
	if filepath.IsAbs(location) {
		source.base = filepath.Dir(location)
	}
	return source, nil
}

// resticForRestore finds restic, and on a computer that has none yet installs
// the pinned version: a restore must not depend on running setup first. The
// download is checked against the hash in tools.lock.json.
func (a *app) resticForRestore(ctx context.Context, layout config.Layout) (string, error) {
	binary, err := a.resticBinary(layout)
	if err == nil || !engine.IsNotInstalled(err) {
		return binary, err
	}
	installer, err := a.installer(layout)
	if err != nil {
		return "", err
	}
	if !a.jsonOut {
		fmt.Fprintln(a.stderr, a.tr.T("restore.installing_restic"))
	}
	tool, err := installer.Install(ctx, engine.ToolRestic)
	if err != nil {
		return "", err
	}
	return tool.Path, nil
}

// useRclone points restic at the pinned rclone and the rclone settings in the
// data folder, for an rclone: repository.
func (a *app) useRclone(layout config.Layout, restic *engine.Restic) error {
	if !strings.HasPrefix(restic.Repo, "rclone:") {
		return nil
	}
	installer, err := a.installer(layout)
	if err != nil {
		return err
	}
	tool, err := installer.Locate(engine.ToolRclone)
	if err != nil {
		return err
	}
	restic.Options = append(restic.Options, "rclone.program="+tool.Path)
	if conf := filepath.Join(layout.Root, "rclone.conf"); fileExists(conf) {
		restic.ExtraEnv = append(restic.ExtraEnv, "RCLONE_CONFIG="+conf)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// typedPassword reads the repository password the person types. At a terminal
// nothing is echoed; with --password-stdin the first line of the input is
// taken, for scripts. The value is never printed or logged.
func (a *app) typedPassword(fromStdin bool) (string, error) {
	var password string
	switch {
	case fromStdin:
		line, err := bufio.NewReader(a.stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", i18n.E("restore.password_read_failed").Because(err)
		}
		password = strings.TrimRight(line, "\r\n")
	case a.deps.terminal() && a.deps.readSecret != nil:
		fmt.Fprint(a.stderr, a.tr.T("restore.password_prompt"), " ")
		line, err := a.deps.readSecret()
		fmt.Fprintln(a.stderr)
		if err != nil {
			return "", i18n.E("restore.password_read_failed").Because(err)
		}
		password = line
	default:
		return "", i18n.E("restore.password_needed").Hint("restore.password_needed.hint").Exit(exitUsage)
	}
	password = strings.TrimSpace(password)
	if password == "" {
		return "", i18n.E("restore.password_needed").Hint("restore.password_needed.hint").Exit(exitUsage)
	}
	return password, nil
}

// snapshotRef names a snapshot in the answer.
type snapshotRef struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Host    string    `json:"host"`
	Profile string    `json:"profile"`
}

func refOf(snapshot engine.Snapshot) snapshotRef {
	return snapshotRef{ID: snapshot.ShortID, Time: snapshot.Time, Host: snapshot.Hostname, Profile: snapshotProfile(snapshot)}
}

// applyReport is what putting files back did or would do.
type applyReport struct {
	DryRun  bool           `json:"dry_run"`
	Planned int            `json:"planned"`
	Copied  int            `json:"copied"`
	Bytes   int64          `json:"bytes"`
	Moves   []restore.Pair `json:"moves,omitempty"`
	// Rewritten counts Claude's files whose paths were moved.
	Rewritten int `json:"rewritten"`
	// Existing files stay as they are; Differ of them hold other content than
	// the copy.
	Existing int `json:"existing"`
	Differ   int `json:"differ"`
	// Skipped counts files without a place here, by reason.
	Skipped  map[string]int    `json:"skipped,omitempty"`
	Examples []restore.Skipped `json:"examples,omitempty"`
	Written  []string          `json:"written,omitempty"`
	Running  []sysproc.Process `json:"running,omitempty"`
	Locked   bool              `json:"locked,omitempty"`
	Copies   []restore.Copy    `json:"-"`
}

// resumeHint says how to open a restored session.
type resumeHint struct {
	ID      string `json:"id"`
	Cwd     string `json:"cwd,omitempty"`
	Command string `json:"command"`
	Desktop bool   `json:"desktop,omitempty"`
}

type restoreReport struct {
	Mode      string           `json:"mode"`
	Source    string           `json:"source"`
	Location  string           `json:"location"`
	Snapshots []snapshotRef    `json:"snapshots"`
	Sessions  []string         `json:"sessions,omitempty"`
	Project   string           `json:"project,omitempty"`
	Pieces    []restore.Piece  `json:"pieces"`
	Fetched   *restore.Fetched `json:"restored"`
	Apply     *applyReport     `json:"apply,omitempty"`
	Resume    []resumeHint     `json:"resume,omitempty"`
	Notes     []string         `json:"notes,omitempty"`
}

// runRestore finds what to bring back, restores it into a folder of its own,
// and with --apply puts the missing files back in place.
func (a *app) runRestore(ctx context.Context, source *restoreSource, mode string, options restoreOptions) (*restoreReport, error) {
	snapshots, err := source.restic.Snapshots(ctx, "")
	if err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return nil, i18n.E("restore.no_snapshots", source.location).Hint("restore.no_snapshots.hint")
	}
	report := &restoreReport{Mode: mode, Source: source.role, Location: safeLocation(source.location)}
	var used []engine.Snapshot
	var descriptions []string
	switch mode {
	case restore.ModeSession:
		match, err := restore.FindSession(ctx, source.restic, source.manifest, snapshots, options.session, options.snapshot)
		if err != nil {
			return nil, err
		}
		used = append(used, match.Snapshot)
		report.Pieces, report.Sessions, descriptions = match.Pieces, match.IDs, match.Descriptions
	case restore.ModeProject:
		snapshot, err := restore.Pick(snapshots, options.snapshot, restore.TagProjects)
		if err != nil {
			return nil, err
		}
		root, err := restore.FindProject(*snapshot, options.project)
		if err != nil {
			return nil, err
		}
		pieces, err := restore.PiecesOf(ctx, source.restic, *snapshot, restore.TagProjects, []string{root})
		if err != nil {
			return nil, err
		}
		if len(pieces) == 0 {
			return nil, i18n.E("restore.project_not_found", options.project).Hint("restore.project_not_found.hint")
		}
		used = append(used, *snapshot)
		report.Pieces, report.Project = pieces, restore.LocalPath(root)
	default:
		chosen, err := snapshotsForAll(snapshots, options.snapshot)
		if err != nil {
			return nil, err
		}
		for _, snapshot := range chosen {
			pieces, err := restore.PiecesOf(ctx, source.restic, snapshot, snapshotProfile(snapshot), restore.RootsOf(snapshot))
			if err != nil {
				return nil, err
			}
			report.Pieces = append(report.Pieces, pieces...)
			used = append(used, snapshot)
		}
	}
	for _, snapshot := range used {
		report.Snapshots = append(report.Snapshots, refOf(snapshot))
	}

	target := options.to
	if target == "" {
		target = restore.DefaultTarget(source.base, time.Now().Format(stampLayout))
	} else if target, err = filepath.Abs(target); err != nil {
		return nil, i18n.E("restore.target_failed", options.to).Because(err)
	}
	report.Fetched, err = restore.Fetch(ctx, source.restic, report.Pieces, target)
	if err != nil {
		return nil, err
	}
	if mode == restore.ModeSession {
		if err := a.fetchLinkedTranscripts(ctx, source, snapshots, used[0], descriptions, target, report); err != nil {
			return nil, err
		}
	}
	if options.apply {
		if report.Apply, err = a.applyRestore(source, snapshots, used[0], target, report.Pieces, options); err != nil {
			return report, err
		}
	}
	if mode == restore.ModeSession {
		report.Resume = a.resumeHints(source, snapshots, used[0], target, report, options)
	}
	return report, nil
}

// snapshotsForAll is the newest snapshot of each profile, or the one named.
func snapshotsForAll(snapshots []engine.Snapshot, id string) ([]engine.Snapshot, error) {
	if id != "" {
		picked, err := restore.Pick(snapshots, id, "")
		if err != nil {
			return nil, err
		}
		return []engine.Snapshot{*picked}, nil
	}
	var chosen []engine.Snapshot
	for _, tag := range []string{restore.TagClaude, restore.TagProjects} {
		if latest := restore.Latest(snapshots, tag); latest != nil {
			chosen = append(chosen, *latest)
		}
	}
	if len(chosen) == 0 {
		return nil, i18n.E("restore.no_snapshots", restore.TagClaude).Hint("restore.no_snapshots.hint")
	}
	return chosen, nil
}

// fetchLinkedTranscripts brings the Claude Code transcript behind a session of
// the desktop application: its description names it as cliSessionId.
func (a *app) fetchLinkedTranscripts(ctx context.Context, source *restoreSource, snapshots []engine.Snapshot, snapshot engine.Snapshot, descriptions []string, target string, report *restoreReport) error {
	for _, description := range descriptions {
		data, err := os.ReadFile(restore.StagedPath(target, description))
		if err != nil {
			continue
		}
		var meta struct {
			CLI string `json:"cliSessionId"`
		}
		if json.Unmarshal(data, &meta) != nil || meta.CLI == "" || slices.Contains(report.Sessions, strings.ToLower(meta.CLI)) {
			continue
		}
		match, err := restore.FindSession(ctx, source.restic, source.manifest, snapshots, meta.CLI, snapshot.ID)
		if err != nil {
			report.Notes = append(report.Notes, "linked_transcript_missing")
			continue
		}
		fetched, err := restore.Fetch(ctx, source.restic, match.Pieces, target)
		if err != nil {
			return err
		}
		report.Fetched = fetched
		report.Pieces = append(report.Pieces, match.Pieces...)
		report.Sessions = append(report.Sessions, match.IDs...)
	}
	return nil
}

// placement works out where the data of the snapshot belongs on this computer:
// the home folder of the snapshot's computer becomes this home folder, the
// desktop application's folder becomes the one here, and --map moves the rest.
func (a *app) placement(source *restoreSource, snapshots []engine.Snapshot, snapshot engine.Snapshot, maps []string) (restore.Placement, error) {
	origin := restore.Origin{Host: snapshot.Hostname}
	if slices.Contains(snapshot.Tags, restore.TagClaude) {
		origin = restore.OriginOf(source.manifest, snapshot)
	} else if claude := latestOnHost(snapshots, restore.TagClaude, snapshot.Hostname); claude != nil {
		origin = restore.OriginOf(source.manifest, *claude)
	}
	var pairs []restore.Pair
	for _, item := range maps {
		from, to, ok := strings.Cut(item, "=")
		if !ok || !filepath.IsAbs(strings.TrimSpace(from)) || !filepath.IsAbs(strings.TrimSpace(to)) {
			return restore.Placement{}, i18n.E("restore.bad_map", item).Hint("restore.bad_map.hint").Exit(exitUsage)
		}
		pairs = append(pairs, restore.Pair{From: strings.TrimSpace(from), To: strings.TrimSpace(to)})
	}
	found := discover.Locate(source.manifest, source.env)
	var blocked []string
	for _, root := range origin.Desktop {
		old := restore.LocalPath(root)
		switch {
		case len(found.Desktop) == 0:
			blocked = append(blocked, old)
		case desktopHere(found.Desktop, old):
			// The same folder exists here: nothing moves.
		default:
			pairs = append(pairs, restore.Pair{From: old, To: found.Desktop[0].Path})
		}
	}
	if origin.Home != "" {
		pairs = append(pairs, restore.Pair{From: restore.LocalPath(origin.Home), To: source.env.Home})
	}
	mapper := restore.NewMapper(pairs)
	for _, folder := range blocked {
		mapper.Block(folder)
	}
	return restore.Placement{Manifest: source.manifest, Origin: origin, Mapper: mapper}, nil
}

func desktopHere(found []discover.Location, path string) bool {
	for _, location := range found {
		if restore.WithinLocal(location.Path, path) && restore.WithinLocal(path, location.Path) {
			return true
		}
	}
	return false
}

func latestOnHost(snapshots []engine.Snapshot, tag, host string) *engine.Snapshot {
	var latest *engine.Snapshot
	for i := range snapshots {
		s := &snapshots[i]
		if s.Hostname == host && slices.Contains(s.Tags, tag) && (latest == nil || s.Time.After(latest.Time)) {
			latest = s
		}
	}
	return latest
}

// applyRestore puts back the files that are missing. Before anything is
// written it refuses to touch the databases of a running desktop application:
// the application keeps them open and would write its own state over them.
func (a *app) applyRestore(source *restoreSource, snapshots []engine.Snapshot, snapshot engine.Snapshot, target string, pieces []restore.Piece, options restoreOptions) (*applyReport, error) {
	place, err := a.placement(source, snapshots, snapshot, options.maps)
	if err != nil {
		return nil, err
	}
	plan, err := restore.PlanApply(target, pieces, place)
	if err != nil {
		return nil, err
	}
	report := &applyReport{DryRun: options.dryRun, Planned: len(plan.Copies), Moves: place.Mapper.Pairs(), Skipped: map[string]int{}, Copies: plan.Copies}
	for _, skipped := range plan.Skipped {
		if skipped.Reason == restore.SkipExists {
			report.Existing++
			if skipped.Differs {
				report.Differ++
			}
			continue
		}
		report.Skipped[skipped.Reason]++
		if len(report.Examples) < maxExamples {
			report.Examples = append(report.Examples, skipped)
		}
	}
	if plan.Locked() {
		report.Locked = true
		running, err := a.desktopRunning(source.manifest)
		if err != nil || len(running) > 0 {
			report.Running = running
			return report, i18n.E("restore.claude_running", target).Hint("restore.claude_running.hint")
		}
	}
	for _, item := range plan.Copies {
		if len(report.Written) < maxExamples {
			report.Written = append(report.Written, item.To)
		}
	}
	if options.dryRun {
		return report, nil
	}
	done, err := restore.Apply(plan, place.Mapper)
	if done != nil {
		report.Copied, report.Bytes, report.Rewritten = done.Copied, done.Bytes, done.Rewritten
	}
	return report, err
}

const maxExamples = 10

// desktopRunning lists the processes of the desktop application.
func (a *app) desktopRunning(manifest *manifests.Manifest) ([]sysproc.Process, error) {
	find := a.deps.running
	if find == nil {
		find = sysproc.Find
	}
	return find(manifest.Desktop.Processes.Names, manifest.Desktop.Processes.PathMarkers)
}

// resumeHints tell how to open each restored Claude Code session: from the
// folder it worked in, as that folder is called on this computer.
func (a *app) resumeHints(source *restoreSource, snapshots []engine.Snapshot, snapshot engine.Snapshot, target string, report *restoreReport, options restoreOptions) []resumeHint {
	place, err := a.placement(source, snapshots, snapshot, options.maps)
	if err != nil {
		return nil
	}
	transcripts := place.Origin.Transcripts(source.manifest)
	var hints []resumeHint
	for _, piece := range report.Pieces {
		if transcripts == "" || !restore.Within(transcripts, piece.Root) {
			continue
		}
		for _, name := range piece.Names {
			id, ok := strings.CutSuffix(strings.ToLower(name), transcriptSuffix)
			if !ok {
				continue
			}
			hint := resumeHint{ID: id, Command: "claude --resume " + id}
			if file, err := os.Open(filepath.Join(restore.StagedPath(target, piece.Root), name)); err == nil {
				if cwd, err := discover.FirstCwd(file); err == nil && cwd != "" {
					if mapped, ok := place.Mapper.Map(cwd); ok {
						hint.Cwd = mapped
					}
				}
				_ = file.Close()
			}
			hints = append(hints, hint)
		}
	}
	for _, piece := range report.Pieces {
		for _, root := range place.Origin.Desktop {
			if restore.Within(root, piece.Root) {
				hints = append(hints, resumeHint{ID: strings.Join(report.Sessions, ","), Desktop: true})
				return hints
			}
		}
	}
	return hints
}

func (a *app) printRestore(w io.Writer, report *restoreReport) {
	fmt.Fprintln(w, a.tr.T("restore.source."+report.Source, report.Location))
	for _, snapshot := range report.Snapshots {
		fmt.Fprintln(w, a.tr.T("restore.snapshot", snapshot.ID, snapshot.Time.Local().Format("02.01.2006 15:04"), snapshot.Profile, snapshot.Host))
	}
	switch report.Mode {
	case restore.ModeSession:
		fmt.Fprintln(w, a.tr.T("restore.found_session", strings.Join(report.Sessions, ", "), len(report.Pieces)))
	case restore.ModeProject:
		fmt.Fprintln(w, a.tr.T("restore.found_project", report.Project))
	default:
		fmt.Fprintln(w, a.tr.T("restore.found_all", len(report.Pieces)))
	}
	if report.Fetched != nil {
		fmt.Fprintln(w, a.tr.T("restore.fetched", report.Fetched.Target, report.Fetched.Files, a.formatSize(report.Fetched.Bytes)))
		fmt.Fprintln(w, "  "+a.tr.T("restore.fetched_verified", report.Fetched.Digest))
	}
	for _, note := range report.Notes {
		fmt.Fprintln(w, a.tr.T("restore.note."+note))
	}
	if apply := report.Apply; apply != nil {
		a.printApply(w, apply)
	}
	for _, hint := range report.Resume {
		if hint.Desktop {
			fmt.Fprintln(w, a.tr.T("restore.resume_desktop"))
			continue
		}
		if hint.Cwd != "" {
			fmt.Fprintln(w, a.tr.T("restore.resume", hint.Cwd, hint.Command))
		} else {
			fmt.Fprintln(w, a.tr.T("restore.resume_no_cwd", hint.Command))
		}
	}
	if report.Apply == nil {
		fmt.Fprintln(w, a.tr.T("restore.next_apply"))
	}
}

func (a *app) printApply(w io.Writer, apply *applyReport) {
	for _, move := range apply.Moves {
		fmt.Fprintln(w, "  "+a.tr.T("restore.apply.move", move.From, move.To))
	}
	if apply.DryRun {
		fmt.Fprintln(w, a.tr.T("restore.apply.planned", apply.Planned))
	} else {
		fmt.Fprintln(w, a.tr.T("restore.apply.copied", apply.Copied, a.formatSize(apply.Bytes)))
	}
	for _, path := range apply.Written {
		fmt.Fprintln(w, "    "+path)
	}
	if apply.Planned > len(apply.Written) {
		fmt.Fprintln(w, "    "+a.tr.T("restore.apply.more", apply.Planned-len(apply.Written)))
	}
	fmt.Fprintln(w, a.tr.T("restore.apply.existing", apply.Existing, apply.Differ))
	reasons := make([]string, 0, len(apply.Skipped))
	for reason := range apply.Skipped {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Fprintln(w, a.tr.T("restore.apply.skipped."+reason, apply.Skipped[reason]))
	}
	for _, example := range apply.Examples {
		fmt.Fprintln(w, "    "+example.Path)
	}
	if apply.Rewritten > 0 {
		fmt.Fprintln(w, a.tr.T("restore.apply.rewritten", apply.Rewritten))
	}
}
