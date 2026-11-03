# History and restore

gcd keeps every sync as a commit. `gcd log` lists them; `gcd restore` writes an
old version of one file back into the folder. Both are safe to run without
asking: `log` reads only, and `restore` refuses to overwrite a different file
unless you pass `--force`.

History older than the repo's `retention_days` (default 30) has been compacted
into one snapshot and is gone.

## Log

`gcd log --json [path]` (add `--disk <name>` outside the Disk folder):

- `entries` is newest first. Each entry has `commit`, `time` (UTC), `host` (the
  machine that synced) and `changes` (`{path, kind}`).
- With a path, only the syncs that touched that file or folder are listed.

Show times in the user's local time zone when you know it, and name the
machine (`host`) for each version.

## Restore

1. Find the version. For "the version from yesterday", "before I deleted it"
   and the like, run `gcd log --json <path>` and pick the entry. Without
   `--at`, `restore` takes the newest version that has the file, which for a
   deleted file is the version just before the deletion.
2. Run `gcd restore <path> --json`, adding `--at <commit>` for a specific
   entry (a commit hash from the log is exact; a time like
   `"2026-11-03 14:00"` or `2026-11-03` also works and is read in local time).
3. Read the result:
   - `unchanged: true`: the folder already had that version. Say so.
   - Otherwise the file was written. Tell the user the restored file is only
     local: the next `gcd sync` uploads it to the other machines.
4. On the error `exists`, a different file is at that path. Tell the user that
   restoring overwrites the current local file, and ask. Run the command again
   with `--force` only after the user agrees. (A copy of the current file
   first is a good offer.)

Other errors name the cause in `message`: a time older than the retention
period, a commit not in this Disk's history, a path that did not exist at that
point, or a folder (restore its files one at a time).
