# gcd: GitHub Cloud Disk

gcd syncs a local folder with a private GitHub repo, so the repo works as a
personal cloud drive across macOS, Linux and Windows. It has its own
three-way sync engine: when both sides changed a file, gcd keeps both copies
and never runs git merge. A GitHub Actions workflow in the repo compacts old
history so the repo does not keep growing.

gcd is built for personal documents and notes: up to about 2 GB per repo and
50 MB per file. For more data, split it across several repos (one Disk each)
or use object storage.

## Requirements

- **System git 2.30 or newer** on `PATH`. gcd calls `git` for all storage and
  transfer and refuses to run with an older version. Check with
  `git --version`.
- A **private** GitHub repo per Disk. gcd does not create repos and refuses
  public ones.
- A fine-grained personal access token for that repo with **Contents:
  read and write**, **Workflows: read and write** and **Actions: read and
  write** (Actions is used by `gcd compact`). `gcd init` prints a link to
  GitHub's token page with these permissions pre-filled; select the repo
  under *Repository access*.

## Tokens

`gcd init` asks for the token once and saves it in the OS keychain (macOS
Keychain, Windows Credential Manager, or the Linux Secret Service), one entry
per repo. `GCD_TOKEN`, when set, overrides the keychain for every repo, which
is useful in CI and on Linux machines without a Secret Service. When a token
expires or is revoked, every command says so; create a new one and run
`gcd login <owner/repo>` to replace the saved token.

gcd hands the token to git through git's credential prompt (`GIT_ASKPASS`),
so it never appears in process arguments, the remote URL, git config or
logs. gcd ignores your own git credential helpers for its repos.

## Install

### Download a release

Every release on the
[Releases page](https://github.com/Hanson-Tsai/gcd/releases) has an archive
for each platform plus `checksums.txt`:

| OS | amd64 | arm64 |
|---|---|---|
| macOS | `gcd_<version>_darwin_amd64.tar.gz` | `gcd_<version>_darwin_arm64.tar.gz` |
| Linux | `gcd_<version>_linux_amd64.tar.gz` | `gcd_<version>_linux_arm64.tar.gz` |
| Windows | `gcd_<version>_windows_amd64.zip` | `gcd_<version>_windows_arm64.zip` |

Verify the download against `checksums.txt`, unpack it, and put the `gcd`
binary (`gcd.exe` on Windows) on your `PATH`. For example, on Linux amd64:

```sh
VERSION=1.0.0   # without the leading "v"
base=https://github.com/Hanson-Tsai/gcd/releases/download/v$VERSION
curl -fsSLO "$base/gcd_${VERSION}_linux_amd64.tar.gz"
curl -fsSLO "$base/checksums.txt"
sha256sum --check --ignore-missing checksums.txt   # macOS: shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf "gcd_${VERSION}_linux_amd64.tar.gz" gcd
sudo install gcd /usr/local/bin/
```

On Windows, check the hash with
`Get-FileHash gcd_<version>_windows_amd64.zip -Algorithm SHA256` and compare
it with the line in `checksums.txt`.

On macOS, a binary downloaded with a browser may be quarantined. Run
`xattr -d com.apple.quarantine gcd` once, or download with `curl` as above.

### go install

With Go 1.24 or newer:

```sh
go install github.com/Hanson-Tsai/gcd/cmd/gcd@latest
```

The binary lands in `$(go env GOPATH)/bin`. Use `@v1.2.3` instead of
`@latest` to pin a release.

### Check the install

```sh
gcd version
```

prints the version, OS and architecture, for example
`gcd version 1.0.0 linux/amd64`.

## Usage

```sh
gcd init <owner/repo> <folder>   # bind a folder to a private repo (once per machine)
gcd login <owner/repo>           # replace the saved token (e.g. after it expired)
gcd sync                         # upload local changes, download remote ones
gcd status                       # show pending uploads, downloads and conflicts
gcd watch                        # stay in the foreground and sync automatically
gcd log [path]                   # history of the Disk or one file
gcd restore <path> --at <time>   # recover an old or deleted version
gcd compact                      # run history compaction now (GitHub Actions)
gcd config edit                  # edit the shared repo settings
gcd workflow upgrade             # update the compaction workflow to this gcd version
gcd version                      # print the gcd version
```

`gcd watch` watches every registered Disk. It syncs once local files have
been quiet for `debounce_seconds` (default 10) and checks the remote every
`poll_seconds` (default 60) with one cheap request, downloading only when it
changed. A sync that would delete many files is skipped with a warning; run
`gcd sync --yes` yourself if the deletions are intended. Ctrl+C waits for a
running sync to finish; press it again to quit at once.

Run `gcd` with no arguments to list the commands this build supports.
Inside a synced folder, gcd works out which Disk you mean; elsewhere, pass
`--disk <name>`.

Conflicts: when two machines change the same file, the remote version stays
at the original path and the local one is renamed
`name (conflict from <host> <date>).ext`. Both are synced.

Mass deletes: a sync that would delete many files (more than
`mass_delete_count`, default 50, or more than `mass_delete_percent`, default
20 %, of the files once over 5 files) stops before changing anything, for
example when an external drive is not mounted. Run `gcd sync --yes` if the
deletions are intended.

## What is not synced

gcd syncs regular files only, byte for byte (no line-ending conversion).
These are **not** synced:

- **Empty folders.** A folder exists on other machines only once it contains a
  file.
- **Symlinks.** They are skipped.
- **Executable bits and other permissions.** Every file is stored as a plain,
  non-executable file, and downloaded files get default permissions.
- Files larger than the size limit (50 MB by default). gcd warns and skips them.
- Names that would break on another OS: Windows-invalid characters
  (`: ? * < > |`) and reserved names (`CON`, `NUL`, ...), names ending in a
  space or a dot, and paths that differ only in case. gcd warns and skips them.
- System clutter such as `.DS_Store`, `Thumbs.db`, `desktop.ini`, Office lock
  files and editor swap files, plus anything matched by a `.gcdignore` file
  (same syntax as `.gitignore`).

## Releasing

Maintainers: push a `v*` tag (for example `git tag v1.2.3 && git push origin
v1.2.3`). `.github/workflows/release.yml` runs GoReleaser
(`.goreleaser.yaml`), which builds all six binaries with the version embedded
and publishes them with `checksums.txt` to GitHub Releases. To try the build
locally without publishing:

```sh
go run github.com/goreleaser/goreleaser/v2@v2.18.3 release --snapshot --clean
```

See `docs/manual-verification.md` for the release check that needs real
GitHub.
