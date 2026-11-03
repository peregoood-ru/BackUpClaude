# BackUpClaude

[Русская версия](README.ru.md)

> **Unofficial project.** It is not affiliated with, endorsed by or supported by Anthropic.

BackUpClaude keeps your Claude data and your projects safe: Claude's sessions, memory and settings every hour, project folders every day, in two encrypted copies, one on a local disk and one in external storage. It is a single file with no dependencies to install. Backups are done by [restic](https://github.com/restic/restic) and [rclone](https://github.com/rclone/rclone), which it installs itself, at pinned versions with verified hashes.

**Status: early development.** Snapshots to the local repository run on a schedule, `restore` brings back a session, a project or everything, and a trial restore checks the copy every four weeks. The external copy and the setup wizard arrive at the next stages. Do not rely on it yet.

## Commands

| Command | What it does | Status |
| --- | --- | --- |
| `backupclaude doctor` | Find a problem and suggest a fix | works |
| `backupclaude password` | Check that the password exists, `--show` displays it for copying to paper | works |
| `backupclaude --version` | Version of the application and pinned tools | works |
| `backupclaude backup` | Take a snapshot now, `--profile claude` or `projects` for one of them, `--dry-run` to store nothing | works |
| `backupclaude list` | Snapshots, sessions and projects | works |
| `backupclaude status` | Whether the data is safe, in plain words: age of the newest snapshot in each copy, size, free space, last check, failures | works |
| `backupclaude notify` | Set up where failure messages go (`telegram`, `webhook`, `test`, `remove`) | works |
| `backupclaude setup` | Setup wizard | planned |
| `backupclaude restore` | Bring back a session (`--session`), a project (`--project`) or everything (`--all`) into a new folder; `--apply` also puts the missing files back in place and never replaces existing ones. See [docs/restore.md](docs/restore.md) | works |
| `backupclaude ui` | Open the page in a browser | planned |
| `backupclaude update` | Update the application | planned |
| `backupclaude uninstall` | Remove the application, copies stay | planned |

Add `--json` to any command for scripts, and `--lang ru` or `--lang en` to choose the language.

## Notifications

A failed scheduled job is reported to you once, a successful run never is. The same goes for a sudden fall in the number of Claude sessions (more than `notify.deletion_drop_percent` since the previous run, 20 by default) and for a Claude folder that was there and is gone, which is what a move after an update looks like. Once a week a summary arrives even when all is well. Channels are Telegram (the bot token is kept in the system key store, the chat id in `config.json`) and a webhook (its address is kept in the key store). Without a channel the program works and says nothing; `status` shows the same facts.

## Restore

Without flags at a terminal, `backupclaude restore` asks what to bring back and lists the latest sessions. Step-by-step instructions for a lost session, a damaged project and a new computer are in [docs/restore.md](docs/restore.md).

## What is saved

- **Profile `claude`:** the whole `~/.claude` folder and `~/.claude.json`, and the Claude application's data, in a classic installation or a Microsoft Store one. Login tokens, caches, logs and programs that install themselves again stay out; the full list is in `manifests/`.
- **Profile `projects`:** folders Claude worked in, found in session transcripts and remembered after old sessions are deleted, plus folders from `sources.extra` in the settings. Dependencies, virtual environments and caches stay out, and inside a git repository so do build folders.

Your home folder and temporary folders are not treated as projects. In `sources.exclude` a full path leaves out that folder or file, `*` at the end of a path is a mask, and a bare name leaves that name out at any depth.

## Security

- Data is encrypted on your computer before it leaves. Storage only sees unreadable files.
- The password lives in the OS key store: Credential Manager on Windows, Keychain on macOS, Secret Service on Linux.
- **Without the password the copies cannot be opened, and nobody can recover it.** Copy it to paper with `backupclaude password --show`.

## Building

Requires Go, see the version in `go.mod`.

```
go build ./cmd/backupclaude
go test ./...
```

## License

MIT, see [LICENSE](LICENSE). Third-party notices are in [NOTICE](NOTICE).
