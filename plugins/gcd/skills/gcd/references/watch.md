# Watch

`gcd watch` syncs a Disk continuously: after local files settle and whenever
the remote moves. Use it in two cases:

- **Waiting for a file**: the user (or the task) needs a file that another
  machine or another agent will upload. This is the common case.
- **Open-ended watching**: the user explicitly asks to keep the Disk in sync
  for a while.

Each watch syncs, so it uploads local changes and downloads remote ones like
`gcd sync`. It never deletes in bulk: a mass delete stops with a
`needs_confirm` event instead.

## Waiting for a file

1. Find the Disk name (`disk` in `gcd status --json`, or from the user).
2. Start, as a background command:

   ```sh
   gcd watch --json --disk <name> --until <path>
   ```

   `<path>` is relative to the current directory, or to the Disk folder when
   you run it from elsewhere. A folder path waits for any file below it.
3. The command exits 0 by itself once a sync downloads that path from another
   machine (added, modified or deleted). Your own upload of the path does not
   count. The last line is the `until_reached` event with `path` and `kind`.
4. Continue the task with the downloaded file.

Watch the output while it runs and report these events to the user at once,
without waiting for `until_reached`:

| `event` | Report |
|---|---|
| `synced` with a non-empty `conflicts` | Each `path` → `copy`; the user merges them (see the main skill). |
| `needs_confirm` | gcd refused a sync that deletes `pending_deletes` files. Ask the user to check the folder; the deletion needs the user's `gcd sync --yes` (see [sync.md](sync.md#mass-deletes)). |
| `error` | `message`; act on `kind` as in the main skill's error table. `auth` will not fix itself: stop the watch. |
| `busy` | Another gcd syncs this Disk; watch retries by itself. |

If the user gives up waiting, or the wait clearly will not end (the other
machine is offline, the file name is wrong), stop the background command and
say so.

## Open-ended watching

Run `gcd watch --json --disk <name>` in the background only when the user asks
for it, and tell the user it keeps running and syncing until stopped. Stop it
when the user asks or when the work it was for is done. Report the events
above as they come.

## Starting errors

A start that fails prints one error object (`no_disk`, `auth`, …) and exits 1.
`--until` without one Disk (no `--disk`, and the current directory is not in a
Disk) is a usage error, exit 2: add `--disk <name>`.
