# BackUpClaude

[Русская версия](README.ru.md)

> **Unofficial project.** It is not affiliated with, endorsed by or supported by Anthropic.

BackUpClaude keeps your Claude data and your projects safe: Claude's sessions, memory and settings every hour, project folders every day, in two encrypted copies, one on a local disk and one in external storage. It is a single file with no dependencies to install. Backups are done by [restic](https://github.com/restic/restic) and [rclone](https://github.com/rclone/rclone), which it installs itself, at pinned versions with verified hashes.

**Status: early development.** The foundation works: settings, verified tool installation, the password in the OS key store, and a save and restore check. Regular backups and restore arrive at the next stages. Do not rely on it yet.

## Commands

| Command | What it does | Status |
| --- | --- | --- |
| `backupclaude doctor` | Find a problem and suggest a fix | works |
| `backupclaude password` | Check that the password exists, `--show` displays it for copying to paper | works |
| `backupclaude --version` | Version of the application and pinned tools | works |
| `backupclaude setup` | Setup wizard | planned |
| `backupclaude status` | Latest snapshots, space, errors | planned |
| `backupclaude backup` | Take a snapshot now | planned |
| `backupclaude list` | Snapshots, sessions and projects | planned |
| `backupclaude restore` | Bring back a session, a project or everything | planned |
| `backupclaude ui` | Open the page in a browser | planned |
| `backupclaude update` | Update the application | planned |
| `backupclaude uninstall` | Remove the application, copies stay | planned |

Add `--json` to any command for scripts, and `--lang ru` or `--lang en` to choose the language.

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
