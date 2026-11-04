package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/peregoood-ru/BackUpClaude/internal/config"
	"github.com/peregoood-ru/BackUpClaude/internal/engine"
	"github.com/peregoood-ru/BackUpClaude/internal/i18n"
	"github.com/peregoood-ru/BackUpClaude/internal/restore"
	"github.com/peregoood-ru/BackUpClaude/internal/secrets"
)

const selftestTag = "selftest"

// backupLockWait is how long a backup waits for a cleanup that holds the whole
// repository. The next scheduled run tries again after that.
const backupLockWait = 20 * time.Minute

// session bundles what repository commands need.
type session struct {
	layout config.Layout
	config *config.Config
	restic *engine.Restic
}

func (a *app) openSession() (*session, error) {
	layout, err := config.Resolve()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(layout.ConfigFile())
	if err != nil {
		return nil, err
	}
	binary, err := a.resticBinary(layout)
	if err != nil {
		return nil, err
	}
	executable, err := a.deps.executable()
	if err != nil {
		return nil, i18n.E("cli.no_executable").Because(err)
	}
	return &session{
		layout: layout,
		config: cfg,
		restic: &engine.Restic{
			Binary:          binary,
			RetryLock:       backupLockWait,
			Repo:            cfg.Repositories.Local,
			PasswordCommand: engine.PasswordCommand(executable, resticPasswordCommand),
			CacheDir:        layout.CacheDir(),
		},
	}, nil
}

func (a *app) resticBinary(layout config.Layout) (string, error) {
	if a.deps.resticPath != nil {
		return a.deps.resticPath(), nil
	}
	installer, err := a.installer(layout)
	if err != nil {
		return "", err
	}
	tool, err := installer.Locate(engine.ToolRestic)
	if err != nil {
		return "", err
	}
	return tool.Path, nil
}

func (a *app) newRepoCommand() *cobra.Command {
	repo := &cobra.Command{
		Use:    "repo",
		Short:  a.tr.T("cli.repo.short"),
		Hidden: true,
		Args:   a.noArgs,
		RunE:   a.showGroup,
	}
	repo.AddCommand(&cobra.Command{
		Use:   "init",
		Short: a.tr.T("cli.repo.init.short"),
		Args:  a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.repoInit(cmd.Context())
		},
	})
	return repo
}

// repoResult is the answer of repo init. It never holds the password.
type repoResult struct {
	Repository      string `json:"repository"`
	Created         bool   `json:"created"`
	PasswordCreated bool   `json:"password_created"`
}

// ensureRepo makes sure the password and the repository exist, and creates what
// is missing. An existing password or repository is never replaced.
func (a *app) ensureRepo(ctx context.Context, current *session) (repoResult, error) {
	passwordCreated, err := secrets.EnsureRepoPassword(a.deps.secrets())
	if err != nil {
		return repoResult{}, err
	}
	result := repoResult{Repository: current.restic.Repo, PasswordCreated: passwordCreated}
	err = current.restic.Probe(ctx)
	switch {
	case err == nil:
	case engine.IsKind(err, engine.KindRepoMissing):
		if _, err := current.restic.Init(ctx); err != nil {
			return result, err
		}
		result.Created = true
	default:
		return result, err
	}
	return result, nil
}

func (a *app) repoInit(ctx context.Context) error {
	current, err := a.openSession()
	if err != nil {
		return err
	}
	result, err := a.ensureRepo(ctx, current)
	if err != nil {
		return err
	}
	return a.emit(result, func(w io.Writer) {
		if result.Created {
			fmt.Fprintln(w, a.tr.T("repo.created", result.Repository))
		} else {
			fmt.Fprintln(w, a.tr.T("repo.exists", result.Repository))
		}
		if result.PasswordCreated {
			fmt.Fprintln(w, a.tr.T("repo.password_created"))
		}
	})
}

func (a *app) newSelftestCommand() *cobra.Command {
	var source string
	cmd := &cobra.Command{
		Use:    "selftest",
		Short:  a.tr.T("cli.selftest.short"),
		Long:   a.tr.T("cli.selftest.long"),
		Hidden: true,
		Args:   a.noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.selftest(cmd.Context(), source)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", a.tr.T("flag.selftest.source"))
	return cmd
}

// selftestResult is the answer of selftest.
type selftestResult struct {
	Repository  string               `json:"repository"`
	SnapshotID  string               `json:"snapshot_id"`
	Source      string               `json:"source"`
	RestoredTo  string               `json:"restored_to"`
	Files       int                  `json:"files"`
	Bytes       int64                `json:"bytes"`
	SourceHash  string               `json:"source_tree_sha256"`
	RestoreHash string               `json:"restored_tree_sha256"`
	Match       bool                 `json:"match"`
	Differences []restore.Difference `json:"differences,omitempty"`
}

// selftest stores a folder, restores it to another place and compares hashes.
func (a *app) selftest(ctx context.Context, source string) error {
	current, err := a.openSession()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(current.layout.TempDir(), 0o700); err != nil {
		return i18n.E("selftest.workdir_failed", current.layout.TempDir()).Because(err)
	}
	work, err := os.MkdirTemp(current.layout.TempDir(), "selftest-")
	if err != nil {
		return i18n.E("selftest.workdir_failed", current.layout.TempDir()).Because(err)
	}
	defer os.RemoveAll(work)

	if source == "" {
		source = filepath.Join(work, "source")
		if err := restore.GenerateSample(source); err != nil {
			return err
		}
	} else if source, err = filepath.Abs(source); err != nil {
		return i18n.E("selftest.bad_source", source).Because(err)
	}
	want, err := restore.HashTree(source)
	if err != nil {
		return err
	}

	backup, err := current.restic.Backup(ctx, engine.BackupOptions{Paths: []string{source}, Tags: []string{selftestTag}})
	if err != nil {
		return err
	}
	target := filepath.Join(work, "restored")
	if _, err := current.restic.Restore(ctx, engine.RestoreOptions{
		Snapshot:  backup.Summary.SnapshotID,
		Subfolder: restore.SnapshotPath(source),
		Target:    target,
	}); err != nil {
		return err
	}
	got, err := restore.HashTree(target)
	if err != nil {
		return err
	}

	differences := restore.Compare(want, got)
	result := selftestResult{
		Repository:  current.restic.Repo,
		SnapshotID:  backup.Summary.SnapshotID,
		Source:      source,
		RestoredTo:  target,
		Files:       len(want.Files),
		Bytes:       want.Bytes,
		SourceHash:  want.Digest,
		RestoreHash: got.Digest,
		Match:       len(differences) == 0 && want.Digest == got.Digest,
		Differences: differences,
	}
	if err := a.emit(result, func(w io.Writer) {
		fmt.Fprintln(w, a.tr.T("selftest.snapshot", result.SnapshotID, result.Repository))
		fmt.Fprintln(w, a.tr.T("selftest.files", result.Files, result.Bytes))
		fmt.Fprintln(w, a.tr.T("selftest.source_hash", result.SourceHash))
		fmt.Fprintln(w, a.tr.T("selftest.restored_hash", result.RestoreHash))
		if result.Match {
			fmt.Fprintln(w, a.tr.T("selftest.match"))
			return
		}
		fmt.Fprintln(w, a.tr.T("selftest.mismatch"))
		for _, difference := range result.Differences {
			fmt.Fprintln(w, "  -", difference.Reason, difference.Path)
		}
	}); err != nil {
		return err
	}
	if !result.Match {
		return errReported
	}
	return nil
}
