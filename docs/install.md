# Install, update, remove

Everything here needs no administrator rights. The program lives in your own folder: `%USERPROFILE%\.backupclaude` on Windows. Nothing is written to Program Files or the system.

## Install

In PowerShell:

```powershell
irm https://github.com/peregoood-ru/BackUpClaude/releases/latest/download/install.ps1 | iex
```

The script downloads `backupclaude_<version>_windows_amd64.zip` and `checksums.txt`, compares the SHA-256 and stops if they differ, then runs `backupclaude.exe install` (copies the program to `%USERPROFILE%\.backupclaude\bin` and adds that folder to your PATH) and `backupclaude setup`.

Options go through a script block:

```powershell
& ([scriptblock]::Create((irm https://github.com/peregoood-ru/BackUpClaude/releases/latest/download/install.ps1))) -NoSetup
```

| Option | Meaning |
| --- | --- |
| `-Version 0.1.0` | A given version instead of the newest |
| `-FromFile <zip or exe>` | Install from a file on the disk, no download |
| `-Sha256 <hash>` | Expected hash of `-FromFile` (without it a local file is not verified) |
| `-NoSetup` | Install the program and stop |
| `-NoPath` | Do not change the PATH |

While the repository is private the script needs a GitHub token that may read it, in `BACKUPCLAUDE_GITHUB_TOKEN`; without one it says so. The alternative is to download the zip by hand and use `-FromFile`.

A new terminal window is needed after the PATH changes, so that the short command `backupclaude` works.

## The wizard

`backupclaude setup` has eight steps: it checks the computer (system, data folder, key store, Claude's data, scheduler), puts the program in place, installs restic and rclone, asks the questions, writes the settings, sets up the password and the repository, creates the tasks, offers the skill and takes the first snapshot. A sample (shortened, in English; `--lang ru` gives Russian):

```
Step 4 of 8: Questions
Where to keep the local copy (outside Yandex Disk, OneDrive, Google Drive and Dropbox) [D:\ClaudeBackup\repo]:
An external copy keeps your data if this computer is lost, stolen or burned. It can be any rclone remote ...
External copy: rclone:remote:folder, sftp:user@host:/path, a folder, or Enter to skip:
  [attention] There is no external copy: if this computer is lost, stolen or burned, the copies go with it
Where to send failure messages?
  1. Nowhere (look at status yourself)
  2. Telegram bot
  3. Webhook address
Your choice [1]:
```

Each question has a default. The local copy is offered on the roomiest disk that is not the system one. A folder inside Yandex Disk, OneDrive, Google Drive or Dropbox is refused with the reason, and so is a folder that holds other files.

At the end the wizard explains how to write the password on paper. At a terminal it can show the password after a typed `yes`; in a script it never does, it points to `backupclaude password --show` instead.

### Running it again

`setup` can be run any number of times. It reads the settings first and changes only what you answer:

- `config.json` is rewritten only if an answer changes it; otherwise it stays byte for byte.
- The password and the repository are never replaced.
- A scheduled task that matches the settings is left alone; a missing or different one is installed.
- If the tasks already run a program from some folder, they keep running it. `--install` moves them to `%USERPROFILE%\.backupclaude\bin`.
- A repository that already holds snapshots gets no new snapshot unless you pass `--snapshot`.
- A settings file that cannot be read is never overwritten.

### What to save

The wizard asks: **1. Only Claude's data** (the default) or **2. Claude's data and the folders of your projects**. Claude's data are the sessions, chats, context and settings. Project folders are the working folders found in the transcripts; the folders you added to `sources.extra` go with them while they are on, and move to Claude's data when they are off, so a folder you added by hand is never dropped without a word. If your projects already live in a synced or backed-up place, leave them out. The same switch is in `config.json`:

```json
"profiles": { "claude": true, "projects": false }
```

and on the **Settings** tab of `backupclaude ui`. A profile that is off has no scheduled task, `backup` skips it, and `status` does not call it late. Snapshots made earlier are kept and can be restored; the program never deletes them on its own. If you do not need them any more, remove them yourself with restic: `restic forget --tag projects --prune` with the repository and the `--password-command` of the program. A `config.json` without `profiles` keeps both on. After editing the file run `backupclaude schedule install` to bring the tasks in line.

### Without questions

`backupclaude setup --yes` takes every default. The answers have flags: `--local`, `--offsite` / `--no-offsite`, `--telegram-chat-id` with `--telegram-token-stdin`, `--webhook-url-stdin`, `--no-notify`, `--skill` / `--no-skill`, `--install` / `--no-install`, `--save claude|all|projects`, `--projects-now`, `--no-snapshot`. With `--json` the answer is one object with the steps, and no secret is in it. A token is read from the first line of the input, never from an argument.

## The /backup skill for Claude Code

The wizard offers it; you can also run `backupclaude skill install` and `backupclaude skill remove`. The skill is one file, `~/.claude/skills/backup/SKILL.md`. It tells Claude to call `status`, `list` and `restore` with `--json`, to restore into a separate folder first and put files back with `--apply` only after you agree, and never to run `password --show`. A skill of the same name that this program did not write is left untouched.

In Claude Code type `/backup`, or just ask: "bring back the session I deleted yesterday".

## Update

```
backupclaude update --check     only look
backupclaude update             download, verify, replace
```

The program looks for the newest published release, downloads the archive for your system and `checksums.txt`, and stops unless the SHA-256 matches. It starts the new file to see that it runs, then swaps it in. A running `.exe` cannot be overwritten on Windows but can be renamed, so the old file is renamed to `backupclaude.exe.old` and deleted at the next start. The settings, the schedule and the copies are not touched; the tasks keep pointing at the same file. If the new version pins other versions of restic and rclone, it fetches them.

For a private repository set `BACKUPCLAUDE_GITHUB_TOKEN`; without it the command says that the repository is private and what to do. `backupclaude update --from-file <zip or exe> [--sha256 <hash>]` installs a file you downloaded yourself.

## Uninstall

```
backupclaude uninstall          asks first
backupclaude uninstall --yes    asks nothing
```

It removes the tasks of the Task Scheduler, restic and rclone, the cache, the `/backup` skill (only if this program wrote it), the program itself and its folder from the PATH. It **does not touch**:

- the local copies and the external copy;
- the password in the key store (Credential Manager, entry `BackUpClaude:repository-password`). Before you do anything else make sure the password is on paper;
- the settings and logs in `%USERPROFILE%\.backupclaude`, so that a later `setup` picks them up. `--purge` removes them too, together with the bot token and the webhook address in the key store. `rclone.conf`, which may hold the login to the external storage, always stays.

The running program cannot delete itself on Windows: a hidden helper removes the file a few seconds after the command ends.

## Trying it on a computer that already has backups

`BACKUPCLAUDE_SANDBOX=name` (letters and digits) gives the scheduler folder, the key store group and the PATH key a name of their own, so that a test never touches the real ones. Together with a different `BACKUPCLAUDE_HOME` and `USERPROFILE` it is a second, separate installation. `scripts/e2e-windows.ps1 -Exe backupclaude.exe -Sandbox name` does that for the full chain.

## Troubleshooting

- *"The folder ... lies inside Yandex Disk"*: choose a folder outside the synchronised ones, for example on another disk.
- *"No access to the releases"*: the repository is private; see Update.
- *Windows protected your PC*: the program is not signed; see the README.
- *The command `backupclaude` is not found*: open a new terminal, or run the program by its full path `%USERPROFILE%\.backupclaude\bin\backupclaude.exe`.
- `backupclaude doctor` finds most other problems and says what to do.
