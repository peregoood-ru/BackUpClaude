package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/sysproc"
)

// The fake restic keeps one snapshot as a folder tree: the file of snapshot
// path /C/x lies at <dir>/tree/C/x. nodes.jsonl and snapshots.json describe it.
const (
	fakeSnapshotID = "abcdef0123456789"
	typedPassword  = "TEST-ONLY-PASSWORD"
)

// typedPasswordMatches reads the password restic would read from its input.
func typedPasswordMatches() bool {
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	for _, arg := range os.Args {
		if arg == "--password-command" || strings.Contains(arg, typedPassword) {
			return false
		}
	}
	return strings.TrimRight(line, "\r\n") == typedPassword
}

func fakeFind(dir string, args []string) {
	var patterns []string
	for i, arg := range args {
		if arg == "--" {
			patterns = args[i+1:]
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "nodes.jsonl"))
	result := engine.FindResult{Snapshot: fakeSnapshotID}
	var matchedDirs []string
	for _, line := range strings.Split(string(data), "\n") {
		var node engine.Node
		if json.Unmarshal([]byte(line), &node) != nil || node.Path == "" {
			continue
		}
		inside := false
		for _, folder := range matchedDirs {
			inside = inside || strings.HasPrefix(node.Path, folder+"/")
		}
		for _, pattern := range patterns {
			if ok, _ := path.Match(pattern, node.Name); ok && !inside {
				inside = true
				if node.Type == engine.NodeDir {
					matchedDirs = append(matchedDirs, node.Path)
				}
			}
		}
		if inside {
			result.Matches = append(result.Matches, node)
		}
	}
	if len(result.Matches) == 0 {
		fmt.Println("[]")
		return
	}
	out, _ := json.Marshal([]engine.FindResult{result})
	fmt.Println(string(out))
}

func fakeRestore(dir string, args []string) {
	var subfolder, target string
	var includes []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target":
			target = args[i+1]
			i++
		case "--include":
			includes = append(includes, args[i+1])
			i++
		case "--include-file":
			data, _ := os.ReadFile(args[i+1])
			for _, line := range strings.Split(string(data), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					includes = append(includes, line)
				}
			}
			i++
		case "--repo", "--password-command", "--cache-dir", "--retry-lock", "--overwrite", "-o":
			i++
		default:
			if _, sub, ok := strings.Cut(args[i], ":"); ok && strings.HasPrefix(sub, "/") {
				subfolder = sub
			}
		}
	}
	source := filepath.Join(dir, "tree", filepath.FromSlash(strings.TrimPrefix(subfolder, "/")))
	restored := 0
	_ = filepath.WalkDir(source, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(source, p)
		rel = "/" + filepath.ToSlash(rel)
		if len(includes) > 0 && !includedBy(includes, rel) {
			return nil
		}
		dest := filepath.Join(target, filepath.FromSlash(rel))
		if _, err := os.Lstat(dest); err == nil {
			return nil
		}
		data, _ := os.ReadFile(p)
		_ = os.MkdirAll(filepath.Dir(dest), 0o755)
		_ = os.WriteFile(dest, data, 0o644)
		info, _ := entry.Info()
		_ = os.Chtimes(dest, info.ModTime(), info.ModTime())
		restored++
		return nil
	})
	fmt.Printf(`{"message_type":"summary","total_files":%d,"files_restored":%d}`+"\n", restored, restored)
}

func includedBy(patterns []string, rel string) bool {
	for _, pattern := range patterns {
		parts := strings.Split(rel, "/")
		for i := 1; i <= len(parts); i++ {
			if ok, _ := path.Match(pattern, strings.Join(parts[:i], "/")); ok {
				return true
			}
		}
	}
	return false
}

// writeFakeSnapshot saves the sources as the only snapshot of the fake restic.
func writeFakeSnapshot(t *testing.T, dir, tag string, sources ...string) {
	t.Helper()
	tree := filepath.Join(dir, "tree")
	for _, source := range sources {
		dest := filepath.Join(tree, filepath.FromSlash(strings.TrimPrefix(restore.SnapshotPath(source), "/")))
		err := filepath.WalkDir(source, func(p string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(source, p)
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out := filepath.Join(dest, rel)
			fakehome.Write(t, out, string(data))
			info, _ := entry.Info()
			return os.Chtimes(out, info.ModTime(), info.ModTime())
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var nodes strings.Builder
	_ = filepath.WalkDir(tree, func(p string, entry fs.DirEntry, err error) error {
		if err != nil || p == tree {
			return err
		}
		rel, _ := filepath.Rel(tree, p)
		info, _ := entry.Info()
		node := engine.Node{Name: entry.Name(), Path: "/" + filepath.ToSlash(rel), Type: engine.NodeFile, Mtime: info.ModTime()}
		if entry.IsDir() {
			node.Type = engine.NodeDir
		} else {
			node.Size = info.Size()
		}
		line, _ := json.Marshal(node)
		nodes.Write(append(line, '\n'))
		return nil
	})
	fakehome.Write(t, filepath.Join(dir, "nodes.jsonl"), nodes.String())
	snapshots, _ := json.Marshal([]engine.Snapshot{{
		ID: fakeSnapshotID, ShortID: fakeSnapshotID[:8], Time: time.Now().Add(-time.Hour), Hostname: "pc", Paths: sources, Tags: []string{tag},
	}})
	fakehome.Write(t, filepath.Join(dir, "snapshots.json"), string(snapshots))
}

type restoreWorld struct {
	*backupWorld
	project, transcript, id string
}

func newRestoreWorld(t *testing.T, mode string) *restoreWorld {
	t.Helper()
	w := &restoreWorld{backupWorld: newBackupWorld(t, mode), id: "0fc6092b-f4d7-460e-ab01-acc244289ebe"}
	w.project = filepath.Join(w.home, filepath.FromSlash(fakehome.Repository))
	w.transcript = fakehome.Transcript(t, w.home, restore.ProjectFolder(w.project), w.id, w.project)
	writeFakeSnapshot(t, w.dir, restore.TagClaude,
		filepath.Join(w.home, ".claude"), filepath.Join(w.home, filepath.FromSlash(fakehome.StoreRoot)))
	return w
}

type restoreAnswer struct {
	Mode     string           `json:"mode"`
	Sessions []string         `json:"sessions"`
	Fetched  *restore.Fetched `json:"restored"`
	Apply    *applyReport     `json:"apply"`
	Resume   []resumeHint     `json:"resume"`
}

func (w *restoreWorld) answer(t *testing.T, r result) restoreAnswer {
	t.Helper()
	var answer restoreAnswer
	if err := json.Unmarshal(r.envelope(t).Data, &answer); err != nil {
		t.Fatalf("%v\n%s", err, r.stdout)
	}
	return answer
}

func TestRestoreNeedsAModeWithoutATerminal(t *testing.T) {
	w := newRestoreWorld(t, "")
	r := w.run("restore", "--json")
	if r.code != exitUsage || r.envelope(t).Error.Code != "restore.mode_required" {
		t.Fatalf("%+v", r)
	}
	if r := w.run("restore", "--session", "a", "--all"); r.code != exitUsage {
		t.Errorf("two modes: %+v", r)
	}
	if r := w.run("restore", "--all", "--dry-run"); r.code != exitUsage {
		t.Errorf("--dry-run without --apply: %+v", r)
	}
}

func TestRestoreFromAMissingExternalCopy(t *testing.T) {
	w := newRestoreWorld(t, "")
	r := w.run("restore", "--all", "--from", "offsite", "--json")
	if r.code != 1 || r.envelope(t).Error.Code != "restore.offsite_missing" {
		t.Fatalf("%+v", r)
	}
	text := w.run("restore", "--all", "--from", "offsite")
	if !strings.Contains(text.stderr, "not set up") || !strings.Contains(text.stderr, "--from local") {
		t.Errorf("the message must say what to do: %s", text.stderr)
	}
}

func TestRestoreSessionIntoAFolderChangesNothingInPlace(t *testing.T) {
	w := newRestoreWorld(t, "")
	fakehome.Remove(t, w.transcript)
	target := filepath.Join(t.TempDir(), "куда")
	r := w.run("restore", "--session", w.id[:8], "--to", target, "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	answer := w.answer(t, r)
	if answer.Fetched.Files != 1 || answer.Apply != nil || len(answer.Sessions) != 1 {
		t.Errorf("%+v", answer)
	}
	if _, err := os.Stat(w.transcript); err == nil {
		t.Error("the session was put back without --apply")
	}
	staged := restore.StagedPath(target, restore.SnapshotPath(w.transcript))
	if _, err := os.Stat(staged); err != nil {
		t.Errorf("not in the restore folder: %v", err)
	}
	if len(answer.Resume) != 1 || answer.Resume[0].Cwd != w.project || answer.Resume[0].Command != "claude --resume "+w.id {
		t.Errorf("resume = %+v", answer.Resume)
	}
}

func TestRestoreSessionApplyPutsItBack(t *testing.T) {
	w := newRestoreWorld(t, "")
	want, _ := os.ReadFile(w.transcript)
	fakehome.Remove(t, w.transcript)
	r := w.run("restore", "--session", w.id, "--apply", "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	answer := w.answer(t, r)
	if answer.Apply == nil || answer.Apply.Copied != 1 {
		t.Fatalf("%+v", answer.Apply)
	}
	if got, _ := os.ReadFile(w.transcript); string(got) != string(want) {
		t.Errorf("came back as %q", got)
	}
	layout, _ := config.Resolve()
	if !strings.HasPrefix(answer.Fetched.Target, filepath.Join(filepath.Dir(filepath.Join(layout.Root, "repo")), "restored")) {
		t.Errorf("the default folder must be next to the local repository: %s", answer.Fetched.Target)
	}
	text := w.run("restore", "--session", w.id, "--apply")
	if text.code != 0 || !strings.Contains(text.stdout, "left untouched") || strings.Contains(text.stdout, "restore.") {
		t.Errorf("%+v", text)
	}
}

func TestRestoreRefusesTheDatabasesWhileClaudeRuns(t *testing.T) {
	w := newRestoreWorld(t, "")
	w.running = []sysproc.Process{{PID: 1, Name: "claude.exe"}}
	storage := filepath.Join(w.home, filepath.FromSlash(fakehome.StoreRoot), "Local Storage")
	fakehome.Remove(t, storage)
	fakehome.Remove(t, w.transcript)
	r := w.run("restore", "--all", "--apply", "--json")
	if r.code != 1 || r.envelope(t).Error.Code != "restore.claude_running" {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(storage); err == nil {
		t.Error("Local Storage was written while Claude runs")
	}
	if _, err := os.Stat(w.transcript); err == nil {
		t.Error("nothing at all may be written when the restore is refused")
	}
	w.running = nil
	if r := w.run("restore", "--all", "--apply", "--json"); r.code != 0 {
		t.Fatalf("with Claude closed: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(storage, "leveldb")); err != nil {
		t.Errorf("Local Storage did not come back: %v", err)
	}
}

// A new computer: no settings, no password in the key store. The copy is named
// on the command line and the password typed; it reaches restic on its input.
func TestRestoreOnANewComputerWithATypedPassword(t *testing.T) {
	w := newRestoreWorld(t, "typed")
	layout, _ := config.Resolve()
	if err := os.Remove(layout.ConfigFile()); err != nil {
		t.Fatal(err)
	}
	if r := w.run("restore", "--all", "--json"); r.code != 1 || r.envelope(t).Error.Code != "restore.no_settings" {
		t.Fatalf("%+v", r)
	}
	w.stdin = "wrong\n"
	if r := w.run("restore", "--repo", filepath.Join(t.TempDir(), "repo"), "--password-stdin", "--all", "--json"); r.code != 1 || r.envelope(t).Error.Code != "restore.wrong_password" {
		t.Fatalf("%+v", r)
	}
	w.stdin = typedPassword + "\n"
	target := filepath.Join(t.TempDir(), "новый компьютер")
	r := w.run("restore", "--repo", filepath.Join(t.TempDir(), "repo"), "--password-stdin", "--all", "--to", target, "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(r.stdout+r.stderr, typedPassword) {
		t.Error("the password was printed")
	}
	if _, err := os.Stat(restore.StagedPath(target, restore.SnapshotPath(w.transcript))); err != nil {
		t.Errorf("not restored: %v", err)
	}
}

func TestRestoreWizardAsksAndShowsSessions(t *testing.T) {
	w := newRestoreWorld(t, "")
	w.terminal = true
	fakehome.Remove(t, w.transcript)
	w.stdin = "1\n1\n2\n" // a session; the first in the list; put it back
	r := w.run("restore")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.stdout, w.id[:8]) || !strings.Contains(r.stdout, "only in copy") {
		t.Errorf("the list must show the missing session:\n%s", r.stdout)
	}
	if _, err := os.Stat(w.transcript); err != nil {
		t.Errorf("not put back: %v\n%s", err, r.stdout)
	}
}

func TestRestoreTestJobComparesHashesAndReportsInTheStatus(t *testing.T) {
	w := newRestoreWorld(t, "")
	r := w.run("run", "--job", schedule.JobRestoreTest, "--json")
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	runs := readRunLog(t)
	last := runs[len(runs)-1]
	data, _ := json.Marshal(last.Result)
	var answer restoreTestAnswer
	if err := json.Unmarshal(data, &answer); err != nil || answer.Files == 0 || answer.Matched != answer.Files {
		t.Fatalf("answer = %s, %v", data, err)
	}
	checked := false
	for _, call := range w.calls(t) {
		checked = checked || (strings.Contains(call, `"check"`) && strings.Contains(call, `"10%"`))
	}
	if !checked {
		t.Errorf("a tenth of the data must be read: %v", w.calls(t))
	}
	layout, _ := config.Resolve()
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Join(layout.Root, "repo")), "restore-test-*"))
	if len(leftovers) != 0 {
		t.Errorf("the trial folder must be removed: %v", leftovers)
	}
	status := w.run("status")
	if !strings.Contains(status.stdout, "Trial restore") || !strings.Contains(status.stdout, "all equal") {
		t.Errorf("status:\n%s", status.stdout)
	}
}
