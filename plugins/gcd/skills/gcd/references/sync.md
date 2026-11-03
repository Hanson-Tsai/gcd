# Sync

A sync changes every machine of the Disk: it pushes local changes, including
deletions, and writes remote changes into the folder. So you sync only when
the user asks for a sync (in words or with `/gcd:sync`), and you look before
you sync.

## Steps

1. Run `gcd status --json` (add `--disk <name>` outside the Disk folder).
2. Classify the result:
   - **Clean**: `uploaded` and `downloaded` hold only `added` and `modified`
     changes, `conflicts` is empty and `needs_confirm` is `false`. Go to step 4.
   - **Needs the user**: any change with `kind` `deleted` (on either side),
     any entry in `conflicts`, or `needs_confirm` is `true`. Go to step 3.
   - Nothing in `uploaded` or `downloaded`: tell the user the Disk is up to
     date and stop.
3. Show the user what the sync would do: the deleted paths (and on which side),
   each conflict as `path` → `copy`, and for `needs_confirm` the number
   `pending_deletes`. Ask whether to go ahead, and wait for the answer.
   - The user agrees: go to step 4. If `needs_confirm` was `true`, the user
     must have agreed to the deletions themselves; only then add `--yes`.
   - The user declines: stop. Nothing has changed.
4. Run `gcd sync --json` (with `--yes` only as allowed in step 3).
5. Report the result (below).

The folder can change between status and sync. If `gcd sync --json` fails with
`needs_confirm`, go back to step 3 with its `pending_deletes`; never retry with
`--yes` on your own.

## Reporting the result

From the `gcd sync --json` object:

- How many files went up (`uploaded`) and came down (`downloaded`); list them
  when there are only a few.
- Each conflict: the original `path` keeps the other machine's version, and
  the local version was saved as `copy`. Offer to show a diff of the two files
  so the user can merge them. Leave both files in place.
- `rejected`: files gcd does not sync, with the reason. Suggest the fix:

  | `reason` | Fix the user can make |
  |---|---|
  | `bad_char` | Rename: Windows does not allow `: ? * < > \| " \` in names. |
  | `reserved_name` | Rename: Windows reserves `CON`, `NUL`, `COM1` … even with an extension. |
  | `bad_ending` | Rename: the name ends with a space or a period. |
  | `case_collision` | Rename or merge: two paths differ only in letter case. |
  | `too_large` | Move it out of the Disk, or raise `max_file_mb` with `gcd config edit`. |
  | `not_regular` | Symlinks and special files do not sync. |

  `remote: true` means the path came from another git client and exists only
  on GitHub.
- `skipped`: files that were being written during the sync; the next sync
  picks them up.

## Mass deletes

gcd refuses a sync that deletes more files than the repo config allows
(`needs_confirm`), including a sync that would delete every synced file on one
side. The usual cause is an accident: an external drive that is not mounted, a
folder moved away, a tool that emptied the folder. Tell the user how many files
(`pending_deletes`) would go and that `--yes` deletes them on all machines, and
ask whether the folder is meant to be that empty. Run `gcd sync --yes --json`
only after the user confirms these deletions.

## Busy

`busy` means another gcd process is syncing the Disk, often a `gcd watch` the
user runs. Ask the user to wait for it or stop it, then start again at step 1.
