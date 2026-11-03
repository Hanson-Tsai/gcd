---
name: gcd
description: Operate gcd (GitHub Cloud Disk), which syncs a folder with a private GitHub repo. Use when the user mentions gcd, a cloud disk (雲端硬碟), a synced folder or a Disk; or when the working directory is inside a Disk and the user wants to sync, see pending changes, look up file history, restore an old or deleted file, or wait for a file from another machine.
---

# gcd

gcd syncs a local folder (a **Disk**) with a private GitHub repo. Each machine
runs gcd against the same repo, so the Disk works like a cloud drive. A sync
uploads local changes, downloads remote ones, and keeps both versions of a file
changed on two machines (a **conflict copy**).

Reply in the user's language. These instructions are in English; the
conversation stays in whatever language the user writes.

## Before the first gcd command

Run `gcd version --json` once per conversation.

- `dev` is `true`: a development build. Accept it.
- `version` is `0.1.0-rc2` or newer (`0.1.0-rc2` < `0.1.0` < `0.1.1` …): accept it.
- `version` is older (for example `0.1.0-rc1`), or the command fails with a
  usage error (gcd before `0.1.0-rc2` has no `--json`): tell the user this
  plugin needs gcd `0.1.0-rc2` or newer, and how to upgrade (see
  [references/setup.md](references/setup.md#install-or-upgrade)).
- `gcd` is not found: propose an install command from
  [references/setup.md](references/setup.md#install-or-upgrade) and run it
  only after the user approves it.

## How to run gcd

Add `--json` to every `status`, `sync`, `log`, `restore`, `version` and `watch`
command, and decide from the JSON, never from text output. The fields are in
`docs/json.md` of the gcd repo; this skill names the ones that matter. Treat an
unknown field as absent and an unknown error `kind` as `other`.

Outside a Disk folder, pass `--disk <name>`. Paths in the JSON are relative to
the Disk folder.

## Which command, and how much freedom

| Command | What you do |
|---|---|
| `status` | Run it freely. Summarize: uploads, downloads, conflicts, `rejected` files with the reason, `skipped` files. |
| `sync` | Only when the user asks. Status first; stop and ask before deletes, conflicts or `needs_confirm`. → [references/sync.md](references/sync.md) |
| `watch` | Only when the user asks, or the task must wait for a file. Always `--json --disk <name>`, plus `--until <path>` to wait for a file; runs in the background. → [references/watch.md](references/watch.md) |
| `log` | Run it freely. → [references/history.md](references/history.md) |
| `restore` | Run it freely; ask before `--force`. → [references/history.md](references/history.md) |
| `version` | Run it freely (the version check above). |
| `init`, `login`, `config edit` | The user runs these in their own terminal; you give the exact line. `init` with a `file://` URL you may run. → [references/setup.md](references/setup.md) |
| `compact`, `workflow upgrade` | Only when the user asks for exactly that. → [references/setup.md](references/setup.md#admin-commands) |

## Rules that hold everywhere

- **Tokens stay out of the conversation.** gcd asks for the GitHub token in
  the user's own terminal (`gcd init`, `gcd login`) and keeps it in the OS
  keychain. Never ask the user to paste a token into the chat, and never put a
  token in a command argument or environment variable you set.
- **Sync on request.** Editing files inside a Disk is ordinary work; sync only
  when the user asks for a sync (or `/gcd:sync`).
- **Conflict copies belong to the user.** Report each `conflicts[]` entry as
  the original `path` and its `copy`. Offer to show the differences. Leave both
  files as they are until the user decides how to merge; the user deletes the
  copy.
- **The repo's `.gcd/` and `.github/` are gcd's.** Change them only through
  gcd commands (`gcd config edit`, `gcd workflow upgrade`), which check what
  they write.
- **`--yes` is the user's decision.** It deletes files on every machine. Add
  it only after the user has seen the deletions and agreed to them.

## Errors

A failed `--json` command prints `{"error": {"kind", "message"}}` and exits 1.
Act on `kind`:

| `kind` | What you do |
|---|---|
| `needs_confirm` | A sync would delete `pending_deletes` files. Follow [references/sync.md](references/sync.md#mass-deletes). |
| `auth` | The token is missing, expired, revoked or lacks a permission. Tell the user to run `gcd login <owner/repo>` in their own terminal (the repo is in `message`). |
| `busy` | Another gcd (a `gcd watch`, another sync) holds this Disk. Ask the user to wait or stop the other gcd, then retry. |
| `no_disk` | No Disk here. `message` lists the Disks: pick the one the user means with `--disk <name>`, or ask which. With no Disk at all, see [references/setup.md](references/setup.md). |
| `format_too_new` | The repo was written by a newer gcd. Ask the user to upgrade gcd; nothing was changed. |
| `exists` | `restore` found a different file in the way. See [references/history.md](references/history.md#restore). |
| `other` | Show `message` to the user. |
