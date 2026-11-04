---
name: backup
description: Check that the BackUpClaude backups are healthy, list the saved Claude sessions and projects, and bring back a lost or damaged session, project or file set from the backup. Use when the user types /backup, asks whether their backups work, says a Claude session or a project folder disappeared, or wants something restored.
---
<!-- managed by BackUpClaude. Remove it with: backupclaude skill remove -->

# /backup

BackUpClaude keeps encrypted copies of Claude's data (sessions, settings, memory) every hour and of the user's projects every day. This skill drives its command line. The program is:

```
{{COMMAND}}
```

Use that exact command in every example below. Always add `--json`: the answer is one object `{"ok": true|false, "command": "...", "data": {...}, "error": {"code", "message", "hint"}}`. When `ok` is false, tell the user the `message` and the `hint`; do not retry in a loop.

Text after `/backup` is what the user wants: `$ARGUMENTS`. With no text, run step 1 and give a short summary.

## 1. Is everything fine?

```
{{COMMAND}} status --json
```

Read `data.verdict`: `ok`, `attention` or `problem`. Report the age of the last snapshot of each profile (`copies[].profiles[].age_seconds`), whether the external copy is configured (`copies[].configured` for the role `offsite`), the last check and trial restore, and anything in `issues` and `alerts`. If there is no external copy, say so plainly: a lost computer takes the local copy with it.

## 2. What is saved?

```
{{COMMAND}} list --sessions --limit 30 --json
{{COMMAND}} list --projects --json
{{COMMAND}} list --snapshots --json
```

Each session has `id`, `title`, `cwd`, `modified`, `on_disk` and `in_snapshot`. A session with `on_disk: false` and `in_snapshot: true` is one that vanished from this computer and can be brought back.

## 3. Bring something back

Restoring is safe by design: it never overwrites an existing file. Go in two steps and ask the user before the second.

1. Restore into a folder of its own, which changes nothing in place:

   ```
   {{COMMAND}} restore --session <id or the first characters of it> --json
   {{COMMAND}} restore --project <folder name, part of it, or full path> --json
   ```

   The answer lists the folder with the files (`restored`) and a hash of the tree. Show the user what was found and where.

2. Only after the user agrees, put the missing files back where they were. Show the plan first:

   ```
   {{COMMAND}} restore --session <id> --apply --dry-run --json
   {{COMMAND}} restore --session <id> --apply --json
   ```

   `--apply` adds missing files only. A file that exists and differs is counted and left alone. If the desktop application of Claude is running, `--apply` may refuse to write its databases: ask the user to close Claude and run it again. After a session comes back, `resume` in the answer shows how to continue it with `claude --resume <id>`.

`--from offsite` reads the external copy instead of the local one. `--all` restores everything and is for a new computer: tell the user to run it themselves, in their own terminal, because it asks for the password.

## Rules

- Never run `password --show`, never read the key store, never print or ask for the repository password. If the password is needed, the user types it in their own terminal.
- Never delete snapshots or repository files, and never run `uninstall`.
- Do not edit `config.json` by hand. Settings change through `{{COMMAND}} setup`.
- If a command says the copy cannot be opened, or the password is wrong, stop and tell the user what the message says.
- Say only what the commands returned. If a snapshot is old, say how old.
