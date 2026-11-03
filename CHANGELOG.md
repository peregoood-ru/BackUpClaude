# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `backupclaude ui`: a page in the browser for people who do not want a command line. It starts a small server on `127.0.0.1` (random port), opens the default browser and ends when the tab is closed, so nothing stays running. Screens: **State** (is the data safe or is action needed, the age of each copy, size, disks, checks, scheduled tasks, a **Save now** button), **Snapshots**, **Restore** (a session or a project, into a separate folder or back in place after a preview and a confirmation) and **Settings** (language, retention, alarm threshold, extra and excluded folders, the status icon). English and Russian.
- The page holds no logic: its buttons run `status`, `list`, `backup`, `restore` and `schedule status` in the same process and show their `--json` answers; the texts come from `locales/`. It is embedded with `go:embed` and needs neither the internet nor Node.
- Safety of the page: 127.0.0.1 only, a one-time token in the link that becomes a session cookie, `Host`, `Origin` and a marker header checked, a fixed list of commands, names behind `--name=`, no password anywhere (the page learns only whether it is stored), and putting files back needs a preview, a receipt valid for exactly that request, and a confirmation.
- `backupclaude tray` (Windows): a status icon in the notification area built on `fyne.io/systray`; the colour and sign (green tick, yellow exclamation mark, red cross, grey dot) follow the verdict of `status`, a click or the menu opens the page. Off by default; `tray --enable`, `tray --disable` or the tick on the Settings tab register it at login in the Run key of the user (no administrator rights).
- `docs/ui.md` and `docs/ui.ru.md`.

## [0.1.0] - not yet released

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
- Failure messages: `internal/notify` with Telegram (`sendMessage` only, never reads updates) and webhook channels, each a small module on `net/http` (no library: nothing added to the dependency tree, and the secrets are scrubbed from every error). The bot token and the webhook address live in the OS key store, the chat id in the settings; `notify telegram|webhook|remove|test` set them up. Without a channel the program works and says nothing.
- Any failed scheduled job is reported once; a failure that goes on is repeated once a day, a successful run is never reported. An undelivered message is tried again at the next run.
- Deletion control: the claude job compares the number of `.jsonl` transcripts with the previous run and raises an alarm when it fell by more than `notify.deletion_drop_percent` (20 by default; ignored below 5 earlier files). The backup still runs and older snapshots keep the deleted sessions.
- Warning when an important folder of the manifest (Claude's folder, its `projects`, a desktop data folder) was there at the previous run and is gone now, which is how a move after a Claude update shows up.
- `status` in plain words: age of the newest snapshot in each copy, size of the copies, free space on the disks, result of the last check, failed jobs and recent alarms; an external copy that is not set up is said so and is not an error. `--json` gives the same facts.
- The weekly `summary` job (a fifth scheduled task, Sunday 09:00, `schedule.summary_every_hours`) sends the same report through the channels.
- `restore`: one session (`--session`, also by the first characters of its id and from an older snapshot when the newest no longer has it), one project (`--project`, by name, part of a name or path) or everything (`--all`), from the local or the external copy (`--from`) and any snapshot (`--snapshot`). By default the files go into a new folder next to the local repository and nothing in place changes; restic reads every restored file back (`--verify`) and the answer gives the hash of the restored tree. Without flags at a terminal the command asks questions and lists the latest sessions, marking those missing on disk. A session of the desktop application brings the Claude Code transcript it names along.
- `restore --apply` also puts the missing files back in place and never replaces an existing file, not even one that differs from the copy (the copy's version stays in the restore folder). `--dry-run` shows the list first. Local Storage and IndexedDB are not put back while Claude runs: the command refuses before writing anything and says to quit Claude.
- Moving to another computer: the home folder of the snapshot becomes this home folder, the desktop application's folder the one installed here, and `--map OLD=NEW` moves other folders such as another drive. Transcript folders are renamed after the new project paths and paths inside Claude's settings are rewritten; transcripts and the user's project files are copied exactly.
- A computer without settings restores with `--repo <location>` and the password typed from paper (`--password-stdin` for scripts). The password reaches restic on its input only; restic is downloaded and checked against its hash if it is not installed yet.
- The `restore-test` job (a sixth scheduled task, every four weeks on Sunday at 08:00, `schedule.restore_test_every_hours`, 672 by default) reads a tenth of the data with `restic check --read-data-subset 10%`, restores up to 60 sample files of the newest snapshots into an empty folder next to the repository and compares the SHA-256 of each with the file on disk. A failure is reported at once like any failed job; the result shows in `status` and in the weekly summary.
- `docs/restore.md` and `docs/restore.ru.md`: what to do when a session is gone, a project is damaged, or the computer is lost.
- The setup wizard, `backupclaude setup`: eight steps (check the computer, put the program in place, install restic and rclone, questions, settings and repository, tasks, the `/backup` skill, first snapshot and password). Every question has a default that Enter accepts; the questions are the local copy, the external copy and the channel for failure messages, and answers exist as flags (`--local`, `--offsite`, `--telegram-chat-id` with `--telegram-token-stdin`, `--webhook-url-stdin`, `--skill`, `--install`, `--projects-now` and others). `--yes` asks nothing, `--json` gives one object with the steps and no secrets. It finishes by explaining how to write the password on paper and by taking the first snapshot of Claude's data.
- The local copy is refused inside the folders of Yandex Disk, OneDrive, Google Drive and Dropbox, found by name, by the environment (`OneDrive*`), by the cloud files registry of Windows and by marker files; a folder that holds other files is refused too. A disk that is not the system one is suggested.
- The wizard can be run again: it reads the settings and changes only what is answered. A settings file that would not change is not rewritten, an existing password or repository is never replaced, tasks that match the settings are left alone, a program the tasks already run is not moved without `--install`, and a repository that already has snapshots gets none unless `--snapshot` is given. A broken settings file stops the wizard instead of being overwritten.
- `backupclaude uninstall` removes the tasks, restic, rclone, the cache, the `/backup` skill it wrote, the program and its PATH entry. It never touches the copies or the password in the key store, warns about the password, and asks first. `--purge` also deletes settings, state, logs and the notification secrets; `rclone.conf` stays.
- `backupclaude update` looks for the newest published release, downloads the archive and `checksums.txt`, stops unless the SHA-256 matches, starts the new program to see that it runs and swaps it in; a running `.exe` is renamed, not overwritten, and the old file goes at the next start. Settings, schedule and copies are not touched. A private repository gives an error that says what to do (a token in `BACKUPCLAUDE_GITHUB_TOKEN`); `--from-file` installs a file from the disk, `--check` only looks.
- `backupclaude install` copies the program into `%USERPROFILE%\.backupclaude\bin` and adds the folder to the PATH of the user (the registry, no administrator rights), and `scripts/install.ps1`, published with every release, downloads and verifies a release and runs it: `irm .../releases/latest/download/install.ps1 | iex`.
- The `/backup` skill for Claude Code (`skill install|remove|status`, offered by the wizard): it calls `status`, `list` and `restore` with `--json`, restores into a separate folder first, puts files back only after the person agrees and never runs `password --show`.
- `BACKUPCLAUDE_SANDBOX=name` gives the scheduler folder, the key store group and the PATH key a name of their own, so that a test on a computer with working copies cannot touch them.
- `scripts/e2e-windows.ps1` and a CI job on a clean Windows: install from a local archive, setup, made-up data, backup, deleted files, restore, comparison of SHA-256, update, uninstall.
- `docs/install.md` and `docs/install.ru.md`, the README files rewritten for a first-time user: quick start, what is saved, security, limits, the SmartScreen warning for an unsigned file.

### Changed

- On Windows the data folder is `%USERPROFILE%\.backupclaude` instead of `%LOCALAPPDATA%\BackUpClaude`. A Store installation of Claude Desktop redirects `%LOCALAPPDATA%` into its package for everything it starts, so a scheduled task, which runs outside the package, could not see the settings. To move an older folder, copy its content.

### Fixed

- With the home folder inside a tree that is never a project, such as `/tmp` in a container, every project was dropped silently. Folders inside home now stay projects.
- `list` showed every session as not saved: `restic ls` with a path stops at direct children unless given `--recursive`. Application sessions without a transcript are now compared with the snapshot too, and a damaged description on disk no longer looks like a deleted session.

### Known limits of 0.1.0

- The copy to external storage is not done: the wizard saves a location, but nothing copies to it yet and `status` shows it as unreachable.
- Windows 10 and 11 only; the program is not signed.
- `update` needs a token while the repository is private.
