package profile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
)

// EnvRealRestic names a real restic binary. CI has none and skips this test: its
// point is to pin how restic reads our patterns, which no fake can show.
const EnvRealRestic = "BACKUPCLAUDE_TEST_RESTIC"

func realRestic(t *testing.T, home string) *engine.Restic {
	t.Helper()
	binary := os.Getenv(EnvRealRestic)
	if binary == "" {
		t.Skipf("%s is not set", EnvRealRestic)
	}
	dir := t.TempDir()
	restic := &engine.Restic{
		Binary:   binary,
		Repo:     filepath.Join(dir, "хранилище с пробелом"),
		CacheDir: filepath.Join(dir, "cache"),
		ExtraEnv: []string{"RESTIC_PASSWORD=fixture-only"},
	}
	if _, err := restic.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return restic
}

func runReal(t *testing.T, restic *engine.Restic, plan *Plan) []string {
	t.Helper()
	ctx := context.Background()
	file, err := plan.WriteExcludeFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := restic.Backup(ctx, plan.Options(file, false))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	err = restic.Ls(ctx, result.Summary.SnapshotID, nil, func(node engine.Node) {
		if node.Type == engine.NodeFile {
			paths = append(paths, node.Path)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestRealResticFollowsThePatterns(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Both)
	restic := realRestic(t, home)
	in := inputs(t, home)

	claude := strings.Join(runReal(t, restic, build(t, Claude, in)), "\n")
	for _, want := range []string{"/.claude/settings.json", "/.claude.json", "/brand-new-folder/", "/Local Storage/leveldb/", "/plugins/demo/cache/stays.txt"} {
		if !strings.Contains(claude, want) {
			t.Errorf("%s is missing:\n%s", want, claude)
		}
	}
	for _, banned := range []string{".credentials.json", "/vm_bundles/", "node_modules", "/.claude/cache/", "/Partitions/", "/Cache/", "dxt-install"} {
		if strings.Contains(claude, banned) {
			t.Errorf("%s must stay out:\n%s", banned, claude)
		}
	}

	bracketed := filepath.Join(home, "проект [2026]")
	fakehome.Write(t, filepath.Join(bracketed, "main.txt"), "x")
	fakehome.Write(t, filepath.Join(bracketed, "node_modules", "dep.js"), "x")
	fakehome.Write(t, filepath.Join(bracketed, "тяжёлое", "ролик.mp4"), "x")
	dollar := filepath.Join(home, "цена $5")
	fakehome.Write(t, filepath.Join(dollar, "main.txt"), "x")
	fakehome.Write(t, filepath.Join(dollar, "node_modules", "dep.js"), "x")
	underBuild := filepath.Join(home, "build", "приложение")
	fakehome.Write(t, filepath.Join(underBuild, "main.txt"), "x")
	fakehome.Transcript(t, home, "x", "a", filepath.Join(home, filepath.FromSlash(fakehome.Repository)))
	fakehome.Transcript(t, home, "x", "b", filepath.Join(home, filepath.FromSlash(fakehome.PlainFolder)))
	fakehome.Transcript(t, home, "x", "c", bracketed)
	fakehome.Transcript(t, home, "x", "d", dollar)
	fakehome.Transcript(t, home, "x", "e", underBuild)
	in.Config.Sources.Exclude = []string{filepath.Join(bracketed, "тяжёлое")}

	projects := strings.Join(runReal(t, restic, build(t, Projects, in)), "\n")
	for _, want := range []string{"Сайт клуба/index.html", "Сайт клуба/.git/HEAD", "Без git/build/единственная копия.txt", "проект [2026]/main.txt", "цена $5/main.txt", "приложение/main.txt"} {
		if !strings.Contains(projects, want) {
			t.Errorf("%s is missing:\n%s", want, projects)
		}
	}
	for _, banned := range []string{"node_modules", "Сайт клуба/dist", "venv", ".credentials.json", "тяжёлое"} {
		if strings.Contains(projects, banned) {
			t.Errorf("%s must stay out:\n%s", banned, projects)
		}
	}

	// restic stops at direct children of a given path unless told otherwise. That
	// once made every session look unsaved, and no fake could show it.
	transcripts := restore.SnapshotPath(filepath.Join(home, ".claude", "projects"))
	found := 0
	claudeSnapshot, err := restic.Snapshots(context.Background(), Claude)
	if err != nil || len(claudeSnapshot) == 0 {
		t.Fatalf("snapshots = %+v, %v", claudeSnapshot, err)
	}
	absent := restore.SnapshotPath(filepath.Join(home, "нет такой папки"))
	err = restic.Ls(context.Background(), claudeSnapshot[0].ShortID, []string{transcripts, absent}, func(node engine.Node) {
		if strings.HasSuffix(node.Name, ".jsonl") {
			found++
		}
	})
	if err != nil || found == 0 {
		t.Errorf("a walk below a path must reach the files: found %d, %v", found, err)
	}

	plan := build(t, Projects, in)
	check, err := plan.Verify(context.Background(), "latest", nil, restic.Ls)
	if err != nil {
		t.Fatal(err)
	}
	if !check.OK {
		t.Errorf("check = %+v", check)
	}
}
