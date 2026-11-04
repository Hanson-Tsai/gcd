# Setup, tokens and admin commands

Commands in this file either handle the user's GitHub token or change the
repo's shared setup. The user runs most of them in their own terminal: you
explain and give the exact line.

## Install or upgrade

Propose one of these and run it only after the user approves. Releases are at
https://github.com/Hanson-Tsai/gcd/releases; the plugin needs `0.1.0-rc2` or
newer. Find the newest release (pre-releases included) with
`curl -fsSL https://api.github.com/repos/Hanson-Tsai/gcd/releases?per_page=1`
(`tag_name`, without the leading `v`).

- **Release download** (no Go needed), Linux and macOS. Pick `OS`
  (`linux`/`darwin`) and `ARCH` (`amd64`/`arm64`) from `uname -s` / `uname -m`:

  ```sh
  VERSION=0.1.0 OS=linux ARCH=amd64
  base=https://github.com/Hanson-Tsai/gcd/releases/download/v$VERSION
  curl -fsSLO "$base/gcd_${VERSION}_${OS}_${ARCH}.tar.gz"
  curl -fsSLO "$base/checksums.txt"
  sha256sum --check --ignore-missing checksums.txt   # macOS: shasum -a 256 --check --ignore-missing checksums.txt
  tar -xzf "gcd_${VERSION}_${OS}_${ARCH}.tar.gz" gcd
  install gcd ~/.local/bin/   # or: sudo install gcd /usr/local/bin/
  ```

  Stop if the checksum check fails. On Windows, the archive is
  `gcd_<version>_windows_<arch>.zip`; compare
  `Get-FileHash <zip> -Algorithm SHA256` with `checksums.txt`.
- **Go 1.24+**: `go install github.com/Hanson-Tsai/gcd/cmd/gcd@v0.1.0`
  (or `@latest`). The binary lands in `$(go env GOPATH)/bin`.

gcd also needs system git 2.30 or newer.

## init

`gcd init <owner/repo> <folder>` binds a folder to a private GitHub repo on
this machine. It asks for a token on the terminal, checks it, and saves it in
the OS keychain.

For a GitHub repo, the user runs it in their own terminal:

1. Check that gcd is installed and new enough (the main skill's version check).
2. Explain the token. It is a fine-grained personal access token for that one
   repo with:
   - **Contents: read and write** (sync)
   - **Workflows: read and write** (installs the history-compaction workflow)
   - **Actions: read and write** (`gcd compact` starts compaction on GitHub)

   `gcd init` prints a link that pre-fills these permissions; under
   *Repository access* the user selects the repo. The repo must exist and be
   private; gcd creates no repos.
3. Give the exact line, with the user's repo and folder, for example
   `gcd init alice/notes ~/Notes`. Add `--disk <name>` to name the Disk
   (default: the folder name).
4. When the user says it is done, run `gcd status --json` to confirm.

For a local test repo (`file://…`, a bare git repo) no token is involved: you
may run `gcd init file:///path/to/repo.git <folder>` yourself.

## login

`gcd login <owner/repo>` replaces the saved token, for example after an `auth`
error. The user runs it in their own terminal; give the line with the repo from
the error message. If `GCD_TOKEN` is set in the user's environment it overrides
the keychain; mention it when an `auth` error persists after `gcd login`.

## config edit

The repo config (`.gcd/config.toml`, shared by all machines) holds:

| Setting | Default | Meaning |
|---|---|---|
| `ignore` | `[]` | Extra ignore patterns (`.gitignore` syntax); `.gcdignore` files in the folder work too |
| `max_file_mb` | 50 | Files larger than this are not synced |
| `mass_delete_count` | 50 | A sync deleting more files needs `--yes` |
| `mass_delete_percent` | 20 | … or more than this share of the files (when over 5 files) |
| `debounce_seconds` | 10 | `gcd watch`: quiet time before syncing local changes |
| `poll_seconds` | 60 | `gcd watch`: how often it checks the remote |
| `retention_days` | 30 | History kept in full before compaction |
| `compact_schedule` | weekly | Cron expression (UTC) for the compaction workflow |

Explain settings freely. To change one, the user runs `gcd config edit` in
their own terminal: it opens an editor, validates the file and commits it.

## Admin commands

### compact

`gcd compact` rewrites the remote history (older than `retention_days`) into
one snapshot. Run it only when the user explicitly asks to compact now.

- Default: `gcd compact` starts the compaction workflow on GitHub Actions and
  prints the run's URL. Give the user that link.
- `gcd compact --local` compacts from this machine; use it only when the user
  asks for local compaction.
- `gcd compact --run` is the entry point inside the GitHub Actions workflow
  itself. Leave it to the workflow.

### workflow upgrade

`gcd status --json` reports `workflow_version`, the gcd release the repo's
compaction workflow runs. When it is not empty and older than the local
`gcd version`, tell the user and suggest `gcd workflow upgrade`. Run
`gcd workflow upgrade` only when the user asks; pass `--version x.y.z` only
with the version the user names.
