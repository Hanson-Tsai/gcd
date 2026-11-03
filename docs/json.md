# JSON output

These gcd commands print JSON when you add `--json`:

- `gcd status --json`
- `gcd sync --json`
- `gcd log --json`
- `gcd restore --json`
- `gcd version --json`
- `gcd watch --json` (one JSON object for each event)

The JSON output is for programs and agents, for example the Claude Code
plugin in `plugins/gcd`. For people, the text output stays the same.

## Compatibility rule

IMPORTANT: Changes to the JSON output only add fields and values. A field is
never renamed or removed, and its type never changes. Thus, an old plugin
works with a newer gcd.

A program that reads this output must:

- Ignore fields that it does not know.
- Read an error `kind` that it does not know as `other`.
- Read a watch `event` that it does not know as an event that it can ignore.

The first gcd version with `--json` is `0.1.0-rc2`.

## General rules

- With `--json`, stdout has only one JSON object and a newline. gcd writes
  nothing to stderr. `gcd watch --json` writes one object on each line (JSON
  Lines).
- Field names use `snake_case`.
- Times use RFC 3339 in UTC, for example `2026-11-03T14:02:24Z`.
- Paths are relative to the Disk folder and use `/`, for example
  `notes/todo.md`. This is the same as in the text output of `gcd status`.
- A list is always an array. An empty list is `[]`, not `null`.
- The kind of a change is one of `added`, `modified`, or `deleted`.
- Usage errors (exit code 2, for example an unknown flag) do not use JSON. gcd
  writes the usage text to stderr.

## Errors

If a command fails, the exit code is 1, as without `--json`. stdout has one
error object:

```json
{"error": {"kind": "auth", "message": "..."}}
```

| Field | Type | Description |
|---|---|---|
| `error.kind` | string | The type of error. Refer to the table below. |
| `error.message` | string | The error message that gcd shows without `--json`. It can have more than one line. |
| `error.pending_deletes` | number | Only for `needs_confirm`: the number of files that the sync will delete. |

| `kind` | Cause | What to do |
|---|---|---|
| `needs_confirm` | The sync will delete more files than the repo config allows. gcd changed nothing. | Show the deletions to the user. Only if the user agrees, run `gcd sync --yes`. |
| `auth` | There is no token, or GitHub refused it: it expired, someone revoked it, or it does not have the necessary permissions. | The user runs `gcd login <owner/repo>` in a terminal. |
| `busy` | A different gcd process syncs this Disk now (`gcd sync`, `gcd watch`, or `gcd restore`). | Wait, or stop the other gcd. Then try again. |
| `no_disk` | The current folder is not in a Disk and there is more than one Disk, no Disk is registered, or no Disk has the name in `--disk`. The message lists the Disks. | Use `--disk <name>`, or run `gcd init`. |
| `format_too_new` | The repo format is newer than this gcd supports. gcd changed nothing. | Upgrade gcd. |
| `exists` | `gcd restore`: a different file is at the path in the folder. gcd changed nothing. | Ask the user. Only if the user agrees, run the command again with `--force`. |
| `other` | All other errors. | Read `message`. |

## gcd status --json

`gcd status --json` shows what the next sync will do. It does not change the
folder, the remote, or the sync state.

```json
{
  "disk": "notes",
  "uploaded": [{"path": "todo.md", "kind": "modified"}],
  "downloaded": [{"path": "report.md", "kind": "added"}],
  "conflicts": [{"path": "plan.md", "copy": "plan (conflict from laptop 2026-11-03).md"}],
  "skipped": [],
  "rejected": [{"path": "a:b.txt", "reason": "bad_char", "detail": "name contains ':', which Windows does not allow", "remote": true}],
  "needs_confirm": false,
  "pending_deletes": 0,
  "workflow_version": "0.1.0-rc2"
}
```

(The example has more than one line. gcd prints the object on one line.)

| Field | Type | Description |
|---|---|---|
| `disk` | string | The name of the Disk. |
| `uploaded` | array of change | The local changes that the sync will upload: `{path, kind}`. |
| `downloaded` | array of change | The remote changes that the sync will write to the folder: `{path, kind}`. |
| `conflicts` | array of conflict | The files that changed on the two sides: `{path, copy}`. The remote version stays at `path`. The local version becomes the conflict copy `copy`. Each conflict also shows as an `added` upload of `copy` and a `modified` download of `path`. |
| `skipped` | array of string | The files that changed during the scan, for example because an editor writes them. The sync does not include them. A later sync syncs them. |
| `rejected` | array of rejection | The files that gcd does not sync. Refer to [Rejections](#rejections). |
| `needs_confirm` | boolean | `true` if the sync will stop because it deletes too many files. Then `gcd sync` fails with `needs_confirm`. |
| `pending_deletes` | number | The number of files that the sync will delete, on the two sides. It is `0` if `needs_confirm` is `false`. |
| `workflow_version` | string | The gcd version that the compaction workflow in the repo uses. It is `""` if the repo has no workflow, or if gcd cannot read the workflow. |

### Rejections

| Field | Type | Description |
|---|---|---|
| `path` | string | The file or folder. |
| `reason` | string | `bad_char`, `reserved_name`, `bad_ending`, `case_collision`, `too_large`, or `not_regular`. |
| `detail` | string | An explanation for people. |
| `remote` | boolean | `true` if the path is only on the remote (a different git client pushed it). |

| `reason` | Cause |
|---|---|
| `bad_char` | The name has a character that Windows does not accept: `: ? * < > \| " \` or a control character. |
| `reserved_name` | The name is reserved on Windows, for example `CON` or `nul.txt`. |
| `bad_ending` | The name ends with a space or a period. |
| `case_collision` | Two paths are different only in letter case or in Unicode normalization. |
| `too_large` | The file is larger than `max_file_mb` in the repo config. |
| `not_regular` | The path is a symlink or a different special file. |

Ignored files (ignore patterns, `.gcdignore`, and system files) are not in
`rejected`.

## gcd sync --json

`gcd sync --json` has the same fields as `gcd status --json`, but not
`workflow_version`. It also has:

| Field | Type | Description |
|---|---|---|
| `commit` | string | The commit that this sync pushed. It is `""` if the sync uploaded nothing. |

The lists show what the sync did. `needs_confirm` is always `false` in a
successful sync: if the sync deletes too many files, it fails with the error
kind `needs_confirm` and `pending_deletes`.

## gcd log --json

```json
{
  "disk": "notes",
  "path": "todo.md",
  "entries": [
    {
      "commit": "4f9c2e1d0a...",
      "time": "2026-11-03T14:02:24Z",
      "host": "laptop",
      "changes": [{"path": "todo.md", "kind": "modified"}]
    }
  ]
}
```

| Field | Type | Description |
|---|---|---|
| `disk` | string | The name of the Disk. |
| `path` | string | The path that you gave, relative to the Disk folder. It is `""` if you gave no path. |
| `entries` | array | The syncs, newest first. |
| `entries[].commit` | string | The full commit hash. You can use it with `gcd restore --at`. |
| `entries[].time` | string | The time of the sync. |
| `entries[].host` | string | The machine that did the sync. |
| `entries[].changes` | array of change | The changed files: `{path, kind}`. If you gave a path, only the files at or below that path. |

## gcd restore --json

```json
{"path": "todo.md", "commit": "4f9c2e1d0a...", "time": "2026-11-03T14:02:24Z", "unchanged": false}
```

| Field | Type | Description |
|---|---|---|
| `path` | string | The restored file, relative to the Disk folder. |
| `commit` | string | The commit that gcd took the version from. |
| `time` | string | The time of that commit. |
| `unchanged` | boolean | `true` if the file in the folder was already that version. Then gcd wrote nothing. |

`gcd restore` changes only the folder. The next sync uploads the restored
file.

## gcd version --json

```json
{"version": "0.1.0-rc2", "os": "linux", "arch": "amd64", "dev": false}
```

| Field | Type | Description |
|---|---|---|
| `version` | string | The version, without `v`. A development build shows `dev` or a Go pseudo-version. |
| `os` | string | The operating system (`GOOS`). |
| `arch` | string | The architecture (`GOARCH`). |
| `dev` | boolean | `true` for a development build, which has no release. A version check must accept a development build. |

## gcd watch --json

`gcd watch --json` writes one JSON object on each line, one for each event.
All events have these fields:

| Field | Type | Description |
|---|---|---|
| `time` | string | The time of the event. |
| `disk` | string | The name of the Disk. |
| `event` | string | The type of event. Refer to the table below. |

| `event` | More fields | Description |
|---|---|---|
| `watching` | `folder` | gcd started to watch the Disk. The first sync comes next. |
| `synced` | the fields of `gcd sync --json` | A sync is complete. Look for `conflicts`. |
| `needs_confirm` | `pending_deletes`, `message` | gcd did not do a sync because it would delete too many files. gcd does not try again until the folder or the remote changes. |
| `busy` | `message` | A different gcd syncs this Disk. gcd tries again soon. gcd sends this event one time. |
| `error` | `kind`, `message` | A sync or a remote check failed. `kind` has the values of the error kinds. gcd tries again at each poll interval and sends the same error one time. |
| `warning` | `message` | gcd cannot watch all the files, for example because of the file watch limit of the OS. A rescan at each poll interval finds the changes. |
| `until_reached` | `path`, `kind` | Only with `--until`. A sync downloaded `path` (`kind` is `added`, `modified`, or `deleted`). This is the last line. gcd stops with exit code 0. |

### Options

- `--disk <name>`: watch only this Disk. Without `--disk`, gcd watches all
  the Disks.
- `--until <path>`: stop after a sync downloads this path. The path is
  relative to the current folder, as in `gcd log` and `gcd restore`. If the
  path is a folder, a download of a file in the folder also counts.
  - An upload of the path does not count. Only a change from a different
    machine counts.
  - The first sync after the start also counts.
  - gcd watches only one Disk: the Disk of the current folder, or the Disk
    in `--disk`. If there is no such Disk, it is a usage error (exit code 2).
  - Without `--json`, gcd writes one line before it stops.

Ctrl+C operates as without `--json`: gcd waits until the current sync is
complete. To stop gcd immediately, push Ctrl+C again.
