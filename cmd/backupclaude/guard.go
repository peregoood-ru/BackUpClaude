package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/peregoood-ru/BackUpClaude/internal/notify"
	"github.com/peregoood-ru/BackUpClaude/internal/schedule"
	"github.com/peregoood-ru/BackUpClaude/internal/watch"
)

// deliver sends a message and says whether it is settled: it reached somebody,
// or there is nobody to reach. Only a message that no channel took stays open,
// so that the next run tries again. What went wrong with a channel goes to the
// notes of the run log, in words that hold no secret.
func (a *app) deliver(ctx context.Context, notifier *notify.Notifier, message notify.Message, record *runRecord) bool {
	if notifier.Empty() {
		record.Notes = append(record.Notes, "notify: no channel is set up")
		return true
	}
	delivered, failures := notifier.Send(ctx, message)
	for _, failure := range a.describeFailures(failures) {
		record.Notes = append(record.Notes, "notify "+failure.Channel+": "+failure.Error)
	}
	return delivered > 0
}

// guardClaude looks at Claude's folders before the claude job saves them, and
// tells the user when sessions vanished or a folder is gone. It never fails the
// job: the backup must still run, and the snapshots it makes do not destroy
// older ones.
func (a *app) guardClaude(ctx context.Context, current *session, notifier *notify.Notifier, record *runRecord) {
	in, err := a.planInputs(current.layout, current.config)
	if err != nil {
		record.Notes = append(record.Notes, "watch: "+a.tr.Describe(err).Message)
		return
	}
	path := current.layout.WatchFile()
	state, err := watch.Load(path)
	if err != nil {
		record.Notes = append(record.Notes, "watch: state file was unreadable, starting over")
	}
	now := time.Now()
	folders := watch.Important(in.Manifest, in.Env)

	folderSettled := true
	if gone := state.Vanished(folders.Present); len(gone) > 0 {
		message := notify.Message{
			Title: a.tr.T("notify.folder.title"),
			Body:  a.tr.T("notify.folder.body", strings.Join(gone, "\n"), hostName()),
		}
		folderSettled = a.deliver(ctx, notifier, message, record)
		if folderSettled {
			record.Notes = append(record.Notes, "watch: folder missing: "+strings.Join(gone, ", "))
			_ = watch.Update(path, func(s *watch.State) { s.AddAlert(now, watch.AlertFolder, message.Title) })
		}
	}

	var files int
	counted := false
	deletionSettled := true
	if folders.TranscriptsExist {
		n, err := watch.CountTranscripts(folders.Transcripts)
		if err != nil {
			record.Notes = append(record.Notes, "watch: cannot count transcripts: "+err.Error())
		} else {
			files, counted = n, true
		}
	}
	if counted {
		if drop := state.Deletion(files, current.config.Notify.DeletionDropPercent); drop.Alarm {
			message := notify.Message{
				Title: a.tr.T("notify.deletion.title"),
				Body: a.tr.T("notify.deletion.body", drop.Previous.Files, drop.Previous.Time.Format("02.01.2006 15:04"),
					drop.Current, drop.Percent, hostName()),
			}
			deletionSettled = a.deliver(ctx, notifier, message, record)
			if deletionSettled {
				record.Notes = append(record.Notes, fmt.Sprintf("watch: transcripts fell from %d to %d", drop.Previous.Files, drop.Current))
				_ = watch.Update(path, func(s *watch.State) { s.AddAlert(now, watch.AlertDeletion, message.Title) })
			}
		}
	}

	// The new numbers become the baseline only when the alarm they raised has
	// been settled: an undelivered alarm is raised again at the next run.
	if err := watch.Update(path, func(s *watch.State) {
		if counted && deletionSettled {
			s.SetTranscripts(now, files)
		}
		if folderSettled {
			s.SetFolders(folders.Present)
		}
	}); err != nil {
		record.Notes = append(record.Notes, "watch: cannot save the state: "+err.Error())
	}
}

// tellFailure tells the user that a job failed. A failure that goes on is not
// repeated at every run: see watch.State.ShouldTellFailure. Successful runs are
// never reported; they only clear the failure.
func (a *app) tellFailure(ctx context.Context, layoutWatchFile string, notifier *notify.Notifier, job string, jobErr error, record *runRecord) {
	if job == schedule.JobSummary {
		return // a summary that did not arrive cannot be reported through the same channels
	}
	now := time.Now()
	state, _ := watch.Load(layoutWatchFile)
	if !state.ShouldTellFailure(job, now) {
		return
	}
	description := a.tr.Describe(jobErr)
	hint := description.Hint
	if hint == "" {
		hint = a.tr.T("notify.failure.hint_default")
	}
	message := notify.Message{
		Title: a.tr.T("notify.failure.title", job),
		Body:  a.tr.T("notify.failure.body", hostName(), now.Format("02.01.2006 15:04"), description.Message, hint),
	}
	if a.deliver(ctx, notifier, message, record) && !notifier.Empty() {
		if err := watch.Update(layoutWatchFile, func(s *watch.State) { s.JobTold(job, now) }); err != nil {
			record.Notes = append(record.Notes, "watch: cannot save the state: "+err.Error())
		}
	}
}

// clearFailure notes that job works again.
func clearFailure(layoutWatchFile, job string) error {
	state, err := watch.Load(layoutWatchFile)
	if err != nil && !errors.Is(err, watch.ErrDamaged) {
		return err
	}
	if _, failing := state.Jobs[job]; !failing {
		return nil
	}
	return watch.Update(layoutWatchFile, func(s *watch.State) { s.JobSucceeded(job) })
}
