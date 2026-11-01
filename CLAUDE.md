# BackUpClaude: rules for working in this repository

BackUpClaude is a Go application with a command line and a local browser page. It saves Claude's data and the user's projects to two encrypted copies using restic and rclone as separate programs. Different people install it, so nothing here may assume one person's computer.

## Hard rules

1. **No personal data in the repository.** No names, personal e-mail addresses, home paths, tokens, ids, project names or chat ids, in code, tests, docs, commit messages or fixtures. Personal settings live in the user's data folder. Commits use only the `peregoood-ru` account with its noreply address. `STATE.md`, `ACCEPTANCE.md` and `TZ.local.md` hold personal data and are in `.gitignore`: never force-add them.
2. **Paths come only from settings and manifests.** Code never hardcodes a user path or a location of Claude's data. Claude's paths live in `manifests/<os>.json`, the user's paths in `config.json`. Everything is built with `filepath`; Cyrillic, spaces and long paths must work.
3. **OS-specific code sits behind interfaces and build tags.** Shared code calls an interface; `windows.go`, `darwin.go`, `linux.go` or `*_windows.go` implement it with a `//go:build` line. No `runtime.GOOS` branches for behaviour in shared code.
4. **All user-facing text comes from `locales/`.** Add every message to both `en.json` and `ru.json`, with the same placeholders. Errors use `i18n.E(key, args...)` and a `.Hint(...)` that says what to do. No literal messages in Go code. Tests enforce this for literal keys.
5. **Every package has tests.** New behaviour comes with tests. Tests never touch the real `~/.claude`, the real key store or the network: use `testdata/fake-home`, `secrets.NewMemory`, `httptest` and the fake restic in `internal/engine/restic_test.go`.
6. **Do not copy GPL code.** restic and rclone run as separate programs. Backrest, resticprofile, Claude-Session-Backup and vykar are GPL: read them for ideas, copy nothing. Anything borrowed from permissively licensed projects goes into `NOTICE`.

## Secrets

- The repository password lives only in the OS key store and reaches restic through `--password-command`.
- Never print it, log it, pass it as an argument or put it in an environment variable. `password --show` is the single exception: it works only for a person at a terminal, and an agent must never run it. The hidden `restic-password` command is for restic alone; never run it by hand.
- An existing password is never replaced. That would lock the user out of the repository.

## Conventions

- Every command supports `--json`. The answer is one envelope: `ok`, `command`, `data`, `error`. Field names use snake_case. Error `code` values are locale keys, so treat them as a public interface.
- Exit codes: 0 success, 1 failure, 2 wrong usage, 3 not implemented yet.
- `config.json` is parsed strictly: unknown fields are errors. Changing its meaning requires a new `version` and a migration.
- External tools are pinned in `tools.lock.json` with SHA-256 from their official release checksums. Never download without checking the hash.
- Restore never overwrites existing files.

## Workflow

- From stage 2 on, every change goes through a branch and a pull request. Direct pushes to `main` were allowed only at stage 1.
- CI must be green: `go vet`, `golangci-lint`, `go test`, gitleaks, a GoReleaser snapshot build.
- Keep `CHANGELOG.md` up to date. Versions follow SemVer.
- End commit messages with the `Co-Authored-By` line the session asks for.
- On the development PC memory is short: set `GOMAXPROCS=2` and `GOFLAGS=-p=2`, run one build at a time, leave `golangci-lint` to CI.
