package engine

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUnlockRemovesOnlyStaleLocks(t *testing.T) {
	r, record := fakeClient(t, "silent")
	if err := r.Unlock(context.Background()); err != nil {
		t.Fatal(err)
	}
	args := recorded(t, record).Args
	if args[0] != "unlock" || slices.Contains(args, "--remove-all") {
		t.Errorf("args = %v: --remove-all would break a live run", args)
	}
}

func TestForgetAppliesRetentionByHostAndTags(t *testing.T) {
	r, record := fakeClient(t, "forget")
	result, err := r.Forget(context.Background(), Retention{Hourly: 48, Daily: 30, Weekly: 12, Monthly: 24}, ForgetOptions{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Groups != 2 || result.Kept != 3 || result.Removed != 1 || !slices.Equal(result.Gone, []string{"cccccccc"}) {
		t.Errorf("result = %+v", result)
	}
	args := strings.Join(recorded(t, record).Args, " ")
	for _, want := range []string{"forget --json", "--group-by host,tags", "--keep-hourly 48", "--keep-daily 30", "--keep-weekly 12", "--keep-monthly 24", "--prune"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	if strings.Contains(args, "--dry-run") {
		t.Errorf("a real run must not be a dry run: %s", args)
	}
}

func TestForgetSkipsZeroRulesAndNeedsOne(t *testing.T) {
	r, record := fakeClient(t, "forget")
	if _, err := r.Forget(context.Background(), Retention{Daily: 7}, ForgetOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(recorded(t, record).Args, " ")
	if strings.Contains(args, "--keep-hourly") || !strings.Contains(args, "--keep-daily 7") || !strings.Contains(args, "--dry-run") {
		t.Errorf("args = %s", args)
	}
	if _, err := r.Forget(context.Background(), Retention{}, ForgetOptions{}); err == nil {
		t.Error("forget without any rule must be refused: restic would have no policy")
	}
}

func TestCheckReadsTheSummary(t *testing.T) {
	r, _ := fakeClient(t, "check")
	result, err := r.Check(context.Background())
	if err != nil || result.Errors != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	broken, _ := fakeClient(t, "check-errors")
	result, err = broken.Check(context.Background())
	if err == nil || result == nil || result.Errors != 2 || !result.SuggestRepairIndex {
		t.Errorf("result=%+v err=%v", result, err)
	}
}

func TestRetryLockOnlyForLockingCommands(t *testing.T) {
	r, record := fakeClient(t, "silent")
	r.RetryLock = 15 * time.Minute
	if err := r.Unlock(context.Background()); err != nil {
		t.Fatal(err)
	}
	if args := recorded(t, record).Args; slices.Contains(args, "--retry-lock") {
		t.Errorf("unlock must not wait: %v", args)
	}
	checking, record := fakeClient(t, "check")
	checking.RetryLock = 15 * time.Minute
	if _, err := checking.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(recorded(t, record).Args, " ")
	if !strings.Contains(args, "--retry-lock 15m0s") {
		t.Errorf("args = %s", args)
	}
}
