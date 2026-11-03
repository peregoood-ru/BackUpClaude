package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
)

// Unlock removes stale locks and nothing else. A lock is stale when its owner
// is gone: its process no longer runs on this computer, or it has not been
// refreshed for half an hour. A lock of a live run stays. A computer that
// crashes in the middle of a run leaves such locks behind, and they would block
// the next forget or prune.
func (r *Restic) Unlock(ctx context.Context) error {
	return r.run(ctx, r.repoArgs("unlock"), nil, nil)
}

// Retention says how many snapshots of each kind forget keeps. A zero field
// keeps nothing of that kind.
type Retention struct {
	Hourly, Daily, Weekly, Monthly int
}

// ForgetOptions selects what Forget does besides applying the policy.
type ForgetOptions struct {
	// Prune frees the space of the removed snapshots.
	Prune bool
	// DryRun reports what would go and removes nothing.
	DryRun bool
}

// ForgetResult counts the snapshots the policy kept and removed.
type ForgetResult struct {
	Groups  int      `json:"groups"`
	Kept    int      `json:"kept"`
	Removed int      `json:"removed"`
	Gone    []string `json:"gone,omitempty"`
}

// retentionGroup groups snapshots by host and tags, the way a profile's backup
// finds its parent snapshot: the claude and projects profiles are each judged
// alone, and other tags (a selftest, for example) stay in their own groups.
const retentionGroup = "host,tags"

// Forget applies the retention policy.
func (r *Restic) Forget(ctx context.Context, policy Retention, opts ForgetOptions) (*ForgetResult, error) {
	args := []string{"--group-by", retentionGroup}
	for _, keep := range []struct {
		flag  string
		count int
	}{
		{"--keep-hourly", policy.Hourly}, {"--keep-daily", policy.Daily},
		{"--keep-weekly", policy.Weekly}, {"--keep-monthly", policy.Monthly},
	} {
		if keep.count > 0 {
			args = append(args, keep.flag, strconv.Itoa(keep.count))
		}
	}
	if len(args) == 2 {
		// Without any keep rule restic would have nothing to go by.
		return nil, i18n.E("engine.restic.no_retention").Hint("engine.restic.no_retention.hint")
	}
	if opts.Prune {
		args = append(args, "--prune")
	}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	result := &ForgetResult{}
	err := r.run(ctx, r.repoArgs("forget", args...), func(line []byte) {
		if !bytes.HasPrefix(line, []byte("[")) {
			return
		}
		var groups []struct {
			Keep   []json.RawMessage `json:"keep"`
			Remove []struct {
				ShortID string `json:"short_id"`
			} `json:"remove"`
		}
		if json.Unmarshal(line, &groups) != nil {
			return
		}
		for _, group := range groups {
			result.Groups++
			result.Kept += len(group.Keep)
			result.Removed += len(group.Remove)
			for _, gone := range group.Remove {
				result.Gone = append(result.Gone, gone.ShortID)
			}
		}
	}, nil)
	if err != nil {
		return result, err
	}
	return result, nil
}

// CheckResult is the summary of restic check.
type CheckResult struct {
	Errors             int  `json:"errors"`
	SuggestRepairIndex bool `json:"suggest_repair_index"`
	SuggestPrune       bool `json:"suggest_prune"`
}

// Check verifies the structure of the repository: indexes, packs and trees. It
// does not read the data blobs.
func (r *Restic) Check(ctx context.Context) (*CheckResult, error) {
	var result *CheckResult
	err := r.run(ctx, r.repoArgs("check"), func(line []byte) {
		var summary struct {
			MessageType        string `json:"message_type"`
			NumErrors          int    `json:"num_errors"`
			SuggestRepairIndex bool   `json:"suggest_repair_index"`
			SuggestPrune       bool   `json:"suggest_prune"`
		}
		if json.Unmarshal(line, &summary) == nil && summary.MessageType == "summary" {
			result = &CheckResult{Errors: summary.NumErrors, SuggestRepairIndex: summary.SuggestRepairIndex, SuggestPrune: summary.SuggestPrune}
		}
	}, nil)
	if err != nil {
		return result, err
	}
	if result == nil {
		return nil, i18n.E("engine.restic.bad_output")
	}
	return result, nil
}

// StatsResult is the size of a repository.
type StatsResult struct {
	// Bytes is what the repository occupies, packs after compression.
	Bytes     int64 `json:"total_size"`
	Files     int64 `json:"total_file_count"`
	Blobs     int64 `json:"total_blob_count"`
	Snapshots int64 `json:"snapshots_count"`
}

// Stats measures the repository in raw-data mode: the real size on the storage,
// read from the index without touching the data.
func (r *Restic) Stats(ctx context.Context) (*StatsResult, error) {
	var result *StatsResult
	err := r.run(ctx, r.repoArgs("stats", "--mode", "raw-data"), func(line []byte) {
		var stats StatsResult
		if json.Unmarshal(line, &stats) == nil && stats.Bytes > 0 {
			result = &stats
		}
	}, nil)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, i18n.E("engine.restic.bad_output")
	}
	return result, nil
}
