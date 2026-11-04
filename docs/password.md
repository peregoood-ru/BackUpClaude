# Changing the repository password

Use it when the password was seen by somebody else (a screenshot, a photo, a chat), or just to start over.

```
backupclaude password change
```

Run it yourself in a terminal window. It refuses to work in a script, with `--json` or when started by an agent: the new password appears on screen. Have a sheet of paper ready.

## What it does

1. Asks for a typed `yes` (in Russian, `да`).
2. Checks that the local copy and the external one (if it is set up) can be opened with the current password. **If the external copy cannot be reached, it stops here and changes nothing**: check the connection and run it again. A copy is never dropped silently.
3. Generates a new password with the same generator as the first one and adds it to each copy as one more restic key (`restic key add`). The new password goes to restic in a temporary file that only your account can open; the file is wiped and deleted right after. The old password goes on restic's standard input. Neither is ever an argument or an environment variable.
4. Opens each copy with the new password. If anything fails here, the keys it added are taken back and the old password stays.
5. Saves the old password in the key store under a separate name (`repository-password-previous`), then replaces the entry `repository-password`.
6. Opens the copies the way a scheduled task does (the password from the key store), then removes the old key from each copy and checks that the old password no longer opens it and that a single key is left.
7. Deletes the saved old password and shows the new one once, with the same warning as `password --show`: write it on paper, do not photograph it, do not keep it in files or chats.

Scheduled tasks need no change. They read the password from the key store, and from step 5 on they read the new one.

## If it stops halfway

Up to step 4 nothing is lost and nothing is left behind. From step 5 on, both passwords open the copies and the key store keeps both (`repository-password` is the new one, `repository-password-previous` the old one, and `repository-password-next` the new one while the change is under way). Run `backupclaude password change` again: it says that an earlier change stopped halfway and finishes it with the same new password, which you have not seen yet. `backupclaude password --show` shows the stored one at any time.

## Other keys

Only the key of the password that was in use is removed. A key added by hand from another computer stays, so `restic key list` may show more than one key on such a repository. The copies made by this program have one key.

## On Windows

The temporary file lives in the `tmp` folder of the data folder. Its rights are set with `icacls` before the password is written: inheritance is removed and only your account is granted access.
