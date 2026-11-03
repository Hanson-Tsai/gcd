# gcd: GitHub Cloud Disk

gcd syncs a local folder with a private GitHub repo. You can use the repo as a
personal cloud disk on macOS, Linux, and Windows.

gcd has its own three-way sync engine. gcd does not use git merge. If two
machines change the same file, gcd keeps the two versions.

A GitHub Actions workflow compacts the old history of the repo. Thus, the size
of the repo does not increase without a limit.

gcd is for personal documents and notes. The limits are approximately 2 GB for
each repo and 50 MB for each file. If you have more data, put the data in more
than one repo, with one Disk for each repo. You can also use object storage.

## Requirements

- **System git 2.30 or higher** on your `PATH`. gcd uses `git` to keep and
  transfer all data. gcd does not operate with an older version of git. To see
  your version, type `git --version`.
- **A private GitHub repo** for each Disk. gcd does not make repos. gcd does
  not let you use a public repo.
- **A fine-grained personal access token** for that repo. The token must have
  these permissions:
  - **Contents: read and write**
  - **Workflows: read and write**
  - **Actions: read and write**. `gcd compact` must have this permission to
    start the compaction on GitHub. If the token does not have this
    permission, `gcd init` binds the Disk and shows a warning.

`gcd init` shows a link to the token page on GitHub. This link sets the
permissions for you. Under *Repository access*, select the repo.

## Tokens

`gcd init` asks for the token one time. gcd saves the token in the keychain of
the operating system:

- macOS: Keychain
- Windows: Credential Manager
- Linux: Secret Service

gcd saves one keychain entry for each repo.

If you set `GCD_TOKEN`, gcd uses this token for all repos and ignores the
keychain. This is useful in CI and on Linux machines that do not have a Secret
Service.

If a token expires or a person revokes it, all commands show an error. To
replace the token, do these steps:

1. Make a new token on GitHub.
2. Type `gcd login <owner/repo>`.

gcd gives the token to git through the credential prompt of git
(`GIT_ASKPASS`). Thus, the token does not show in process arguments, in the
remote URL, in the git configuration, or in logs. For its repos, gcd ignores
your git credential helpers.

## Install

### Download a release

Each release on the
[Releases page](https://github.com/Hanson-Tsai/gcd/releases) has one archive
for each platform and one `checksums.txt` file:

| OS | amd64 | arm64 |
|---|---|---|
| macOS | `gcd_<version>_darwin_amd64.tar.gz` | `gcd_<version>_darwin_arm64.tar.gz` |
| Linux | `gcd_<version>_linux_amd64.tar.gz` | `gcd_<version>_linux_arm64.tar.gz` |
| Windows | `gcd_<version>_windows_amd64.zip` | `gcd_<version>_windows_arm64.zip` |

To install a release, do these steps:

1. Download the archive and `checksums.txt`.
2. Compare the archive with `checksums.txt`.
3. Extract the archive.
4. Put the `gcd` binary (`gcd.exe` on Windows) in a folder on your `PATH`.

Example for Linux amd64:

```sh
VERSION=1.0.0   # without the "v" at the start
base=https://github.com/Hanson-Tsai/gcd/releases/download/v$VERSION
curl -fsSLO "$base/gcd_${VERSION}_linux_amd64.tar.gz"
curl -fsSLO "$base/checksums.txt"
sha256sum --check --ignore-missing checksums.txt   # macOS: shasum -a 256 --check --ignore-missing checksums.txt
tar -xzf "gcd_${VERSION}_linux_amd64.tar.gz" gcd
sudo install gcd /usr/local/bin/
```

On Windows, type
`Get-FileHash gcd_<version>_windows_amd64.zip -Algorithm SHA256`. Compare the
result with the applicable line in `checksums.txt`.

On macOS, a browser can put a downloaded binary in quarantine. To remove the
quarantine, type `xattr -d com.apple.quarantine gcd` one time. You can also
download the binary with `curl`, as in the example.

### go install

You must have Go 1.24 or higher. Type:

```sh
go install github.com/Hanson-Tsai/gcd/cmd/gcd@latest
```

Go puts the binary in `$(go env GOPATH)/bin`. To install one specified
release, use `@v1.2.3` instead of `@latest`.

### Check the installation

Type:

```sh
gcd version
```

The command shows the version, the operating system, and the architecture.
Example: `gcd version 1.0.0 linux/amd64`.

## Usage

```sh
gcd init <owner/repo> <folder>   # bind a folder to a private repo (one time on each machine)
gcd login <owner/repo>           # replace the saved token (for example, after it expires)
gcd sync                         # upload local changes and download remote changes
gcd status                       # show the uploads, downloads, and conflicts of the next sync
gcd watch                        # sync automatically in the foreground
gcd log [path]                   # show the history of the Disk or of one file
gcd restore <path> --at <time>   # restore an old version or a deleted file
gcd compact                      # start the history compaction now (GitHub Actions)
gcd config edit                  # edit the shared repo settings
gcd workflow upgrade             # update the compaction workflow to this gcd version
gcd version                      # show the gcd version
```

To see all the commands of this build, type `gcd` with no arguments.

In a synced folder, gcd finds the applicable Disk. In other folders, add
`--disk <name>`.

### gcd watch

`gcd watch` monitors all registered Disks:

- When the local files do not change for `debounce_seconds` (default: 10),
  gcd syncs.
- Each `poll_seconds` (default: 60), gcd sends one small request to the
  remote. gcd downloads only if the remote changed.
- If a sync will delete many files, gcd does not do the sync and shows a
  warning. If you want to delete the files, type `gcd sync --yes`.
- If you push Ctrl+C, gcd waits until the current sync is complete. To stop
  gcd immediately, push Ctrl+C again.

### Conflicts

If two machines change the same file, gcd keeps the two versions:

- The remote version stays at the original path.
- gcd gives the local version the name
  `name (conflict from <host> <date>).ext`.

gcd syncs the two files.

### Mass deletes

CAUTION: A folder can become empty by accident, for example when an external
drive is not mounted. Do not use `--yes` if you are not sure that you want to
delete the files. If you use `--yes`, gcd deletes the files on all machines.

gcd stops a sync before it changes data if the sync will delete:

- More than `mass_delete_count` files (default: 50).
- More than `mass_delete_percent` of the files (default: 20 %). gcd uses this
  limit only if the sync will delete more than 5 files.
- All the synced files on one side, also if there are only a small number of
  files. For example, this occurs when the folder is empty or an external
  drive is not mounted.

If you want to delete the files, type `gcd sync --yes`.

### Status

`gcd status` reads the latest data from the remote. Then it shows what the
next sync will do:

- The files that the sync will upload or download.
- The files that the sync will change into conflict copies.
- The files that the sync will not sync, and the reason.
- If the sync will need `--yes`.

`gcd status` does not change the folder, the remote, or the sync state. You
can use it while `gcd watch` operates.

## History compaction

`gcd init` installs `.github/workflows/gcd-compact.yml` in the repo. This
workflow does the history compaction:

- It operates one time each week. To change the schedule, use
  `gcd config edit` and change `compact_schedule`. The schedule is a cron
  expression in UTC.
- It keeps the full history of the last `retention_days` days (default: 30).
  It merges the older history into one snapshot.

To start the compaction immediately, type `gcd compact`. The command shows the
link to the run. To do the compaction on your machine, type
`gcd compact --local`.

The workflow uses one specified gcd release. Before the workflow runs gcd, it
does these steps:

1. It downloads `gcd_<version>_linux_amd64.tar.gz` of that release.
2. It compares the file with the SHA-256 value in the workflow. This value
   comes from the `checksums.txt` file of the release.
3. If the two values are the same, it runs gcd.

To set the workflow to the gcd version that you use, type
`gcd workflow upgrade`. To set a different release, add `--version x.y.z`.

NOTE: A development build of gcd does not have a release. Thus, `gcd init`
from a development build does not install the workflow. It shows a message.

The job pushes the changes with the Actions token of the repo. The Actions
token cannot have the Workflows permission. Thus, GitHub can stop the push if
the compacted history changes the workflow file. In this condition, the job
log shows the cause. Do these steps:

1. Make a fine-grained token for that repo with **Contents: read and write**
   and **Workflows: read and write**.
2. Add the token as the repository secret `GCD_TOKEN`
   (Settings > Secrets and variables > Actions).

If this secret is available, the workflow uses it.

## Items that gcd does not sync

gcd syncs only regular files. gcd copies each byte exactly and does not change
line endings.

gcd does not sync these items:

- **Empty folders.** A folder shows on other machines only when it contains a
  file.
- **Symlinks.** gcd skips them.
- **Executable bits and other permissions.** gcd keeps each file as a usual,
  non-executable file. Downloaded files have the default permissions.
- **Files that are larger than the size limit.** The limit is `max_file_mb` in
  the repo config (default: 50 MB). gcd shows a warning and skips these files.
- **Names that are not possible on a different operating system.** gcd shows
  a warning and skips these names on all operating systems:
  - Characters that Windows does not accept: `: ? * < > | " \`
  - Reserved names, for example `CON`, `NUL`, and `COM1`. This also applies to
    names with an extension, for example `nul.txt`.
  - Names that end with a space or a period.
  - Paths that are different only in letter case, for example `a.md` and
    `A.md`, or `Notes/` and `notes/`.
- **System files.** Examples are `.DS_Store`, `Thumbs.db`, `desktop.ini`,
  Office lock files (`~$*`), and editor swap and backup files (`*.swp`,
  `*~`). gcd skips these files and does not show a warning.
- **Files that agree with an ignore pattern.** The patterns are in the
  `ignore` list of the repo config (`gcd config edit`) and in `.gcdignore`
  files in the synced folder. A `.gcdignore` file uses the `.gitignore`
  syntax. You can put it in each subfolder. gcd also syncs the `.gcdignore`
  files. Thus, all machines use the same rules.

`gcd sync` shows each file that it skipped with a warning, and the reason. gcd
does not change a skipped file on the two sides:

- gcd does not upload the file.
- gcd does not delete the copy on GitHub.
- gcd does not write over the file.

This is also true for files with these names that a different git client
pushed to the repo. gcd does not download these files. gcd keeps them in the
repo.

NOTE: gcd does not sync a rename that changes only the letter case, for
example `notes.md` to `Notes.md`. Give the file a different name.

## Release procedure

This section is for maintainers. To make a release, push a `v*` tag, for
example:

```sh
git tag v1.2.3 && git push origin v1.2.3
```

Then `.github/workflows/release.yml` runs GoReleaser (`.goreleaser.yaml`).
GoReleaser builds the six binaries with the version in them. Then it
publishes the binaries and `checksums.txt` to GitHub Releases.

To test the build on your machine without a release, type:

```sh
go run github.com/goreleaser/goreleaser/v2@v2.18.3 release --snapshot --clean
```

For the release check on real GitHub, refer to `docs/manual-verification.md`.
