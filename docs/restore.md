# Restore

[Русский](restore.ru.md)

This page covers three situations: a session is gone, a project is damaged, the computer died and a new Windows is installed. The commands are the same in all three; only what you bring back differs.

## How it works

- **Always into a separate folder first.** `backupclaude restore` puts the files into a new folder next to the local repository, for example `D:\ClaudeBackup\restored\2026-11-03_195814`, and changes nothing in place. Inside, the original path is repeated: `C\Users\Name\.claude\projects\...`.
- **Every file is checked.** restic reads every restored file back and compares it with the copy. At the end the command prints a hash of the whole restored tree.
- **`--apply` only adds.** With this flag the missing files are also put back where they were. An existing file is never replaced, not even one that differs from the copy. The copy's version stays in the restore folder.
- **`--dry-run` together with `--apply`** shows what would be put back and writes nothing.
- **The application's databases are never mixed.** Local Storage and IndexedDB of the Claude application come back only whole, and only while Claude is closed. While it runs, the command refuses and changes nothing.
- **Without flags it asks questions.** Run `backupclaude restore` at a terminal: it asks what to bring back and where to, and lists the latest sessions. Sessions that are no longer on disk are marked "only in copy".
- **The external copy.** By default a restore reads the local copy; `--from offsite` picks the external one. If the external copy is not set up yet, the command says so.

`backupclaude restore --help` lists every flag.

## 1. A session is gone

Sign: the session is missing from Claude Code (`claude --resume`) or from the application.

1. Find the id of the session.

   ```
   backupclaude list --sessions
   ```

   A session that is no longer on disk is marked "only in copy". You can also skip this step: `backupclaude restore` without flags shows the latest sessions with numbers.

2. See what comes back. Nothing in place changes yet.

   ```
   backupclaude restore --session 0fc6092b
   ```

   The first 8 characters of the id are enough. If the newest snapshot no longer has the session, the command finds it in an older one. `--snapshot <id>` picks a snapshot, and `backupclaude list --snapshots` lists them.

3. Put the session back.

   ```
   backupclaude restore --session 0fc6092b --apply
   ```

   The transcript comes back, together with the session's folder (subagents, tool output) and the history of file edits. Whatever is already on disk stays as it is.

4. Open the session. The command prints which folder to open it from:

   ```
   cd "D:\Projects\site"
   claude --resume 0fc6092b-f4d7-460e-ab01-acc244289ebe
   ```

   **A session of the Claude application** (Desktop or Cowork) comes back with its description and with the Claude Code transcript the description names. The application keeps its list of sessions in memory, so quit Claude completely, including the icon next to the clock, and start it again.

## 2. A project is damaged

Sign: project files were deleted or broken, for example by a bad edit, a sync tool or malware.

1. **Do nothing with the damaged folder** until you have a restored copy.

2. Choose a snapshot from before the damage. Projects are saved once a day.

   ```
   backupclaude list --snapshots
   ```

3. Restore the project into a separate folder and compare it with the damaged one.

   ```
   backupclaude restore --project site --snapshot 0a83c76c
   ```

   `--project` takes the name of the project folder, a part of it or the full path. Without `--snapshot` the newest snapshot is used. You can move the files you need out of the restore folder by hand.

4. Or put back only the files that are missing.

   ```
   backupclaude restore --project site --snapshot 0a83c76c --apply --dry-run
   backupclaude restore --project site --snapshot 0a83c76c --apply
   ```

   `--apply` does not replace changed files. The command says how many there are and that they differ from the copy. To get the project exactly as in the snapshot, rename the damaged folder, for example to `site-damaged`, and run the command with `--apply`: the whole project comes back.

   To bring the project back to another place, add `--map "OLD_PATH=NEW_PATH"`.

## 3. The computer died, a new Windows is installed

You only have the password on paper and access to the external storage. A local copy works as well if its disk survived. The user name and drive letters on the new computer may differ.

1. **Download `backupclaude.exe`** to any folder, for example `C:\Tools`. No administrator rights are needed. restic is downloaded at the first restore, and its hash is checked against the one pinned in the program.

2. **Prepare access to the copy.**
   - A copy on a disk (USB, NAS, a surviving drive): you only need the path of the repository folder, for example `E:\ClaudeBackup\repo`.
   - A copy in external storage through rclone, for example Yandex Disk: an address like `rclone:yandex:BackUpClaude`. You also need the rclone login settings in `%USERPROFILE%\.backupclaude\rclone.conf`. Connecting the storage will be described in `docs/storage.md` together with the external copy.

3. **Restore everything into a separate folder.**

   ```
   C:\Tools\backupclaude.exe restore --repo E:\ClaudeBackup\repo --all
   ```

   The command asks for the password. Type it from paper: the characters are not shown, and the password is not stored anywhere. The newest snapshots of Claude's data and of the projects come back. The restore folder appears next to the repository, or in `%USERPROFILE%\BackUpClaude\restored` for an rclone address.

4. **Install Claude.** Install Claude Code and the Claude application and sign in. The sign-in file `.credentials.json` is never in the copy, so you have to sign in again. Start the application once and quit it completely, including the icon next to the clock.

5. **See where everything goes.**

   ```
   C:\Tools\backupclaude.exe restore --repo E:\ClaudeBackup\repo --all --apply --dry-run
   ```

   The lines "path … becomes …" show the move: the old home folder becomes the new one, and the old application folder becomes the application folder on this computer. If projects lived on a drive this computer lacks (for example `D:`), the command says so. Name the new place:

   ```
   ... --map "D:\YandexDisk=E:\YandexDisk"
   ```

6. **Put the files back.** Run the same command without `--dry-run`. The transcript folders are renamed after the new project paths, and paths inside Claude's settings (`.claude.json`, session descriptions) are rewritten. Transcripts and project files are copied unchanged.

   A freshly installed application already has its own Local Storage and IndexedDB, so the old databases do not come back: databases are never mixed. To take the old ones, quit Claude, rename those folders (their path is printed) and run the command again. The application's sidebar then looks as before.

7. **Open a session** with `claude --resume <id>` from the project folder, as in situation 1.

8. **Turn backups on again.** The `backupclaude setup` wizard (coming in version 0.1.0) connects the same copy, so the history goes on.

## When something goes wrong

| Message | What to do |
| --- | --- |
| "The password does not open this repository" | Check the password on paper: groups of 4 characters with dashes, no I, L, O or U. Check that this is the right copy. |
| "The external copy is not set up yet" | Restore from the local copy (`--from local`, the default). |
| "Claude is running…" | Quit Claude completely and run the command again. The restored files are already in the folder whose path is printed. |
| "Not put back: … on a drive this computer does not have" | Add `--map "OLD=NEW"`. |
| "Session … is in none of the snapshots" | A session younger than the last hourly snapshot is not saved yet. Check the id with `backupclaude list --sessions`. |

## Trial restore

Every four weeks, on Sunday at 08:00, the `restore-test` job checks that the copies can be read back. It checks a tenth of the data with `restic check --read-data-subset 10%`. Then it restores up to 60 random files of the newest snapshots into an empty folder next to the repository and compares the SHA-256 of each with the original on disk, and removes the folder afterwards. A failure is reported at once. A success shows in `backupclaude status` and in the weekly summary. To run it by hand: `backupclaude run --job restore-test`.

`restic mount` is not used: restic 0.19.1 for Windows has no such command.
