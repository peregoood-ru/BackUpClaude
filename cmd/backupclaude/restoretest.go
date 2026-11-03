package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
)

// Limits of the trial restore of one copy. A few hundred megabytes are read
// back from a slow disk in a few minutes, and a month of runs tests different
// files each time.
const (
	restoreTestFiles = 60
	restoreTestBytes = 300 * megabyte
	// restoreTestSubset is the share of the data that check reads and compares
	// with its hashes, by the requirement: a tenth a month.
	restoreTestSubset = "10%"
)

// restoreTestCopy is the result for one copy.
type restoreTestCopy struct {
	Role     string                 `json:"role"`
	Check    *engine.CheckResult    `json:"check,omitempty"`
	Profiles []*restore.DrillResult `json:"profiles"`
}

// restoreTestAnswer is the result of the restore-test job in the run log.
type restoreTestAnswer struct {
	Copies   []restoreTestCopy `json:"copies"`
	Files    int               `json:"files"`
	Matched  int               `json:"matched"`
	Bytes    int64             `json:"bytes"`
	Seconds  float64           `json:"seconds"`
	Problems []string          `json:"problems,omitempty"`
}

// runRestoreTest proves that the copies can be read back. For each copy it
// reads a tenth of the data and compares it with the hashes restic keeps, then
// restores sample files of the newest snapshot of each profile into an empty
// folder next to the local repository, with restic reading every file back,
// and compares the SHA-256 of each with the file on disk. The folder is removed
// afterwards. Any difference fails the job, and a failed job is reported at
// once; a success goes only into the weekly summary.
func (a *app) runRestoreTest(ctx context.Context, current *session) (any, error) {
	started := time.Now()
	answer := &restoreTestAnswer{}
	copies := []struct {
		role     string
		location string
	}{{fromLocal, current.config.Repositories.Local}}
	if offsite := current.config.Repositories.Offsite; offsite != "" {
		copies = append(copies, struct {
			role     string
			location string
		}{fromOffsite, offsite})
	}
	var firstErr error
	for _, item := range copies {
		restic := *current.restic
		restic.Repo = item.location
		restic.RetryLock = maintenanceLockWait
		if item.role == fromOffsite {
			if err := a.useRclone(current.layout, &restic); err != nil {
				answer.Problems = append(answer.Problems, item.role+": "+a.tr.Describe(err).Message)
				firstErr = firstOf(firstErr, err)
				continue
			}
		}
		result, err := a.restoreTestCopy(ctx, current, &restic, item.role)
		answer.Copies = append(answer.Copies, result)
		if err != nil {
			answer.Problems = append(answer.Problems, item.role+": "+a.tr.Describe(err).Message)
			firstErr = firstOf(firstErr, err)
		}
		for _, profile := range result.Profiles {
			answer.Files += profile.Restored
			answer.Matched += profile.Matched
			answer.Bytes += profile.Bytes
		}
	}
	answer.Seconds = time.Since(started).Seconds()
	return answer, firstErr
}

func firstOf(first, next error) error {
	if first != nil {
		return first
	}
	return next
}

func (a *app) restoreTestCopy(ctx context.Context, current *session, restic *engine.Restic, role string) (restoreTestCopy, error) {
	result := restoreTestCopy{Role: role, Profiles: []*restore.DrillResult{}}
	if err := restic.Unlock(ctx); err != nil {
		return result, err
	}
	check, err := restic.CheckData(ctx, restoreTestSubset)
	result.Check = check
	if err != nil {
		return result, err
	}
	if check.Errors > 0 {
		return result, i18n.E("run.check_errors", check.Errors).Hint("run.check_errors.hint")
	}
	snapshots, err := restic.Snapshots(ctx, "")
	if err != nil {
		return result, err
	}
	work := restore.DrillFolder(filepath.Dir(current.config.Repositories.Local), time.Now())
	defer os.RemoveAll(work)
	for _, profile := range []string{restore.TagClaude, restore.TagProjects} {
		latest := restore.Latest(snapshots, profile)
		if latest == nil {
			continue
		}
		drill, err := restore.Drill(ctx, restic, *latest, profile, restore.DrillOptions{
			MaxFiles: restoreTestFiles / 2, MaxBytes: restoreTestBytes / 2,
			Seed: uint64(time.Now().UnixNano()), WorkDir: current.layout.TempDir(),
			Target: filepath.Join(work, profile),
		})
		if drill != nil {
			result.Profiles = append(result.Profiles, drill)
		}
		if err != nil {
			return result, err
		}
		if len(drill.Mismatches) > 0 {
			return result, i18n.E("restore_test.mismatch", len(drill.Mismatches), profile, drill.Mismatches[0].Path).Hint("restore_test.mismatch.hint")
		}
	}
	restored := 0
	for _, drill := range result.Profiles {
		restored += drill.Restored
	}
	if restored == 0 {
		return result, i18n.E("restore_test.nothing").Hint("restore_test.nothing.hint")
	}
	return result, nil
}
