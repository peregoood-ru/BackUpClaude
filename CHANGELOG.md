# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Application skeleton on Cobra with all planned commands; `--version` and `doctor` work, the rest explain which stage delivers them.
- `--json` on every command and messages in English and Russian from `locales/`.
- Strict `config.json` loading with readable errors, and a data folder chosen by the rules of the OS.
- `internal/engine`: pinned versions and SHA-256 in `tools.lock.json`, verified download of restic and rclone, running restic and parsing its JSON answers.
- Repository password: generation, storage in the OS key store, delivery to restic through `--password-command`, and `password --show` for a person at a terminal.
- `repo init`, `tools install` and `selftest` as service commands.
- CI on Windows and Linux, plus macOS on `main`: `go vet`, `golangci-lint`, `go test`, gitleaks, GoReleaser snapshot build.
- Manifests of what to save and what to leave out, embedded with `go:embed`. Claude's folders are saved whole except for an exclude list, so a folder a future version adds is not lost.
- Desktop data is found in the classic location and in a Microsoft Store package, and a folder seen under two paths counts once.
- Project discovery by the `cwd` field of transcripts and session descriptions, read as a stream. A session started in the home folder or Temp is followed to the end, because it may be the only trace of a project. Found projects stay in `projects.json` after their sessions are deleted.
- Profiles `claude` and `projects`, the `backup` command with `--profile` and `--dry-run`, and the `list` command for snapshots, sessions and projects.
- Service commands `measure` for size and heavy media, and `verify` to read what a snapshot really holds.
- Automatic runs: `schedule install|remove|status` puts four tasks into the Windows Task Scheduler (`claude` hourly, `projects` daily, `cleanup` and `check` weekly; intervals from `schedule.*_every_hours` in the settings). The tasks run as the user, without administrator rights and without a stored password, show no window, catch up after the computer was off (`StartWhenAvailable`) and never run twice at once.
- `run --job` is what a task runs: it lowers CPU, disk and memory priority (restic inherits it), ties restic to the run with a job object, removes stale locks, does the job and appends one line, with the peak memory, to `logs/runs-YYYY-MM.jsonl`.
- Stale locks are removed before every backup (`restic unlock`, never `--remove-all`), so a crash of the computer does not block the next run.
- Retention from `retention.*` in the settings (48 hourly, 30 daily, 12 weekly, 24 monthly by default) is applied by the weekly `cleanup`, grouped by host and tags, followed by prune. `check` runs `restic check` weekly.

### Changed

- On Windows the data folder is `%USERPROFILE%\.backupclaude` instead of `%LOCALAPPDATA%\BackUpClaude`. A Store installation of Claude Desktop redirects `%LOCALAPPDATA%` into its package for everything it starts, so a scheduled task, which runs outside the package, could not see the settings. To move an older folder, copy its content.

### Fixed

- With the home folder inside a tree that is never a project, such as `/tmp` in a container, every project was dropped silently. Folders inside home now stay projects.
- `list` showed every session as not saved: `restic ls` with a path stops at direct children unless given `--recursive`. Application sessions without a transcript are now compared with the snapshot too, and a damaged description on disk no longer looks like a deleted session.
