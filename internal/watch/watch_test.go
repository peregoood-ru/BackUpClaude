package watch

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/discover"
	"github.com/peregoood-ru/BackUpClaude/internal/fakehome"
	"github.com/peregoood-ru/BackUpClaude/manifests"
)

var noon = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func TestCountTranscriptsCountsEveryJSONLBelowTheRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/one.jsonl", "a/two.JSONL", "b/c/deep.jsonl", "b/notes.txt", "b/c/x.json"} {
		fakehome.Write(t, filepath.Join(root, filepath.FromSlash(name)), "{}\n")
	}
	got, err := CountTranscripts(root)
	if err != nil || got != 3 {
		t.Fatalf("got %d, %v", got, err)
	}
	if _, err := CountTranscripts(filepath.Join(root, "missing")); err == nil {
		t.Error("a missing root must be an error: it is not the same as zero sessions")
	}
}

func TestDeletionAlarmFollowsTheThreshold(t *testing.T) {
	state := &State{}
	if drop := state.Deletion(10, 20); drop.Alarm || drop.Previous.Files != 0 {
		t.Errorf("the first run has nothing to compare with: %+v", drop)
	}
	state.SetTranscripts(noon, 100)
	for _, c := range []struct {
		now       int
		threshold int
		alarm     bool
	}{
		{100, 20, false}, // nothing lost
		{150, 20, false}, // more than before
		{81, 20, false},  // 19 percent
		{80, 20, false},  // exactly 20 percent is not "more than 20"
		{79, 20, true},   // 21 percent
		{0, 20, true},    // everything
		{79, 25, false},  // the threshold is a setting
		{74, 25, true},
	} {
		if got := state.Deletion(c.now, c.threshold); got.Alarm != c.alarm {
			t.Errorf("%d of 100 at %d%%: alarm = %v, want %v (lost %.1f%%)", c.now, c.threshold, got.Alarm, c.alarm, got.Percent)
		}
	}
	if drop := state.Deletion(60, 20); drop.Percent != 40 || drop.Previous.Files != 100 || drop.Previous.Time != noon {
		t.Errorf("%+v", drop)
	}
}

func TestSmallBaselinesNeverAlarm(t *testing.T) {
	state := &State{}
	state.SetTranscripts(noon, MinBaseline-1)
	if state.Deletion(0, 20).Alarm {
		t.Error("with so few sessions the alarm would only be noise")
	}
	state.SetTranscripts(noon, MinBaseline)
	if !state.Deletion(0, 20).Alarm {
		t.Error("the smallest meaningful baseline must still alarm")
	}
}

func TestStateRoundTripAndDamage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "watch.json")
	state, err := Load(path)
	if err != nil || state.Transcripts != nil {
		t.Fatalf("a missing file is an empty state: %+v %v", state, err)
	}
	if err := Update(path, func(s *State) {
		s.SetTranscripts(noon, 42)
		s.SetFolders([]string{"a", "b"})
		s.JobTold("claude", noon)
	}); err != nil {
		t.Fatal(err)
	}
	state, err = Load(path)
	if err != nil || state.Transcripts.Files != 42 || !slices.Equal(state.Folders, []string{"a", "b"}) || !state.Jobs["claude"].Failing {
		t.Fatalf("%+v %v", state, err)
	}

	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err = Load(path)
	if !errors.Is(err, ErrDamaged) || state == nil || state.Transcripts != nil {
		t.Fatalf("a damaged file starts over: %+v %v", state, err)
	}
	if err := Update(path, func(s *State) { s.SetTranscripts(noon, 1) }); err != nil {
		t.Fatalf("a damaged file must not stop the run: %v", err)
	}
	if state, err = Load(path); err != nil || state.Transcripts.Files != 1 {
		t.Fatalf("%+v %v", state, err)
	}
}

func TestVanishedFolders(t *testing.T) {
	state := &State{}
	state.SetFolders([]string{"home", "home/projects", "desktop"})
	gone := state.Vanished([]string{"home", "home/projects"})
	if !slices.Equal(gone, []string{"desktop"}) {
		t.Errorf("gone = %v", gone)
	}
	state.SetFolders([]string{"home", "home/projects"})
	if gone := state.Vanished([]string{"home", "home/projects"}); len(gone) != 0 {
		t.Errorf("a folder is reported once: %v", gone)
	}
	if gone := (&State{}).Vanished(nil); len(gone) != 0 {
		t.Errorf("the first run has nothing to lose: %v", gone)
	}
}

func TestFailureIsNewsOnceAndThenOncePerDay(t *testing.T) {
	state := &State{}
	if !state.ShouldTellFailure("claude", noon) {
		t.Fatal("the first failure is news")
	}
	state.JobTold("claude", noon)
	if state.ShouldTellFailure("claude", noon.Add(time.Hour)) {
		t.Error("the same failure an hour later is not news")
	}
	if !state.ShouldTellFailure("claude", noon.Add(25*time.Hour)) {
		t.Error("a failure that goes on is mentioned again after a day")
	}
	if !state.ShouldTellFailure("projects", noon.Add(time.Hour)) {
		t.Error("another job has its own state")
	}
	state.JobSucceeded("claude")
	if !state.ShouldTellFailure("claude", noon.Add(2*time.Hour)) {
		t.Error("after a success the next failure is news again")
	}
}

func manifest(t *testing.T) *manifests.Manifest {
	t.Helper()
	m, err := manifests.Load(runtime.GOOS)
	if err != nil {
		t.Skip(err)
	}
	return m
}

func TestImportantFindsTheFoldersOfAFakeHome(t *testing.T) {
	home := fakehome.Copy(t, fakehome.Both)
	env := discover.Env{Home: home, Lookup: fakehome.Lookup(home)}
	m := manifest(t)
	found := Important(m, env)
	if !found.TranscriptsExist || !slices.Contains(found.Present, filepath.Join(home, ".claude")) {
		t.Fatalf("%+v", found)
	}
	if runtime.GOOS == "windows" && len(found.Present) < 4 {
		t.Errorf("home, projects and both desktop folders were expected: %v", found.Present)
	}

	// Claude moves its folder: the old one is simply gone.
	if err := os.RemoveAll(filepath.Join(home, ".claude", "projects")); err != nil {
		t.Fatal(err)
	}
	after := Important(m, env)
	if after.TranscriptsExist || slices.Contains(after.Present, found.Transcripts) {
		t.Errorf("the folder is gone but still listed: %+v", after)
	}
	state := &State{}
	state.SetFolders(found.Present)
	if gone := state.Vanished(after.Present); !slices.Contains(gone, found.Transcripts) {
		t.Errorf("the missing folder was not noticed: %v", gone)
	}
}

func TestImportantOnAComputerWithoutClaude(t *testing.T) {
	home := t.TempDir()
	found := Important(manifest(t), discover.Env{Home: home, Lookup: fakehome.Lookup(home)})
	if len(found.Present) != 0 || found.TranscriptsExist {
		t.Errorf("%+v", found)
	}
}

func TestCountsLargeTree(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 30; i++ {
		fakehome.Write(t, filepath.Join(root, "p", strconv.Itoa(i)+".jsonl"), "{}")
	}
	if n, err := CountTranscripts(root); err != nil || n != 30 {
		t.Fatalf("%d %v", n, err)
	}
}
