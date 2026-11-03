# Architecture

Orientation for implementers. The spec is issue #1; this page maps it onto the code.

## Package map

Everything except `main` lives under `internal/`. Modules follow the spec's module table.

| Package | Role | Status |
|---|---|---|
| `cmd/gcd` | `main`: wires signals and calls `cli.Run` | done |
| `internal/cli` | Subcommands, standard-library `flag` only. One file per command (`cmd_<name>.go`) that calls `register(...)` from `init()`; `cli.go` holds dispatch, the git version check and `openCurrentDisk` | `init`, `login`, `sync`, `status`, `log`, `restore`, `compact`, `config edit`, `version` |
| `internal/disk` | **Seam A.** `Open(Options)` / `Sync(ctx, SyncOptions) (Result, error)` / `Status(ctx) (Result, error)`. Orchestrates one sync, owns the local state file (`state.go`), names conflict copies | full three-way sync (#4) |
| `internal/scan` | Walk the folder and produce path → git blob hash plus size and mtime, applying the file rules: ignore rules, file-name policy, size limit, regular files only (`Rules`, `Result.Rejected`). `Result.Exclude` applies them to remote/base paths; `Collisions` finds case collisions | done (#6) |
| `internal/reconcile` | Pure three-way rules: `Reconcile(base, local, remote) []Action` | done (#4) |
| `internal/gitstore` | Wraps system git (≥ 2.30) on a bare git-dir: fetch, read tree, write blobs/tree/commit, push | done for #2 |
| `internal/apply` | Atomic writes to the folder (temp file + rename, retried on Windows), local deletes, conflict copies (`CopyName`, rename local file aside), re-check before replace, stale temp cleanup (`RemoveTemp`) | done (#8) |
| `internal/registry` | Per-machine Disk list (`registry.json`) and "which Disk am I in": `--disk <name>`, else the Disk containing cwd (matched by file identity), else the only Disk | done |
| `internal/config` | Repo config `.gcd/config.toml` (TOML): `Parse` / `ParseStrict`, defaults, `Format` check (`ErrFormatTooNew`), `IsManaged` | done (#3) |
| `internal/fixture` | Seam A test fixture: `file://` bare remote + N devices; `Remote.Commit` plants files as another client. Seam B: `Remote.CommitAt(CommitSpec{Time, Host, Files, Delete})` plants commits with chosen times, `Remote.History` reads them back with parents, idents and dates | done |
| `internal/auth` | `ParseRepo` (owner/repo), `Tokens.Lookup` (`GCD_TOKEN`, then OS keychain via go-keyring, one entry per repo) / `Store`, `PromptToken`, `GitHub.Check` (REST + git smart-HTTP; base URLs injectable), `TokenHelp`. `auth/authtest`: fake GitHub (REST + `git http-backend`) and in-memory keychain | done (#13) |
| `internal/compact` | **Seam B.** `Run(ctx, Options{GitDir, Remote, Token, RetentionDays, Now}) (Result, error)`: history compaction, used by `gcd compact --local` and `--run` | done (#12) |
| `watch`, `workflow` | Not created yet; add them when their ticket lands (#16, #15) | — |

Dependency direction: `cli → disk, registry → scan, reconcile, gitstore, apply`. `reconcile` imports nothing from gcd. `fixture` imports `disk` and is used only from external `_test` packages, such as `package disk_test`.

## On-disk layout (per machine)

```
<gcd home>/                      $GCD_HOME, else the OS user-data dir + /gcd
  registry.json                  Disk list: name, folder, remote, state_dir
  disks/<name>/                  state dir (always outside the synced folder)
    state.json                   {"base_tree": "<tree hash>", "last_sync": ...}
    sync.lock                    OS lock held for the whole of a Sync; the file stays
    git/                         bare git-dir; remote "origin" = the Disk's URL
```

The synced folder holds only user files and never a `.git`. gcd syncs through the remote's `main` branch only. Its local view is `refs/remotes/origin/main`, moved by pushes and by the fetches of every other command (sync, init, config edit, log, restore, compact --local); `gcd status` fetches into `refs/gcd/peek/main` instead.

## Sync data flow (`disk.Sync`)

```
lock <state dir>/sync.lock (busy → ErrAlreadySyncing at once)
apply.RemoveTemp(folder)           (leftover .gcd-tmp-* from an interrupted sync)
Fetch → repo config + remote .gcdignore files ► scan.Rules
scan.Scan(folder, rules) ───────► local  {path → blob hash, size, mtime} + Rejected
┌ round, retried up to 5× while Push fails with gitstore.ErrPushRejected
gitstore.Fetch → TreeOf → ReadTree ► remote {path → Entry}
gitstore.ReadTree(state.base_tree) ► base   {path → Entry}
applyRules: drop excluded paths from all three sides ► Result.Rejected
                 │
reconcile.Reconcile(base, local, remote) → []Action
   Conflict               → disk names Copy ("<stem> (conflict from <host> <date>)<ext>")
   re-check uploads       → changed size/mtime, or blob ≠ scanned hash → Skipped
   Upload / DeleteRemote  → WriteBlobs → WriteTree(remote ± changes)
   + Conflict                (local file uploaded under Copy)
                            → CommitTree(parent = remote head,
                                         author = host,
                                         "sync from <host>: +A ~M -D")
                            → Push (fast-forward only)
└ (rejected 5× → ErrRemoteChanging; folder and base untouched)
   Download / DeleteLocal → apply.Apply (temp file + rename)
   + Conflict                (rename local file to Copy, then download remote to Path)
                             re-check before replace: local edited since scan →
                             Download moves it to a conflict copy first,
                             DeleteLocal keeps it (Skipped)
                 │
state.base_tree = new tree (or the remote tree when nothing was uploaded),
                  except paths skipped before commit and excluded paths keep
                  their old base entry
```

Key invariants:

- **The base is a tree hash, not a commit**, so history compaction (#12) does not invalidate it.
- **No merge, rebase, pull, index or checkout.** gcd builds trees itself from a throwaway `GIT_INDEX_FILE` and `commit-tree`.
- **All content is binary.** It goes through `hash-object --no-filters` and `cat-file blob`, and `core.autocrlf=false` is set in the git-dir. Local hashes are computed in Go (SHA-1 of `blob <n>\0<data>`), so they compare directly with tree entries.
- **Three-way rules**: one-sided change wins; same content on both sides (incl. both deleted) is a no-op; edit beats delete; different edits → remote stays at the path, local is renamed to a conflict copy and uploaded. Copy names avoid every local/remote path case-insensitively and get ` 2`, ` 3`… inside the parentheses when taken. A sync interrupted after push but before apply loses nothing: the next sync reconciles it through the same rules (at worst leaving a duplicate copy).
- Commits are created only when there are local changes to upload. A download-only sync makes no commit.
- Only `100644` entries are written: no exec bit, no symlinks.
- **Edits during a sync are never lost or half-uploaded (#8).** A file whose size or mtime moved since the scan, or whose blob no longer matches the scanned hash, sits out the round (`Result.Skipped`). Its old base entry is kept, so the next sync sees the same three-way situation. Downloads are written to `.gcd-tmp-*` in the target directory and renamed over the target only after a last re-check. Scan never lists `.gcd-tmp-*`, and each Sync deletes leftovers under the lock.
- **A path the file rules exclude is invisible on every side (#6).** See "File rules" below.
- **One Sync per Disk at a time.** The lock is an OS advisory lock (flock / LockFileEx), so the OS releases it when the holder dies; there is no stale-lock cleanup to do. A crash leaves only an unlocked file.
- **Everything that depends on the remote lives in `Disk.round`**, which is re-run from fetch on a rejected push. Nothing outside the git-dir may change inside a round; apply and the state write come after it.

A round is `Disk.plan` (fetch, repo config, file rules, reconcile, mass-delete count, conflict copy names, re-check of files about to be uploaded; read-only apart from the fetch) followed by the writing part (blobs, commit, push, new base tree). `Status` reuses `plan` (see below).

## Status: `gcd status` (#9)

- `Disk.Status(ctx) (Result, error)` runs the same `scanFolder` + `plan` as Sync and summarizes the plan into the same `Result` fields: `Uploaded`, `Downloaded`, `Conflicts` (with the copy name a sync would pick today), `Skipped` (files changing right now), `Rejected`, `FormatTooNew`, and `NeedsConfirm` / `PendingDeletes` when the next sync would need `--yes` (Status itself does not fail with `ErrMassDelete`). `Commit` and `BaseTree` stay empty.
- It changes neither the folder, the remote, the base (`state.json`) nor `refs/remotes/origin/main`. It skips `RemoveTemp`, `WriteBlobs`, commit, push and apply.
- **Fetch**: Status fetches with `gitstore.Store.Peek`, which downloads the objects but records the head only in the private ref `refs/gcd/peek/main` (`--refmap=` so git does not update remote-tracking refs opportunistically, `--no-write-fetch-head`). `refs/remotes/origin/main` is therefore exactly as `gcd status` found it, so an `ls-remote` comparison against it (`gcd watch`, #16) still notices an update that `gcd status` has already fetched. The peek ref is overwritten by the next Status and is never read by anything else.
- **No sync lock.** Status writes nothing a Sync reads, so it runs while `gcd watch` or `gcd sync` holds the lock and never makes them fail with `ErrAlreadySyncing`. Its output is a snapshot: during a running sync it may still list changes that sync is about to finish.

## Repo config and managed paths (#3)

- `.gcd/` and `.github/` (matched case-insensitively) are managed paths. `disk` drops them from base, local and remote before reconcile, so they are never written to the folder, never uploaded from it and never deleted for being absent. The commit keeps them because it starts from the remote tree.
- `Sync` fetches and parses `.gcd/config.toml` once before the scan, because the scan needs its ignore rules and size limit. It parses it again in every `Disk.round`, right after reading the fetched remote tree. A missing file means defaults. A newer `format` stops the sync before any change: `Result.FormatTooNew` plus a `*config.FormatError` that tells the user to upgrade. An invalid config also stops the sync. Mass delete (#7) reads the parsed `config.Config` in the round. Sync parses leniently and ignores unknown keys.
- `disk.RepoConfig` / `SetRepoConfig` / `EnsureRepoConfig` read and commit the config as a single-file commit on top of the remote head (fast-forward push, `ErrRemoteChanged` if the remote moved). `gcd init` calls `EnsureRepoConfig`. `gcd config edit` opens `$EDITOR` and commits only if `ParseStrict` accepts the file, which also rejects unknown keys.
- Bump `config.Format` only when the repo layout or meaning changes.

## File rules (#6)

The scan (`scan.Scan(folder, scan.Rules)`) applies these checks to each file and folder, in this order:

1. **Ignore rules** use `.gitignore` syntax and match case-sensitively. They come from the built-in list (`scan/ignore.go`: `.DS_Store`, `Thumbs.db`, `desktop.ini`, `~$*`, `*.swp`, `*~`, `.gcd-tmp-*`, ...), then the repo config's `ignore`, then `.gcdignore` files from the root down.
   - A deeper `.gcdignore` beats a shallower one. Within one source the last match wins, and `!` re-includes.
   - An ignored folder is not entered, so a file under an excluded folder cannot be re-included, as in git.
   - Ignored paths are skipped **silently**.
   - `.gcdignore` files are ordinary synced files. Where the folder has no `.gcdignore` but the remote does, the remote's copy is used (`Rules.IgnoreFiles`). A machine joining with existing files therefore does not upload what the shared rules ignore.
2. **File-name policy**: Windows' rules, enforced on every OS. A name must not contain `< > : " \ | ? *`, control characters or invalid UTF-8. It must not be a reserved device name (`CON`, `PRN`, `AUX`, `NUL`, `COM0-9`, `LPT0-9`, `CONIN$`, `CONOUT$`), also with an extension such as `nul.txt`. It must not end in a space or a dot. A bad folder name rejects the folder and everything in it.
3. **Regular files only**: symlinks and special files are rejected.
4. **Size limit**: a file over `max_file_mb` is rejected without being hashed.

`disk.applyRules` then works on all three sides (base, local, remote):

- A remote or base path is dropped when `scan.Result.Exclude` says so: it is ignored by the local rules, lies at or under a path the scan rejected, or has a bad name. Remote symlinks (mode `120000`, which only other git clients can create) are dropped too. Remote paths with bad names or symlink mode are reported with `Remote: true`.
- **Case collisions** are checked across local and remote paths together. Two paths collide when they are equal after lower-casing and NFC normalization, in the name or in any folder above it. Both are dropped and reported. Base paths are not part of the check, so a case-only rename collides with the old name and is not synced. The user has to rename the file to a different name.
- A dropped path is never uploaded, downloaded or deleted. A rejected local file is therefore not mistaken for a deletion, and a remote file another client pushed with a bad name never reaches the folder and stays on the remote.
- Dropped paths **keep their old base entry**, like skipped ones. When a rule stops applying (an ignore rule is removed, a collision is renamed away), the next sync sees each side's real changes since then. For example, a file deleted locally while ignored is downloaded again, not deleted remotely.
- `Result.Rejected` lists the scan's local rejections plus remote ones and collisions, sorted by path. `gcd sync` prints them as `not synced <path>[ (on the remote)]: <reason>`.

The size limit is checked only on local files, since it guards uploads. A remote file over the limit can only come from a non-gcd client or a lowered limit. It is downloaded once; after that its local copy is rejected, so the path stays frozen until the limit allows it again.

Exec bits are never synced: trees get only `100644` entries and downloads are written `0644`. Content is never converted (`hash-object --no-filters`), even when the folder holds a `.gitattributes`.

## Mass-delete guard (#7, `disk/massdelete.go`)

- Checked in `Disk.round` right after reconcile, before conflict naming, commit and push, so it re-runs on every retry. Deletions on both sides count together (`DeleteRemote` + `DeleteLocal`); the percentage is of the base (last synced) files, managed paths and paths excluded by the file rules left out.
- A sync needs confirmation when it deletes more than `mass_delete_count` files, or more than `mass_delete_percent` % of the base files **and** more than 5 files (`minPercentDeletes`, so removing a file or two from a small folder never asks).
- Without `SyncOptions.Yes` it stops with `ErrMassDelete`, `Result.NeedsConfirm` and `Result.PendingDeletes`; the remote and the folder are untouched. The CLI adds the `gcd sync --yes` hint; `gcd watch` (#29 story) should report it as a warning and not pass `Yes`.

## GitHub auth (#13)

- `gcd init owner/repo <folder>` resolves to `https://github.com/owner/repo.git`. A `file://` URL is still accepted and skips every GitHub step (tests, offline use). The registry stores only the URL; commands recover the repo with `auth.GitHub.RepoOf(remote)`, and `file://` remotes get no token.
- Token lookup: `GCD_TOKEN`, then the keychain (service `gcd`, account `github.com/<owner>/<repo>`). `init` prompts when neither has one and saves the typed token only after the checks and the first remote write succeed. `gcd login owner/repo` replaces it.
- `auth.GitHub.Check` before binding: `GET /repos/{o}/{r}` (401 → `ErrTokenInvalid`; 404 → anonymous retry, 200 → `ErrPublicRepo`, else `ErrRepoNotFound`; `private:false` → `ErrPublicRepo`). Then permission probes without side effects: Contents write = `GET {o}/{r}.git/info/refs?service=git-receive-pack` (what `git push` asks first); Actions write = dispatch a workflow file that never exists (403 = missing, 404/422 = present). Missing ones come back together in `*MissingPermissionsError`.
- Workflows write has no side-effect-free probe. A push touching `.github/workflows/` that GitHub refuses comes back as `gitstore.ErrWorkflowsPermission`, and the CLI names the permission. #15 must install the workflow during `init`, before the registry is saved, so this check happens there.
- **Credentials to git** (`gitstore/auth.go`): every git process gets `-c credential.helper=` (drops the user's osxkeychain/manager helpers, so they neither answer nor store) and `GIT_ASKPASS=<gcd executable>` plus `GCD_ASKPASS=1`, `GCD_ASKPASS_TOKEN=<token>` in the child env only. `main` (and any test binary that runs git with a token: see `TestMain` in `cli` and `gitstore`) calls `gitstore.ServeAskpass()` first. Username `x-access-token`. No `GIT_CONFIG_COUNT` (git 2.31).
- Git credential failures come back as `*gitstore.AuthError` (matches `gitstore.ErrAuth`) with the token scrubbed. `cli.Run` passes every error through `explain`, which adds `auth.TokenHelp` (pre-filled token URL, `gcd login`, `GCD_TOKEN`), so every command reports an expired token the same way.

## History: `gcd log` / `gcd restore` (`disk/history.go`)

- `Disk.Log(ctx, path)` fetches, then lists the remote's first-parent history newest first (`gitstore.History`, one `git log --name-status` call). Paths are matched literally (`GIT_LITERAL_PATHSPECS=1`); a directory path matches everything below it.
- `Disk.Restore(ctx, path, RestoreOptions{At, Force})` writes one file's version into the folder only. It does not touch the base, the remote or the state, so the next Sync uploads it as an ordinary local add or modify.
- `At` is a time (newest commit at or before it, by author time) or a commit hash. The zero `At` means the newest version that has the path, which for a deleted file is the version just before the deletion. `disk.ParseAt` reads the CLI's `--at`.
- The retention boundary is the oldest commit still in history. Once compaction (#12) runs, that commit is the snapshot root. Times before it return `ErrBeforeRetention`. A commit outside the history returns `ErrUnknownCommit`, and a path missing at that point returns `ErrPathNotFound`. A different local file returns `ErrExists` unless `Force` is set.

## History compaction: `gcd compact` (`internal/compact`)

- `compact.Run` fetches the remote into `Options.GitDir`, reads `retention_days` from `.gcd/config.toml` at the head (`Options.RetentionDays` > 0 overrides it), and refuses a newer or invalid repo format before changing anything.
- **Cut point**: the commit just before the oldest first-parent commit whose author time is at or after `Now - retention`. Everything from that commit on is kept, even a later commit with an older time (clock skew), so no commit dated inside the retention period is dropped. If the cut point is already the root, or there is none, Run does nothing (`Result.Compacted == false`). A second run is therefore a no-op.
- **Snapshot root**: the cut point's tree, author and committer lines (so `gcd restore` sees the cut point's author time as the retention boundary), no parent, message `snapshot of history up to <time>`.
- **Kept commits** are rebuilt from their raw objects (`gitstore.ReadCommits`): only the `parent` line changes; signatures (`gpgsig`, `mergetag`) are dropped. Author, committer, dates with their zone offsets, encoding and message stay byte for byte. Hashes are computed in Go (`gitstore.CommitHash`, SHA-1) to chain the commits, then written in one `hash-object -t commit --stdin-paths` call (`gitstore.WriteCommits`), which must return the same hashes. No `commit-tree` is involved. The rewrite depends only on the history, so compacting the same history twice gives the same commits.
- Run checks the invariant (new head tree == old head tree) and then pushes with `gitstore.ForcePush`: `--force-with-lease=refs/heads/main:<head at start>`. A push made meanwhile makes it fail with `compact.ErrLeaseFailed` (`Result.LeaseFailed`), and that push is kept.
- Only the first-parent chain survives. gcd never makes merges, so this matters only for repos edited with plain git.
- `gcd compact --local` resolves the Disk like other commands and works in its git-dir (`<state dir>/git`), so only new objects are fetched. It does not take the sync lock: a concurrent sync on the same machine either wins the race (the lease fails) or fetches the new history next round. `gcd compact --run [--repo <url>]` is the Actions entry point (#15). It works in a temporary git-dir and defaults the repo to `$GITHUB_SERVER_URL/$GITHUB_REPOSITORY.git`. Both modes pass the remote's token as `Options.Token`, and gitstore hands it to git through askpass (#13). The token comes from `remoteToken`: `GCD_TOKEN`, else the keychain, and none for non-GitHub URLs. In Actions the workflow sets `GCD_TOKEN` to `github.token`. `gcd compact` without a mode is left for #15 (workflow_dispatch) and currently prints usage.
- Devices need no special handling: their base is a tree hash, and `Fetch` uses a `+` refspec, so the rewritten history is picked up on the next sync. Old objects stay in each device's git-dir (`gc.auto=0`).

## Testing

- **Seam A**: `internal/disk` tests through `fixture.NewDevices(t, n)`. Each device has its own folder, state dir and injectable `Host`. Assertions use `Device.Files` / `AssertFiles`, `Remote.Files` / `AssertFiles` and `Remote.Log`.
- **Seam B**: `internal/compact` tests run `compact.Run` on `fixture.NewRemote` repos built with `Remote.CommitAt` (chosen commit times) and a fixed `Now`. `compact/sync_test.go` checks Seam A devices across a compaction.
- CLI: smoke tests only (`internal/cli`). They use `GCD_HOME` to simulate separate machines. GitHub flows use `cli.UseFakeGitHub(t, authtest.GitHub{...}.Start(t), keyring, stdin)` (in `export_test.go`); the fake serves real bare repos over smart HTTP, so `init owner/repo` and `sync` with a token run offline.
- Real-GitHub tests are opt-in: `TestE2E*` skip unless `GCD_E2E_REPO` and `GCD_TOKEN` are set; the **E2E** workflow runs them on manual dispatch only. See `docs/manual-verification.md`.
- Tests run offline and must pass on macOS, Linux and Windows (CI matrix in `.github/workflows/ci.yml`).
