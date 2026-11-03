# Architecture

Orientation for implementers. The spec is issue #1; this page maps it onto the code.

## Package map

Everything except `main` lives under `internal/`. Modules follow the spec's module table.

| Package | Role | Status |
|---|---|---|
| `cmd/gcd` | `main`: wires signals and calls `cli.Run` | done |
| `internal/cli` | Subcommands, standard-library `flag` only. One file per command (`cmd_<name>.go`) that calls `register(...)` from `init()`; `cli.go` holds dispatch, the git version check and `openCurrentDisk` | `init`, `sync` |
| `internal/disk` | **Seam A.** `Open(Options)` / `Sync(ctx, SyncOptions) (Result, error)`. Orchestrates one sync, owns the local state file (`state.go`), names conflict copies | full three-way sync (#4) |
| `internal/scan` | Walk the folder and produce path → git blob hash plus size and mtime. Ignore rules, file-name policy and the large-file limit go here (#6) | regular files only |
| `internal/reconcile` | Pure three-way rules: `Reconcile(base, local, remote) []Action` | done (#4) |
| `internal/gitstore` | Wraps system git (≥ 2.30) on a bare git-dir: fetch, read tree, write blobs/tree/commit, push | done for #2 |
| `internal/apply` | Atomic writes to the folder (temp file + rename), local deletes, conflict copies (`CopyName`, rename local file aside) | basic + conflicts |
| `internal/registry` | Per-machine Disk list (`registry.json`) and "which Disk am I in" | minimal (#11) |
| `internal/fixture` | Seam A test fixture: `file://` bare remote + N devices | done |
| `internal/config`, `compact`, `auth`, `watch`, `workflow` | Not created yet; add them when their ticket lands (#3, #12, #13, #16, #15) | — |

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
scan.Scan(folder) ──────────────► local  {path → blob hash}
┌ round, retried up to 5× while Push fails with gitstore.ErrPushRejected
gitstore.Fetch → TreeOf → ReadTree ► remote {path → Entry}
gitstore.ReadTree(state.base_tree) ► base   {path → Entry}
                 │
reconcile.Reconcile(base, local, remote) → []Action
   Conflict               → disk names Copy ("<stem> (conflict from <host> <date>)<ext>")
   Upload / DeleteRemote  → WriteBlobs → WriteTree(remote ± changes)
   + Conflict                (local file uploaded under Copy)
                            → CommitTree(parent = remote head,
                                         author = host,
                                         "sync from <host>: +A ~M -D")
                            → Push (fast-forward only)
└ (rejected 5× → ErrRemoteChanging; folder and base untouched)
   Download / DeleteLocal → apply.Apply (temp file + rename)
   + Conflict                (rename local file to Copy, then download remote to Path)
                 │
state.base_tree = new tree (or the remote tree when nothing was uploaded)
```

Key invariants:

- **The base is a tree hash, not a commit**, so history compaction (#12) does not invalidate it.
- **No merge, rebase, pull, index or checkout.** gcd builds trees itself from a throwaway `GIT_INDEX_FILE` and `commit-tree`.
- **All content is binary.** It goes through `hash-object --no-filters` and `cat-file blob`, and `core.autocrlf=false` is set in the git-dir. Local hashes are computed in Go (SHA-1 of `blob <n>\0<data>`), so they compare directly with tree entries.
- **Three-way rules**: one-sided change wins; same content on both sides (incl. both deleted) is a no-op; edit beats delete; different edits → remote stays at the path, local is renamed to a conflict copy and uploaded. Copy names avoid every local/remote path case-insensitively and get ` 2`, ` 3`… inside the parentheses when taken. A sync interrupted after push but before apply loses nothing: the next sync reconciles it through the same rules (at worst leaving a duplicate copy).
- Commits are created only when there are local changes to upload. A download-only sync makes no commit.
- Only `100644` entries are written: no exec bit, no symlinks.

- **One Sync per Disk at a time.** The lock is an OS advisory lock (flock / LockFileEx), so the OS releases it when the holder dies; there is no stale-lock cleanup to do. A crash leaves only an unlocked file.
- **Everything that depends on the remote lives in `Disk.round`**, which is re-run from fetch on a rejected push. Nothing outside the git-dir may change inside a round; apply and the state write come after it.

Hooks later tickets plug into: the mass-delete check goes between reconcile and commit (#7). The mtime/size re-check goes before commit and before replace (#8). `Status` is the same pipeline without commit, push or apply (#9). Managed paths `.gcd/` and `.github/` are filtered from local/remote before reconcile but kept in the new tree (#3).

## Testing

- **Seam A**: `internal/disk` tests through `fixture.NewDevices(t, n)`. Each device has its own folder, state dir and injectable `Host`. Assertions use `Device.Files` / `AssertFiles`, `Remote.Files` / `AssertFiles` and `Remote.Log`.
- **Seam B** (compaction, #12) gets its own fixture for bare repos with chosen commit times.
- CLI: smoke tests only (`internal/cli`). They use `GCD_HOME` to simulate separate machines.
- Tests run offline and must pass on macOS, Linux and Windows (CI matrix in `.github/workflows/ci.yml`).
