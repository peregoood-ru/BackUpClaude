# BackUpClaude

[Русская версия](README.ru.md)

> **Unofficial project.** It is not affiliated with, endorsed by or supported by Anthropic.

BackUpClaude keeps your Claude data and your projects safe: Claude's sessions, memory and settings every hour, project folders every day, in encrypted copies. It is a single file with no dependencies to install. Backups are done by [restic](https://github.com/restic/restic) and [rclone](https://github.com/rclone/rclone), which the setup wizard installs itself, at pinned versions with verified hashes. No server and no account: every person has their own password and their own copies.

**Status: version 0.1.0 is being prepared.** Windows 10 and 11 are supported; macOS and Linux come later. The repository is private for now, so the one-line install below works only after it is opened; until then use the manual way. The copy to external storage is **not** done yet: the wizard can remember a location for it, but nothing copies there in this version (see [Limits](#limits)). Read that part before you rely on it.

## Quick start

You need Windows 10 or 11. Administrator rights are not needed.

**One command**, in PowerShell:

```powershell
irm https://github.com/peregoood-ru/BackUpClaude/releases/latest/download/install.ps1 | iex
```

It downloads the release, checks the SHA-256 against the published `checksums.txt`, puts the program into `%USERPROFILE%\.backupclaude\bin`, adds that folder to your PATH and starts the wizard.

**By hand:** download `backupclaude_<version>_windows_amd64.zip` and `checksums.txt` from the releases page, compare the hash (`Get-FileHash file.zip`), unpack and run:

```
backupclaude.exe setup
```

The wizard asks a few questions, each with a default you accept by pressing Enter:

1. **Where to keep the local copy.** Any folder outside Yandex Disk, OneDrive, Google Drive and Dropbox: a sync service would copy a deleted or damaged file to the cloud. The wizard refuses those folders and suggests another disk.
2. **External copy.** `rclone:remote:folder`, `sftp:user@host:/path` or a folder, or skip. Skipping is allowed and the wizard says plainly what it means.
3. **Where to send failure messages.** Nowhere, a Telegram bot or a webhook.
4. **The `/backup` skill for Claude Code**, so that you can ask Claude to check the backups or bring a session back.

Then it creates the password, the repository and the schedule, takes the first snapshot and shows how to write the password on paper. **Do that.** Without the password the copies cannot be opened and nobody can recover it.

Run `backupclaude setup` again at any time: it changes only what you answer and leaves the rest. `backupclaude setup --yes` asks nothing and takes the defaults, and every answer has a flag for scripts (`backupclaude setup --help`).

### SmartScreen

The program is not signed with a code-signing certificate yet. If you download the file in a browser, Windows may say *"Windows protected your PC"*. Compare the SHA-256 of the file with `checksums.txt` of the release first, then choose **More info**, **Run anyway**. The install command above downloads the file itself, checks the hash and does not trigger the warning.

## Commands

| Command | What it does |
| --- | --- |
| `backupclaude setup` | The wizard: checks the computer, installs restic and rclone, asks, sets up the password, repository, schedule and the first snapshot. Safe to run again |
| `backupclaude status` | Whether the data is safe, in plain words: age of the newest snapshot, size, free space, last check, failures |
| `backupclaude backup` | Take a snapshot now, `--profile claude` or `projects` for one of them, `--dry-run` to store nothing |
| `backupclaude list` | Snapshots, sessions and projects |
| `backupclaude restore` | Bring back a session (`--session`), a project (`--project`) or everything (`--all`) into a new folder; `--apply` also puts the missing files back in place and never replaces existing ones. See [docs/restore.md](docs/restore.md) |
| `backupclaude doctor` | Find a problem and suggest a fix |
| `backupclaude password` | Check that the password exists, `--show` displays it for copying to paper |
| `backupclaude notify` | Where failure messages go (`telegram`, `webhook`, `test`, `remove`) |
| `backupclaude schedule` | The tasks in the Task Scheduler (`install`, `remove`, `status`) |
| `backupclaude update` | Look for a newer release, check its hash, replace the program. Settings, schedule and copies are not touched |
| `backupclaude uninstall` | Remove the tasks and the programs. The copies and the password stay |
| `backupclaude install` | Copy the program into your folder and add it to the PATH (the install command runs it) |
| `backupclaude skill` | Install or remove the `/backup` skill for Claude Code |
| `backupclaude ui` | Open the page in a browser (planned) |

Add `--json` to any command for scripts, and `--lang ru` or `--lang en` to choose the language. More: [docs/install.md](docs/install.md).

## What is saved

- **Profile `claude`**, every hour: the whole `~/.claude` folder and `~/.claude.json`, and the Claude application's data, in a classic installation or a Microsoft Store one. Login tokens (`.credentials.json`), caches, logs and programs that install themselves again stay out; the full list is in `manifests/`.
- **Profile `projects`**, every day: folders Claude worked in, found in session transcripts and remembered after old sessions are deleted, plus folders from `sources.extra` in the settings. Dependencies, virtual environments and caches stay out, and inside a git repository so do build folders.

Your home folder and temporary folders are not treated as projects. In `sources.exclude` a full path leaves out that folder or file, `*` at the end of a path is a mask, and a bare name leaves that name out at any depth. Snapshots are kept 48 hourly, 30 daily, 12 weekly and 24 monthly by default (`retention.*`). Once a week the repository is checked, and every four weeks a sample of files is restored and compared with the originals.

## Notifications

A failed scheduled job is reported to you once, a successful run never is. The same goes for a sudden fall in the number of Claude sessions (more than `notify.deletion_drop_percent` since the previous run, 20 by default) and for a Claude folder that was there and is gone. Once a week a summary arrives even when all is well. Channels are Telegram (the bot token is kept in the system key store, the chat id in `config.json`) and a webhook (its address is kept in the key store). Without a channel the program works and says nothing; `status` shows the same facts.

## Restore

Without flags at a terminal, `backupclaude restore` asks what to bring back and lists the latest sessions. Step-by-step instructions for a lost session, a damaged project and a new computer are in [docs/restore.md](docs/restore.md). With the `/backup` skill you can also tell Claude Code "bring back yesterday's session".

## Security

- Data is encrypted on your computer before it leaves. Storage only sees unreadable files.
- The password lives in the OS key store: Credential Manager on Windows, Keychain on macOS, Secret Service on Linux. It is never printed, logged or passed in an argument; restic gets it through `--password-command`. `password --show` works only for a person at a terminal.
- **Without the password the copies cannot be opened, and nobody can recover it.** The wizard shows how to copy it to paper.
- Restore never overwrites an existing file.
- Downloads of restic, rclone and of updates are checked against SHA-256 before they are used. restic and rclone are pinned in `tools.lock.json`.
- Session transcripts can contain tokens and passwords you typed while working. That is why copies are encrypted before they leave the computer, and why there is no shared password.
- `uninstall` never touches the copies or the password in the key store.

## Limits

- **No automatic copy to external storage yet.** `setup` accepts an external location and keeps it in the settings, but this version does not copy to it, and `status` shows the external copy as not reachable. Until that stage is delivered, a lost computer takes the local copy with it. Keep the local copy on another physical disk, and do not count on it alone.
- Windows 10 and 11 only (64-bit Intel/AMD). The code keeps system-specific parts apart, and macOS and Linux builds are planned.
- The scheduled tasks run in your Windows session: when the computer is off, or nobody is logged in, nothing runs, and a missed run starts after you log in. Files that a running program holds open are skipped and saved at the next run.
- Restoring the databases of the Claude desktop application (Local Storage, IndexedDB) needs Claude closed; the command refuses and says so.
- Chats of claude.ai live in the cloud of Anthropic and are not saved.
- `update` reads the releases of a private repository only with a GitHub token in `BACKUPCLAUDE_GITHUB_TOKEN`; without it the command says so and exits. `update --from-file` installs a file you downloaded yourself.
- The program is not signed (see [SmartScreen](#smartscreen)).
- Hourly snapshots are quick, but the first snapshot of projects can take half an hour or more.

## Building

Requires Go, see the version in `go.mod`.

```
go build ./cmd/backupclaude
go test ./...
```

`scripts/e2e-windows.ps1` runs the whole chain on a made-up profile (install, setup, backup, deleted files, restore, comparison of hashes, update, uninstall); the CI runs it on a clean Windows. On a computer with a real installation run it with `-Sandbox name`: the task folder, the key store group and the PATH key get that name, and the real ones are not touched.

## License

MIT, see [LICENSE](LICENSE). Third-party notices are in [NOTICE](NOTICE).
