package restore_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

// EnvRealRestic names a real restic binary. CI has none and skips this test: it
// pins how restic itself finds, includes and restores, which no fake can show.
const EnvRealRestic = "BACKUPCLAUDE_TEST_RESTIC"

const fixturePassword = "fixture-only"

func TestRealResticRestoresASession(t *testing.T) {
	binary := os.Getenv(EnvRealRestic)
	if binary == "" {
		t.Skipf("%s is not set", EnvRealRestic)
	}
	ctx := context.Background()
	manifest, err := manifests.Load(runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	home := fakehome.Copy(t, fakehome.Store)
	project := filepath.Join(home, filepath.FromSlash(fakehome.Repository))
	const id = "5cb735df-6f64-4b0c-81a4-e399f4bbaa79"
	folder := restore.ProjectFolder(project)
	transcript := fakehome.Transcript(t, home, folder, id, project)
	fakehome.Write(t, filepath.Join(home, ".claude", "projects", folder, id, "tool-results", "out [1].txt"), "result\n")
	want, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	restic := &engine.Restic{Binary: binary, Repo: filepath.Join(dir, "хранилище"), CacheDir: filepath.Join(dir, "cache")}
	restic.UseTypedPassword(fixturePassword) // the way a new computer opens a copy
	if _, err := restic.Init(ctx); err != nil {
		t.Fatal(err)
	}
	sources := []string{filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json"), filepath.Join(home, filepath.FromSlash(fakehome.StoreRoot))}
	if _, err := restic.Backup(ctx, engine.BackupOptions{Paths: sources, Tags: []string{restore.TagClaude}}); err != nil {
		t.Fatal(err)
	}
	snapshots, err := restic.Snapshots(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	fakehome.Remove(t, transcript)
	fakehome.Remove(t, filepath.Join(home, ".claude", "projects", folder, id))

	match, err := restore.FindSession(ctx, restic, manifest, snapshots, id[:8], "")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "восстановлено")
	fetched, err := restore.Fetch(ctx, restic, match.Pieces, target)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Files != 2 {
		t.Errorf("fetched %d files: %v", fetched.Files, fakehome.Tree(t, target))
	}
	place := restore.Placement{Manifest: manifest, Origin: restore.OriginOf(manifest, snapshots[0]), Mapper: restore.NewMapper(nil)}
	plan, err := restore.PlanApply(target, match.Pieces, place)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restore.Apply(plan, place.Mapper); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(transcript)
	if err != nil || string(got) != string(want) {
		t.Errorf("transcript: %q, %v", got, err)
	}

	// A saved file (not a folder) comes back through its parent folder, and
	// only it: nothing else of the home folder is restored.
	pieces, err := restore.PiecesOf(ctx, restic, snapshots[0], restore.TagClaude, restore.RootsOf(snapshots[0]))
	if err != nil {
		t.Fatal(err)
	}
	all := filepath.Join(dir, "всё")
	if _, err := restore.Fetch(ctx, restic, pieces, all); err != nil {
		t.Fatal(err)
	}
	homeStaged := restore.StagedPath(all, restore.SnapshotPath(home))
	entries, err := os.ReadDir(homeStaged)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	if !names[".claude.json"] || !names[".claude"] || !names["AppData"] || len(names) != 3 {
		t.Errorf("home folder of the restore holds %v", names)
	}
	// What a restore writes belongs to the user: it can be removed.
	if err := os.RemoveAll(all); err != nil {
		t.Errorf("the restore folder cannot be removed: %v", err)
	}

	// The trial restore with the real restic.
	result, err := restore.Drill(ctx, restic, snapshots[0], restore.TagClaude, restore.DrillOptions{
		MaxFiles: 50, MaxBytes: 1 << 30, Seed: 7, WorkDir: t.TempDir(), Target: filepath.Join(dir, "проба"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK() || result.Matched == 0 {
		t.Errorf("drill = %+v", result)
	}
}
