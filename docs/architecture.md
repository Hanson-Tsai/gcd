# Architecture

Orientation for implementers. The spec is issue #1; this page maps it onto the code.

## Package map

Everything except `main` lives under `internal/`. Modules follow the spec's module table.

| Package | Role | Status |
|---|---|---|
| `cmd/gcd` | `main`: wires signals and calls `cli.Run` | done |
| `internal/cli` | Subcommands, standard-library `flag` only. One file per command (`cmd_<name>.go`) that calls `register(...)` from `init()`; `cli.go` holds dispatch, the git version check and `openCurrentDisk` | `init`, `login`, `sync`, `config edit` |
| `internal/disk` | **Seam A.** `Open(Options)` / `Sync(ctx, SyncOptions) (Result, error)`. Orchestrates one sync, owns the local state file (`state.go`), names conflict copies | full three-way sync (#4) |
| `internal/scan` | Walk the folder and produce path → git blob hash plus size and mtime. Ignore rules, file-name policy and the large-file limit go here (#6) | regular files only |
| `internal/reconcile` | Pure three-way rules: `Reconcile(base, local, remote) []Action` | done (#4) |
| `internal/gitstore` | Wraps system git (≥ 2.30) on a bare git-dir: fetch, read tree, write blobs/tree/commit, push | done for #2 |
| `internal/apply` | Atomic writes to the folder (temp file + rename, retried on Windows), local deletes, conflict copies (`CopyName`, rename local file aside), re-check before replace, stale temp cleanup (`RemoveTemp`) | done (#8) |
| `internal/registry` | Per-machine Disk list (`registry.json`) and "which Disk am I in": `--disk <name>`, else the Disk containing cwd (matched by file identity), else the only Disk | done |
| `internal/config` | Repo config `.gcd/config.toml` (TOML): `Parse` / `ParseStrict`, defaults, `Format` check (`ErrFormatTooNew`), `IsManaged` | done (#3) |
| `internal/fixture` | Seam A test fixture: `file://` bare remote + N devices; `Remote.Commit` plants files as another client | done |
| `internal/auth` | `ParseRepo` (owner/repo), `Tokens.Lookup` (`GCD_TOKEN`, then OS keychain via go-keyring, one entry per repo) / `Store`, `PromptToken`, `GitHub.Check` (REST + git smart-HTTP; base URLs injectable), `TokenHelp`. `auth/authtest`: fake GitHub (REST + `git http-backend`) and in-memory keychain | done (#13) |
| `internal/compact`, `watch`, `workflow` | Not created yet; add them when their ticket lands (#12, #16, #15) | — |

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

The synced folder holds only user files and never a `.git`. gcd syncs through the remote's `main` branch only. Its local view is `refs/remotes/origin/main`.

## Sync data flow (`disk.Sync`)

```
lock <state dir>/sync.lock (busy → ErrAlreadySyncing at once)
apply.RemoveTemp(folder)           (leftover .gcd-tmp-* from an interrupted sync)
scan.Scan(folder) ──────────────► local  {path → blob hash, size, mtime}
┌ round, retried up to 5× while Push fails with gitstore.ErrPushRejected
gitstore.Fetch → TreeOf → ReadTree ► remote {path → Entry}
gitstore.ReadTree(state.base_tree) ► base   {path → Entry}
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
                  except paths skipped before commit keep their old base entry
```

Key invariants:

- **The base is a tree hash, not a commit**, so history compaction (#12) does not invalidate it.
- **No merge, rebase, pull, index or checkout.** gcd builds trees itself from a throwaway `GIT_INDEX_FILE` and `commit-tree`.
- **All content is binary.** It goes through `hash-object --no-filters` and `cat-file blob`, and `core.autocrlf=false` is set in the git-dir. Local hashes are computed in Go (SHA-1 of `blob <n>\0<data>`), so they compare directly with tree entries.
- **Three-way rules**: one-sided change wins; same content on both sides (incl. both deleted) is a no-op; edit beats delete; different edits → remote stays at the path, local is renamed to a conflict copy and uploaded. Copy names avoid every local/remote path case-insensitively and get ` 2`, ` 3`… inside the parentheses when taken. A sync interrupted after push but before apply loses nothing: the next sync reconciles it through the same rules (at worst leaving a duplicate copy).
- Commits are created only when there are local changes to upload. A download-only sync makes no commit.
- Only `100644` entries are written: no exec bit, no symlinks.
- **Edits during a sync are never lost or half-uploaded (#8).** A file whose size or mtime moved since the scan, or whose blob no longer matches the scanned hash, sits out the round (`Result.Skipped`). Its old base entry is kept, so the next sync sees the same three-way situation. Downloads are written to `.gcd-tmp-*` in the target directory and renamed over the target only after a last re-check. Scan never lists `.gcd-tmp-*`, and each Sync deletes leftovers under the lock.
- **One Sync per Disk at a time.** The lock is an OS advisory lock (flock / LockFileEx), so the OS releases it when the holder dies; there is no stale-lock cleanup to do. A crash leaves only an unlocked file.
- **Everything that depends on the remote lives in `Disk.round`**, which is re-run from fetch on a rejected push. Nothing outside the git-dir may change inside a round; apply and the state write come after it.

Hooks later tickets plug into: the mass-delete check goes between reconcile and commit (#7). `Status` is the same pipeline without commit, push or apply (#9).

## Repo config and managed paths (#3)

- `.gcd/` and `.github/` (matched case-insensitively) are managed paths. `disk` drops them from base, local and remote before reconcile, so they are never written to the folder, never uploaded from it and never deleted for being absent. The commit keeps them because it starts from the remote tree.
- `Sync` parses `.gcd/config.toml` in `Disk.round`, right after reading the fetched remote tree. A missing file means defaults. A newer `format` stops the sync before any change: `Result.FormatTooNew` plus a `*config.FormatError` that tells the user to upgrade. An invalid config also stops the sync. Later tickets read the parsed `config.Config` there (ignore rules and file limit #6, mass delete #7). Sync parses leniently and ignores unknown keys.
- `disk.RepoConfig` / `SetRepoConfig` / `EnsureRepoConfig` read and commit the config as a single-file commit on top of the remote head (fast-forward push, `ErrRemoteChanged` if the remote moved). `gcd init` calls `EnsureRepoConfig`. `gcd config edit` opens `$EDITOR` and commits only if `ParseStrict` accepts the file, which also rejects unknown keys.
- Bump `config.Format` only when the repo layout or meaning changes.

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

## Testing

- **Seam A**: `internal/disk` tests through `fixture.NewDevices(t, n)`. Each device has its own folder, state dir and injectable `Host`. Assertions use `Device.Files` / `AssertFiles`, `Remote.Files` / `AssertFiles` and `Remote.Log`.
- **Seam B** (compaction, #12) gets its own fixture for bare repos with chosen commit times.
- CLI: smoke tests only (`internal/cli`). They use `GCD_HOME` to simulate separate machines. GitHub flows use `cli.UseFakeGitHub(t, authtest.GitHub{...}.Start(t), keyring, stdin)` (in `export_test.go`); the fake serves real bare repos over smart HTTP, so `init owner/repo` and `sync` with a token run offline.
- Real-GitHub tests are opt-in: `TestE2E*` skip unless `GCD_E2E_REPO` and `GCD_TOKEN` are set; the **E2E** workflow runs them on manual dispatch only. See `docs/manual-verification.md`.
- Tests run offline and must pass on macOS, Linux and Windows (CI matrix in `.github/workflows/ci.yml`).
