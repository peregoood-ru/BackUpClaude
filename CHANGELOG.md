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
