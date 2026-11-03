package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	backupclaude "github.com/peregoood-ru/BackUpClaude"
	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/disk"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/notify"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
	"github.com/peregoood-ru/BackUpClaude/internal/selfinstall"
	"github.com/peregoood-ru/BackUpClaude/internal/skill"
	"github.com/peregoood-ru/BackUpClaude/internal/syncdir"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// setupOptions are the flags of setup. Every flag answers a question of the
// wizard, so that a script can run it without a person.
type setupOptions struct {
	yes                  bool
	local, offsite       string
	noOffsite            bool
	telegramChat         string
	telegramStdin        bool
	webhookStdin         bool
	noNotify             bool
	skill, noSkill       bool
	install, noInstall   bool
	snapshot, noSnapshot bool
	projectsNow          bool
}

// Gigabyte in bytes for the space checks (binary, like the disks report it).
const gibibyte = 1 << 30

// A local copy of Claude's data and the projects needs room; below this the
// wizard warns.
const lowSpaceBytes = 20 * gibibyte

// A disk with at least this much free space is offered for the local copy.
const roomyDriveBytes = 30 * gibibyte

// Statuses of a setup step.
const (
	stepOK      = "ok"
	stepChanged = "changed"
	stepKept    = "kept"
	stepSkipped = "skipped"
	stepWarn    = "warn"
	stepFail    = "fail"
)

const setupSteps = 8

func (a *app) newSetupCommand() *cobra.Command {
	var o setupOptions
	cmd := &cobra.Command{
		Use:   "setup",
		Short: a.tr.T("cli.setup.short"),
		Long:  a.tr.T("cli.setup.long"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.setup(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&o.yes, "yes", "y", false, a.tr.T("flag.setup.yes"))
	f.StringVar(&o.local, "local", "", a.tr.T("flag.setup.local"))
	f.StringVar(&o.offsite, "offsite", "", a.tr.T("flag.setup.offsite"))
	f.BoolVar(&o.noOffsite, "no-offsite", false, a.tr.T("flag.setup.no_offsite"))
	f.StringVar(&o.telegramChat, "telegram-chat-id", "", a.tr.T("flag.setup.telegram_chat"))
	f.BoolVar(&o.telegramStdin, "telegram-token-stdin", false, a.tr.T("flag.setup.telegram_stdin"))
	f.BoolVar(&o.webhookStdin, "webhook-url-stdin", false, a.tr.T("flag.setup.webhook_stdin"))
	f.BoolVar(&o.noNotify, "no-notify", false, a.tr.T("flag.setup.no_notify"))
	f.BoolVar(&o.skill, "skill", false, a.tr.T("flag.setup.skill"))
	f.BoolVar(&o.noSkill, "no-skill", false, a.tr.T("flag.setup.no_skill"))
	f.BoolVar(&o.install, "install", false, a.tr.T("flag.setup.install"))
	f.BoolVar(&o.noInstall, "no-install", false, a.tr.T("flag.setup.no_install"))
	f.BoolVar(&o.snapshot, "snapshot", false, a.tr.T("flag.setup.snapshot"))
	f.BoolVar(&o.noSnapshot, "no-snapshot", false, a.tr.T("flag.setup.no_snapshot"))
	f.BoolVar(&o.projectsNow, "projects-now", false, a.tr.T("flag.setup.projects_now"))
	return cmd
}

// setupStep is one line of the wizard's report.
type setupStep struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type passwordReport struct {
	Stored  bool `json:"stored"`
	Created bool `json:"created"`
	// Shown is true when the person asked for the password on the screen.
	Shown bool `json:"shown"`
}

// setupReport is the answer of setup. It holds no secret.
type setupReport struct {
	Rerun         bool            `json:"rerun"`
	ConfigChanged bool            `json:"config_changed"`
	ConfigFile    string          `json:"config_file"`
	Local         string          `json:"local_repository"`
	Offsite       string          `json:"offsite_repository,omitempty"`
	Program       *programReport  `json:"program,omitempty"`
	Tools         []toolInfo      `json:"tools,omitempty"`
	Password      passwordReport  `json:"password"`
	Tasks         []scheduleJob   `json:"tasks,omitempty"`
	Skill         *skill.State    `json:"skill,omitempty"`
	Snapshots     []profileResult `json:"snapshots,omitempty"`
	Steps         []setupStep     `json:"steps"`
}

type toolInfo struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
}

// answers collects what the person decided.
type answers struct {
	local   string
	offsite string
	// chat and token belong to the Telegram channel, webhook to the webhook one.
	chat, token string
	webhook     string
}

type setupRun struct {
	a           *app
	ctx         context.Context
	o           setupOptions
	interactive bool
	p           *prompter
	report      setupReport
	layout      config.Layout
	env         discover.Env
	// exe is the program the tasks and the password command will run.
	exe string
	// installed are the scheduler's tasks found at the start; schedulerOK is
	// false where the OS has no scheduler support yet.
	installed   []schedule.Info
	schedulerOK bool
	claudeHome  discover.Location
	failed      bool
}

func (a *app) setup(ctx context.Context, o setupOptions) error {
	for _, pair := range [][2]bool{{o.install, o.noInstall}, {o.skill, o.noSkill}, {o.snapshot, o.noSnapshot}, {o.offsite != "", o.noOffsite}} {
		if pair[0] && pair[1] {
			return i18n.E("setup.conflicting_flags").Hint("cli.bad_flag.hint").Exit(exitUsage)
		}
	}
	interactive := !o.yes && !a.jsonOut && a.deps.terminal()
	if !o.yes && !interactive {
		return i18n.E("setup.needs_terminal").Hint("setup.needs_terminal.hint").Exit(exitUsage)
	}
	r := &setupRun{a: a, ctx: ctx, o: o, interactive: interactive, p: a.newPrompter()}
	return r.run()
}

// say writes a line for a person; a JSON answer has no free text around it.
func (r *setupRun) say(key string, args ...any) {
	if !r.a.jsonOut {
		fmt.Fprintln(r.a.stdout, r.a.tr.T(key, args...))
	}
}

func (r *setupRun) heading(number int, key string) {
	if !r.a.jsonOut {
		fmt.Fprintln(r.a.stdout)
		fmt.Fprintln(r.a.stdout, r.a.tr.T("setup.heading", number, setupSteps, r.a.tr.T(key)))
	}
}

// step records a result and shows it.
func (r *setupRun) step(id, status, message, hint string) {
	r.report.Steps = append(r.report.Steps, setupStep{ID: id, Status: status, Message: message, Hint: hint})
	if status == stepFail {
		r.failed = true
	}
	if r.a.jsonOut {
		return
	}
	fmt.Fprintf(r.a.stdout, "  [%s] %s\n", r.a.tr.T("setup.status."+status), message)
	if hint != "" {
		fmt.Fprintf(r.a.stdout, "        %s %s\n", r.a.tr.T("cli.hint_prefix"), hint)
	}
}

// stepErr records a failed step from an error.
func (r *setupRun) stepErr(id, status string, err error) {
	d := r.a.tr.Describe(err)
	message := d.Message
	if len(d.Details) > 0 {
		message += " (" + strings.Join(d.Details, "; ") + ")"
	}
	r.step(id, status, message, d.Hint)
}

func (r *setupRun) run() error {
	a := r.a
	layout, err := config.Resolve()
	if err != nil {
		return err
	}
	r.layout = layout
	existing, loadErr := config.Load(layout.ConfigFile())
	switch {
	case loadErr == nil:
		r.report.Rerun = true
	case config.IsMissing(loadErr):
		existing = nil
	default:
		// A damaged file is never replaced: it may hold settings worth saving.
		return loadErr
	}
	r.report.ConfigFile = layout.ConfigFile()

	r.say("setup.welcome")
	if r.report.Rerun {
		r.say("setup.rerun", layout.ConfigFile())
	}

	r.heading(1, "setup.step.environment")
	if err := r.environment(); err != nil {
		return err
	}
	r.heading(2, "setup.step.program")
	if err := r.program(); err != nil {
		return err
	}
	r.heading(3, "setup.step.tools")
	if err := r.tools(); err != nil {
		return err
	}
	r.heading(4, "setup.step.questions")
	given, err := r.questions(existing)
	if err != nil {
		return err
	}
	r.heading(5, "setup.step.settings")
	cfg, err := r.settings(existing, given)
	if err != nil {
		return err
	}
	current, repo, err := r.repository()
	if err != nil {
		return err
	}
	r.heading(6, "setup.step.schedule")
	if err := r.schedule(cfg); err != nil {
		return err
	}
	r.heading(7, "setup.step.skill")
	r.skill()
	r.heading(8, "setup.step.snapshot")
	r.snapshot(current, repo)
	r.paper(repo)

	if r.failed {
		r.say("setup.finished_with_problems")
	} else {
		r.say("setup.finished")
		r.say("setup.next_commands")
	}
	if a.jsonOut {
		if err := a.writeJSON(envelope{OK: !r.failed, Command: a.command, Data: r.report}); err != nil {
			return err
		}
	}
	if r.failed {
		return errReported
	}
	return nil
}

// environment checks what the application depends on. A problem that makes the
// rest pointless stops the wizard; the others are warnings.
func (r *setupRun) environment() error {
	a := r.a
	lock, err := engine.ParseLock(backupclaude.ToolsLock)
	if err != nil {
		return err
	}
	if _, _, err := lock.Asset(engine.ToolRestic, engine.Platform()); err != nil {
		return err
	}
	r.step("platform", stepOK, a.tr.T("setup.env.platform", version, runtime.GOOS, runtime.GOARCH), "")

	env, err := a.deps.env()
	if err != nil {
		return i18n.E("cli.no_home").Because(err)
	}
	r.env = env

	if err := os.MkdirAll(r.layout.Root, 0o700); err != nil {
		e := i18n.E("setup.env.data_dir_failed", r.layout.Root).Hint("doctor.data_dir.readonly.hint").Because(err)
		return e
	}
	probe, err := os.CreateTemp(r.layout.Root, ".setup-*")
	if err != nil {
		e := i18n.E("setup.env.data_dir_failed", r.layout.Root).Hint("doctor.data_dir.readonly.hint").Because(err)
		return e
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	r.step("data_dir", stepOK, a.tr.T("doctor.data_dir.ok", r.layout.Root), "")

	if err := r.probeKeyStore(); err != nil {
		return err
	}
	r.step("key_store", stepOK, a.tr.T("setup.env.key_store"), "")

	manifest, err := manifests.Load(runtime.GOOS)
	if err != nil {
		return err
	}
	r.claudeHome = discover.Locate(manifest, env).Home
	if r.claudeHome.Exists {
		r.step("claude_data", stepOK, a.tr.T("doctor.claude.ok", r.claudeHome.Path), "")
	} else {
		r.step("claude_data", stepWarn, a.tr.T("doctor.claude.absent", r.claudeHome.Path), a.tr.T("doctor.claude.absent.hint"))
	}

	installed, err := a.scheduler(r.layout).List()
	if err != nil {
		r.schedulerOK = false
		r.stepErr("scheduler", stepWarn, err)
		return nil
	}
	r.schedulerOK, r.installed = true, installed
	r.step("scheduler", stepOK, a.tr.T("setup.env.scheduler", len(installed)), "")
	return nil
}

// probeKeyStore writes, reads and deletes a value: the password goes there
// later, and a store that does not work must be found now.
func (r *setupRun) probeKeyStore() error {
	store := r.a.deps.secrets()
	const key, value = "setup-probe", "probe"
	if err := store.Set(key, value); err != nil {
		return err
	}
	got, err := store.Get(key)
	_ = store.Delete(key)
	if err != nil {
		return err
	}
	if got != value {
		return i18n.E("secrets.verify_failed").Hint("secrets.unavailable.hint")
	}
	return nil
}

// existingTaskProgram returns the program the application's tasks already run,
// when that file exists.
func (r *setupRun) existingTaskProgram() string {
	for _, info := range r.installed {
		if info.Command == "" {
			continue
		}
		if _, err := os.Stat(info.Command); err == nil {
			return info.Command
		}
	}
	return ""
}

// program decides where the program lives, and puts it there.
func (r *setupRun) program() error {
	a := r.a
	current, err := a.deps.executable()
	if err != nil {
		return i18n.E("cli.no_executable").Because(err)
	}
	r.exe = current
	target := selfinstall.Target(r.layout.BinDir())
	heldByTasks := r.existingTaskProgram()

	var place bool
	switch {
	case r.o.noInstall:
		place = false
	case r.o.install, selfinstall.SameFile(current, target):
		place = true
	case heldByTasks != "":
		// A working installation keeps its program where it is. Moving it is a
		// choice (--install), not a side effect of running the wizard again.
		place = false
	case r.interactive:
		place, err = r.p.yesNo(a.tr.T("setup.q.install", target), true)
		if err != nil {
			return err
		}
	default:
		place = true
	}
	if !place {
		if heldByTasks != "" && !r.o.noInstall {
			r.exe = heldByTasks
			r.step("program", stepKept, a.tr.T("setup.program.kept", heldByTasks), "")
		} else {
			r.step("program", stepSkipped, a.tr.T("setup.program.skipped", current), a.tr.T("setup.program.skipped.hint"))
		}
		return nil
	}
	report, err := a.installProgram(r.layout, true)
	if err != nil {
		return err
	}
	r.report.Program = &report
	r.exe = report.Path
	// From here on the installed copy is the program: the tasks, the password
	// command and the skill point to it.
	a.deps.executable = func() (string, error) { return report.Path, nil }
	status := stepOK
	if report.Placed {
		status = stepChanged
	}
	r.step("program", status, a.tr.T("setup.program.placed", report.Path), "")
	switch {
	case report.PathChanged:
		r.step("path", stepChanged, a.tr.T("install.path_added", r.layout.BinDir()), a.tr.T("setup.path.new_terminal"))
	case report.InPath:
		r.step("path", stepOK, a.tr.T("install.path_present"), "")
	case !report.PathSupported:
		r.step("path", stepWarn, a.tr.T("install.path_manual", r.layout.BinDir()), "")
	}
	return nil
}

// tools installs restic and rclone of the pinned versions.
func (r *setupRun) tools() error {
	a := r.a
	if a.deps.resticPath != nil {
		r.step("tools", stepSkipped, a.tr.T("setup.tools.provided"), "")
		return nil
	}
	installer, err := a.installer(r.layout)
	if err != nil {
		return err
	}
	for _, name := range []string{engine.ToolRestic, engine.ToolRclone} {
		if have, err := installer.Locate(name); err == nil {
			r.report.Tools = append(r.report.Tools, toolInfo{have.Tool, have.Version})
			r.step("tool_"+name, stepOK, a.tr.T("setup.tools.present", name, have.Version), "")
			continue
		}
		r.say("setup.tools.downloading", name)
		tool, err := installer.Install(r.ctx, name)
		if err != nil {
			// restic is the engine of every copy. rclone matters only for an
			// external copy through it.
			if name == engine.ToolRestic {
				return err
			}
			r.stepErr("tool_"+name, stepWarn, err)
			continue
		}
		r.report.Tools = append(r.report.Tools, toolInfo{tool.Tool, tool.Version})
		r.step("tool_"+name, stepChanged, a.tr.T("setup.tools.installed", name, tool.Version), "")
	}
	return nil
}

// ---- questions ----

func (r *setupRun) questions(existing *config.Config) (answers, error) {
	var given answers
	var err error
	if given.local, err = r.askLocal(existing); err != nil {
		return given, err
	}
	if given.offsite, err = r.askOffsite(existing); err != nil {
		return given, err
	}
	if err := r.askNotify(existing, &given); err != nil {
		return given, err
	}
	return given, nil
}

// suggestLocal proposes a folder for the local copy: on the roomiest disk that
// is not the one Windows lives on, since a copy next to the original does not
// survive the disk.
func (r *setupRun) suggestLocal() string {
	var best *disk.Drive
	drives := r.a.driveList()
	for i := range drives {
		d := &drives[i]
		if d.System || d.Free < roomyDriveBytes {
			continue
		}
		if best == nil || d.Free > best.Free {
			best = d
		}
	}
	if best != nil {
		return filepath.Join(best.Root, "ClaudeBackup", "repo")
	}
	return filepath.Join(r.env.Home, "ClaudeBackup", "repo")
}

func (r *setupRun) askLocal(existing *config.Config) (string, error) {
	a := r.a
	def := r.suggestLocal()
	if existing != nil {
		def = existing.Repositories.Local
	}
	keep := ""
	if existing != nil {
		keep = existing.Repositories.Local
	}
	if r.o.local != "" {
		path, err := r.checkLocal(r.o.local, keep)
		if err != nil {
			return "", err
		}
		return r.confirmLocal(existing, path)
	}
	if !r.interactive {
		path, err := r.checkLocal(def, keep)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := r.p.ask(a.tr.T("setup.q.local"), def)
		if err != nil {
			return "", err
		}
		path, err := r.checkLocal(answer, keep)
		if err != nil {
			r.stepErr("local", stepWarn, err)
			continue
		}
		return r.confirmLocal(existing, path)
	}
	return "", i18n.E("setup.local_gave_up").Hint("setup.local_gave_up.hint", def)
}

// confirmLocal asks before moving a working installation to another folder.
func (r *setupRun) confirmLocal(existing *config.Config, path string) (string, error) {
	if existing == nil || sameFolder(existing.Repositories.Local, path) {
		return path, nil
	}
	if r.interactive {
		ok, err := r.p.yesNo(r.a.tr.T("setup.q.local_change", existing.Repositories.Local, path), false)
		if err != nil {
			return "", err
		}
		if !ok {
			return existing.Repositories.Local, nil
		}
	}
	r.step("local_change", stepWarn, r.a.tr.T("setup.local.changed", existing.Repositories.Local, path), r.a.tr.T("setup.local.changed.hint"))
	return path, nil
}

func sameFolder(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// checkLocal turns an answer into the path of the local copy, or says why the
// place does not do. Folders of the sync services are refused: they would
// copy a damaged or deleted file to the cloud and write the repository all day.
//
// keep is the place the settings already hold. A working installation is not
// stopped by it even if it stands in a sync folder: that is reported as a
// warning, and the person decides.
func (r *setupRun) checkLocal(answer, keep string) (string, error) {
	a := r.a
	path, ok := r.env.Expand(strings.TrimSpace(answer))
	if !ok || path == "" {
		return "", i18n.E("setup.local_bad", answer).Hint("setup.local_bad.hint")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", i18n.E("setup.local_bad", answer).Hint("setup.local_bad.hint").Because(err)
	}
	if match, found := syncdir.Detect(path, a.syncFolders()); found {
		problem := i18n.E("setup.local_in_sync", path, a.tr.T("setup.service."+match.Service)).Hint("setup.local_in_sync.hint", r.suggestLocal())
		if keep == "" || !sameFolder(keep, path) {
			return "", problem
		}
		r.stepErr("local", stepWarn, problem)
	}
	if isInside(path, r.layout.Root) && (keep == "" || !sameFolder(keep, path)) {
		return "", i18n.E("setup.local_inside_data", path, r.layout.Root).Hint("setup.local_bad.hint")
	}
	info, err := os.Stat(path)
	switch {
	case err == nil && !info.IsDir():
		return "", i18n.E("setup.local_is_file", path).Hint("setup.local_bad.hint")
	case err == nil:
		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return "", i18n.E("setup.local_unreadable", path).Because(readErr)
		}
		if len(entries) > 0 && !hasEntry(entries, "config") {
			return "", i18n.E("setup.local_not_empty", path).Hint("setup.local_not_empty.hint")
		}
	}
	r.warnAboutPlace(path)
	return path, nil
}

func hasEntry(entries []os.DirEntry, name string) bool {
	for _, entry := range entries {
		if entry.Name() == name {
			return true
		}
	}
	return false
}

func isInside(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// warnAboutPlace tells about a place that works but is not a good one.
func (r *setupRun) warnAboutPlace(path string) {
	a := r.a
	if home := r.claudeHome.Path; home != "" && strings.EqualFold(filepath.VolumeName(path), filepath.VolumeName(home)) && filepath.VolumeName(path) != "" {
		r.step("local_disk", stepWarn, a.tr.T("setup.local.same_disk", filepath.VolumeName(path)), a.tr.T("setup.local.same_disk.hint"))
	}
	probe := path
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return
		}
		probe = parent
	}
	if usage, err := disk.Of(probe); err == nil && usage.Free < lowSpaceBytes {
		r.step("local_space", stepWarn, a.tr.T("setup.local.low_space", formatBytes(int64(usage.Free))), a.tr.T("setup.local.low_space.hint"))
	}
}

func (r *setupRun) askOffsite(existing *config.Config) (string, error) {
	a := r.a
	current := ""
	if existing != nil {
		current = existing.Repositories.Offsite
	}
	switch {
	case r.o.noOffsite:
		r.offsiteWarning(current)
		return "", nil
	case r.o.offsite != "":
		return r.checkOffsite(r.o.offsite)
	case !r.interactive:
		if current == "" {
			r.offsiteWarning("")
		} else {
			r.step("offsite", stepKept, a.tr.T("setup.offsite.kept", current), "")
		}
		return current, nil
	}
	r.say("setup.offsite.intro")
	if remotes := r.rcloneRemotes(); len(remotes) > 0 {
		r.say("setup.offsite.remotes", strings.Join(remotes, ", "))
	}
	for attempt := 0; attempt < 3; attempt++ {
		answer, err := r.p.ask(a.tr.T("setup.q.offsite"), current)
		if err != nil {
			return "", err
		}
		if answer == "" || strings.EqualFold(answer, "skip") || answer == "-" {
			r.offsiteWarning("")
			return "", nil
		}
		location, err := r.checkOffsite(answer)
		if err != nil {
			r.stepErr("offsite", stepWarn, err)
			continue
		}
		return location, nil
	}
	r.offsiteWarning("")
	return "", nil
}

// offsiteWarning says plainly what a missing external copy means.
func (r *setupRun) offsiteWarning(current string) {
	if current != "" {
		r.step("offsite", stepWarn, r.a.tr.T("setup.offsite.removed", current), "")
		return
	}
	r.step("offsite", stepWarn, r.a.tr.T("setup.offsite.none"), r.a.tr.T("setup.offsite.none.hint"))
}

func (r *setupRun) rcloneFile() string { return filepath.Join(r.layout.Root, "rclone.conf") }

// rcloneRemotes lists the remotes of the application's own rclone settings.
func (r *setupRun) rcloneRemotes() []string {
	if r.a.deps.resticPath != nil {
		return nil
	}
	installer, err := r.a.installer(r.layout)
	if err != nil {
		return nil
	}
	tool, err := installer.Locate(engine.ToolRclone)
	if err != nil {
		return nil
	}
	remotes, err := engine.ListRemotes(r.ctx, tool.Path, r.rcloneFile())
	if err != nil {
		return nil
	}
	return remotes
}

// checkOffsite accepts any location restic understands. For rclone it also
// checks that the remote exists in the application's rclone settings.
func (r *setupRun) checkOffsite(location string) (string, error) {
	a := r.a
	location = strings.TrimSpace(location)
	if !config.ValidOffsite(location) {
		return "", i18n.E("setup.offsite.invalid", location).Hint("setup.offsite.invalid.hint")
	}
	if rest, ok := strings.CutPrefix(location, "rclone:"); ok && a.deps.resticPath == nil {
		name, _, _ := strings.Cut(rest, ":")
		if !inList(r.rcloneRemotes(), name) {
			return "", i18n.E("setup.offsite.no_remote", name, r.rcloneFile()).Hint("setup.offsite.no_remote.hint", r.rcloneFile())
		}
	}
	r.step("offsite", stepChanged, a.tr.T("setup.offsite.accepted", location), a.tr.T("setup.offsite.not_copying"))
	return location, nil
}

// stdinLine reads one line of a secret from the input, for a script.
func (r *setupRun) stdinLine() (string, error) { return r.p.line() }

func (r *setupRun) askNotify(existing *config.Config, given *answers) error {
	a := r.a
	haveChannel := existing != nil && (existing.Notify.Telegram != nil || existing.Notify.Webhook != nil)
	switch {
	case r.o.noNotify:
		return nil
	case r.o.telegramChat != "" || r.o.telegramStdin:
		return r.takeTelegram(existing, given)
	case r.o.webhookStdin:
		return r.takeWebhook(given)
	case !r.interactive:
		if haveChannel {
			r.step("notify", stepKept, a.tr.T("setup.notify.kept"), "")
		} else {
			r.step("notify", stepSkipped, a.tr.T("setup.notify.none"), a.tr.T("setup.notify.none.hint"))
		}
		return nil
	case haveChannel:
		r.step("notify", stepKept, a.tr.T("setup.notify.kept"), "")
		return nil
	}
	options := []string{a.tr.T("setup.notify.option_none"), a.tr.T("setup.notify.option_telegram"), a.tr.T("setup.notify.option_webhook")}
	choice, err := r.p.choose(a.tr.T("setup.q.notify"), options, 0)
	if err != nil {
		return err
	}
	switch choice {
	case 1:
		return r.askTelegram(given)
	case 2:
		return r.askWebhook(given)
	}
	r.step("notify", stepSkipped, a.tr.T("setup.notify.none"), a.tr.T("setup.notify.none.hint"))
	return nil
}

func (r *setupRun) takeTelegram(existing *config.Config, given *answers) error {
	a := r.a
	chat := r.o.telegramChat
	if chat == "" && existing != nil && existing.Notify.Telegram != nil {
		chat = existing.Notify.Telegram.ChatID
	}
	if chat == "" {
		return i18n.E("notify.telegram.chat_required").Hint("notify.telegram.chat_required.hint").Exit(exitUsage)
	}
	token := ""
	if r.o.telegramStdin {
		line, err := r.stdinLine()
		if err != nil {
			return err
		}
		token = line
	}
	if token == "" {
		if _, err := a.deps.secrets().Get(notify.KeyTelegramToken); err != nil {
			return i18n.E("notify.telegram.token_required").Hint("notify.telegram.token_required.hint").Exit(exitUsage)
		}
	} else if !botToken.MatchString(token) {
		return i18n.E("notify.telegram.token_bad").Hint("notify.telegram.token_bad.hint")
	}
	given.chat, given.token = chat, token
	return nil
}

func (r *setupRun) askTelegram(given *answers) error {
	a := r.a
	r.say("setup.notify.telegram_intro")
	for attempt := 0; attempt < 3; attempt++ {
		token, err := r.p.secret(a.tr.T("setup.q.telegram_token"))
		if err != nil {
			return err
		}
		if token == "" {
			r.step("notify", stepSkipped, a.tr.T("setup.notify.none"), a.tr.T("setup.notify.none.hint"))
			return nil
		}
		if !botToken.MatchString(token) {
			r.step("notify", stepWarn, a.tr.T("notify.telegram.token_bad"), a.tr.T("notify.telegram.token_bad.hint"))
			continue
		}
		chat, err := r.p.ask(a.tr.T("setup.q.telegram_chat"), "")
		if err != nil {
			return err
		}
		if chat == "" {
			r.step("notify", stepSkipped, a.tr.T("setup.notify.none"), a.tr.T("setup.notify.none.hint"))
			return nil
		}
		given.chat, given.token = chat, token
		return nil
	}
	return nil
}

func (r *setupRun) takeWebhook(given *answers) error {
	line, err := r.stdinLine()
	if err != nil {
		return err
	}
	if line == "" {
		return i18n.E("notify.webhook.url_required").Hint("notify.webhook.url_required.hint").Exit(exitUsage)
	}
	if bad := notify.CheckWebhookURL(line); bad != nil {
		return bad
	}
	given.webhook = line
	return nil
}

func (r *setupRun) askWebhook(given *answers) error {
	a := r.a
	for attempt := 0; attempt < 3; attempt++ {
		address, err := r.p.secret(a.tr.T("setup.q.webhook"))
		if err != nil {
			return err
		}
		if address == "" {
			r.step("notify", stepSkipped, a.tr.T("setup.notify.none"), a.tr.T("setup.notify.none.hint"))
			return nil
		}
		if bad := notify.CheckWebhookURL(address); bad != nil {
			r.stepErr("notify", stepWarn, bad)
			continue
		}
		given.webhook = address
		return nil
	}
	return nil
}

// ---- settings and repository ----

// settings writes config.json: new, or the existing one with the answers. A
// file that would not change is not rewritten.
func (r *setupRun) settings(existing *config.Config, given answers) (*config.Config, error) {
	a := r.a
	var cfg config.Config
	var before config.Config
	if existing != nil {
		before = *existing
		cfg = *existing
	} else {
		cfg = config.Default()
		cfg.Version = config.CurrentVersion
	}
	cfg.Repositories.Local = given.local
	if existing == nil || r.offsiteAnswered() {
		cfg.Repositories.Offsite = given.offsite
	}
	if given.chat != "" {
		cfg.Notify.Telegram = &config.Telegram{ChatID: given.chat}
	}
	if given.webhook != "" {
		cfg.Notify.Webhook = &config.Webhook{Enabled: true}
	}
	if lang := strings.TrimSpace(a.flagLang); lang != "" && i18n.Normalize(lang) != "" && existing == nil {
		cfg.Language = i18n.Normalize(lang)
	}
	changed := existing == nil || !reflect.DeepEqual(before, cfg)
	r.report.Local, r.report.Offsite = cfg.Repositories.Local, cfg.Repositories.Offsite
	if changed {
		if err := config.Save(r.layout.ConfigFile(), &cfg); err != nil {
			return nil, err
		}
		r.report.ConfigChanged = true
		r.step("settings", stepChanged, a.tr.T("setup.settings.saved", r.layout.ConfigFile()), "")
	} else {
		r.step("settings", stepKept, a.tr.T("setup.settings.unchanged", r.layout.ConfigFile()), "")
	}
	store := a.deps.secrets()
	if given.token != "" {
		if err := store.Set(notify.KeyTelegramToken, given.token); err != nil {
			return nil, err
		}
		r.step("notify", stepChanged, a.tr.T("setup.notify.telegram_saved"), "")
	}
	if given.webhook != "" {
		if err := store.Set(notify.KeyWebhookURL, given.webhook); err != nil {
			return nil, err
		}
		r.step("notify", stepChanged, a.tr.T("setup.notify.webhook_saved"), "")
	}
	return &cfg, nil
}

// offsiteAnswered is true when the person said something about the external
// copy; otherwise a rerun keeps what the settings hold.
func (r *setupRun) offsiteAnswered() bool {
	return r.o.noOffsite || r.o.offsite != "" || r.interactive
}

// repository creates the password and the repository, or finds them.
func (r *setupRun) repository() (*session, repoResult, error) {
	a := r.a
	current, err := a.openSession()
	if err != nil {
		return nil, repoResult{}, err
	}
	result, err := a.ensureRepo(r.ctx, current)
	r.report.Password = passwordReport{Created: result.PasswordCreated}
	if _, getErr := a.deps.secrets().Get(secrets.RepoPasswordKey); getErr == nil {
		r.report.Password.Stored = true
	}
	if err != nil {
		return nil, result, err
	}
	if result.PasswordCreated {
		r.step("password", stepChanged, a.tr.T("repo.password_created"), "")
	} else {
		r.step("password", stepKept, a.tr.T("setup.password.kept"), "")
	}
	if result.Created {
		r.step("repository", stepChanged, a.tr.T("repo.created", result.Repository), "")
	} else {
		r.step("repository", stepKept, a.tr.T("repo.exists", result.Repository), "")
	}
	return current, result, nil
}

// ---- schedule, skill, snapshot, password ----

// schedule installs the tasks that are missing or differ from the settings. A
// task that already matches is left alone, so a rerun does not reset it.
func (r *setupRun) schedule(cfg *config.Config) error {
	a := r.a
	if !r.schedulerOK {
		r.step("schedule", stepWarn, a.tr.T("setup.schedule.unsupported"), a.tr.T("setup.schedule.unsupported.hint"))
		return nil
	}
	scheduler := a.scheduler(r.layout)
	installed, err := scheduler.List()
	if err != nil {
		return err
	}
	byName := map[string]schedule.Info{}
	for _, info := range installed {
		byName[info.Name] = info
	}
	wanted := schedule.Plan(cfg, r.exe)
	var added, kept int
	for _, want := range wanted {
		if info, ok := byName[want.Name]; ok && a.describeTask(want, info).InSync {
			kept++
			continue
		}
		if err := scheduler.Install(want); err != nil {
			return err
		}
		added++
	}
	if err := os.MkdirAll(r.layout.LogsDir(), 0o700); err != nil {
		return i18n.E("schedule.work_failed", r.layout.LogsDir()).Because(err)
	}
	status := stepChanged
	if added == 0 {
		status = stepKept
	}
	r.step("schedule", status, a.tr.T("setup.schedule.done", added, kept), "")
	after, err := scheduler.List()
	if err != nil {
		return err
	}
	byName = map[string]schedule.Info{}
	for _, info := range after {
		byName[info.Name] = info
	}
	for _, want := range wanted {
		job := scheduleJob{Job: want.Name, WantEveryHours: want.Every.Hours()}
		if info, ok := byName[want.Name]; ok {
			job = a.describeTask(want, info)
		}
		r.report.Tasks = append(r.report.Tasks, job)
		if !job.InSync {
			r.step("schedule_"+want.Name, stepWarn, a.tr.T("schedule.job.missing", want.Name), a.tr.T("setup.schedule.unsupported.hint"))
		}
	}
	return nil
}

// skill offers the /backup skill and installs it if the person agrees.
func (r *setupRun) skill() {
	a := r.a
	if r.o.noSkill {
		r.step("skill", stepSkipped, a.tr.T("setup.skill.skipped"), "")
		return
	}
	if !r.claudeHome.Exists {
		r.step("skill", stepSkipped, a.tr.T("skill.no_claude", r.claudeHome.Path), a.tr.T("skill.no_claude.hint"))
		return
	}
	command := a.skillCommand(r.layout)
	state, err := skill.Status(r.claudeHome.Path, command)
	if err != nil {
		r.stepErr("skill", stepWarn, err)
		return
	}
	r.report.Skill = &state
	switch {
	case state.Installed && !state.Ours:
		r.step("skill", stepWarn, a.tr.T("skill.foreign", state.Path), a.tr.T("skill.foreign.hint"))
		return
	case state.Current:
		r.step("skill", stepKept, a.tr.T("skill.up_to_date", state.Path), "")
		return
	}
	switch {
	case r.o.skill, state.Installed:
		// Asked for, or our own skill that only needs the new text.
	case r.interactive:
		yes, err := r.p.yesNo(a.tr.T("setup.q.skill"), true)
		if err != nil || !yes {
			r.step("skill", stepSkipped, a.tr.T("setup.skill.skipped"), a.tr.T("setup.skill.skipped.hint"))
			return
		}
	default:
		r.step("skill", stepSkipped, a.tr.T("setup.skill.not_asked"), a.tr.T("setup.skill.skipped.hint"))
		return
	}
	if _, err := skill.Install(r.claudeHome.Path, command); err != nil {
		r.stepErr("skill", stepWarn, err)
		return
	}
	state, _ = skill.Status(r.claudeHome.Path, command)
	r.report.Skill = &state
	r.step("skill", stepChanged, a.tr.T("skill.installed", state.Path), a.tr.T("skill.how_to_use"))
}

// snapshot takes the first snapshot of Claude's data. A repository that
// already holds snapshots is left as it is unless the person asks.
func (r *setupRun) snapshot(current *session, repo repoResult) {
	a := r.a
	if r.o.noSnapshot {
		r.step("snapshot", stepSkipped, a.tr.T("setup.snapshot.skipped"), "")
		return
	}
	if !repo.Created && !r.o.snapshot {
		have, err := current.restic.Snapshots(r.ctx, "claude")
		if err == nil && len(have) > 0 {
			var last time.Time
			for _, snap := range have {
				if snap.Time.After(last) {
					last = snap.Time
				}
			}
			r.step("snapshot", stepKept, a.tr.T("setup.snapshot.exists", len(have), last.Local().Format("02.01.2006 15:04")), "")
			return
		}
	}
	r.say("setup.snapshot.working")
	r.takeSnapshot(current, "claude")

	projects := r.o.projectsNow
	if !projects && r.interactive && repo.Created {
		projects, _ = r.p.yesNo(a.tr.T("setup.q.projects_now"), false)
		if !projects {
			r.say("setup.snapshot.projects_later")
		}
	}
	if projects {
		r.say("setup.snapshot.projects_working")
		r.takeSnapshot(current, "projects")
	}
}

func (r *setupRun) takeSnapshot(current *session, name string) {
	a := r.a
	started := time.Now()
	report, failed, err := a.saveProfiles(r.ctx, current, []string{name}, false)
	if err != nil {
		r.stepErr("snapshot_"+name, stepFail, err)
		return
	}
	for _, result := range report.Profiles {
		result.Plan = nil
		r.report.Snapshots = append(r.report.Snapshots, result)
		switch {
		case result.Error != nil:
			r.step("snapshot_"+name, stepFail, result.Error.Message, result.Error.Hint)
		case failed:
			r.step("snapshot_"+name, stepFail, a.tr.T("backup.profile", name, a.tr.T("backup.status."+result.Status)), "")
		default:
			status := stepChanged
			message := a.tr.T("setup.snapshot.done", name, result.SnapshotID, result.Files, formatBytes(result.Bytes), time.Since(started).Round(time.Second).String())
			if result.Status == backupLocked {
				message += " " + a.tr.T("setup.snapshot.locked", len(result.LockedFiles))
			}
			r.step("snapshot_"+name, status, message, "")
		}
	}
}

// paper explains how to write the password on paper. Only a person at a
// terminal can have it shown: the program never prints it for a script.
func (r *setupRun) paper(repo repoResult) {
	a := r.a
	r.say("setup.paper.title")
	if !repo.PasswordCreated {
		r.say("setup.paper.existing")
		return
	}
	for _, key := range []string{"setup.paper.why", "setup.paper.step1", "setup.paper.step2", "setup.paper.step3", "setup.paper.step4"} {
		r.say(key)
	}
	if !r.interactive || a.jsonOut {
		r.say("setup.paper.command")
		return
	}
	show, err := r.p.yesNo(a.tr.T("setup.q.show_password"), true)
	if err != nil || !show {
		r.say("setup.paper.command")
		return
	}
	if err := a.showPassword(); err != nil {
		r.stepErr("password_shown", stepWarn, err)
		r.say("setup.paper.command")
		return
	}
	r.report.Password.Shown = true
}
