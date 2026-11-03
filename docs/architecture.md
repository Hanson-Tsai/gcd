# Architecture

Orientation for implementers. The spec is issue #1; this page maps it onto the code.

## Package map

Everything except `main` lives under `internal/`. Modules follow the spec's module table.

| Package | Role | Status |
|---|---|---|
| `cmd/gcd` | `main`: wires signals and calls `cli.Run` | done |
| `internal/cli` | Subcommands, standard-library `flag` only. One file per command (`cmd_<name>.go`) that calls `register(...)` from `init()`; `cli.go` holds dispatch, the git version check and `openCurrentDisk` | `init`, `sync`, `config edit` |
| `internal/disk` | **Seam A.** `Open(Options)` / `Sync(ctx, SyncOptions) (Result, error)`. Orchestrates one sync, owns the local state file (`state.go`) | one-sided changes |
| `internal/scan` | Walk the folder and produce path → git blob hash plus size and mtime. Ignore rules, file-name policy and the large-file limit go here (#6) | regular files only |
| `internal/reconcile` | Pure three-way rules: `Reconcile(base, local, remote) []Action` | one-sided rules; `Conflict` is detected but not resolved (#4) |
| `internal/gitstore` | Wraps system git (≥ 2.30) on a bare git-dir: fetch, read tree, write blobs/tree/commit, push | done for #2 |
| `internal/apply` | Atomic writes to the folder (temp file + rename), local deletes | basic |
| `internal/registry` | Per-machine Disk list (`registry.json`) and "which Disk am I in" | minimal (#11) |
| `internal/config` | Repo config `.gcd/config.toml` (TOML): `Parse` / `ParseStrict`, defaults, `Format` check (`ErrFormatTooNew`), `IsManaged` | done (#3) |
| `internal/fixture` | Seam A test fixture: `file://` bare remote + N devices; `Remote.Commit` plants files as another client | done |
| `internal/compact`, `auth`, `watch`, `workflow` | Not created yet; add them when their ticket lands (#12, #13, #16, #15) | — |

Dependency direction: `cli → disk, registry → scan, reconcile, gitstore, apply`. `reconcile` imports nothing from gcd. `fixture` imports `disk` and is used only from external `_test` packages, such as `package disk_test`.

## On-disk layout (per machine)

```
<gcd home>/                      $GCD_HOME, else the OS user-data dir + /gcd
  registry.json                  Disk list: name, folder, remote, state_dir
  disks/<name>/                  state dir (always outside the synced folder)
    state.json                   {"base_tree": "<tree hash>", "last_sync": ...}
    git/                         bare git-dir; remote "origin" = the Disk's URL
```

The synced folder holds only user files and never a `.git`. gcd syncs through the remote's `main` branch only. Its local view is `refs/remotes/origin/main`.

## Sync data flow (`disk.Sync`)

```
scan.Scan(folder) ──────────────► local  {path → blob hash}
gitstore.Fetch → TreeOf → ReadTree ► remote {path → Entry}
gitstore.ReadTree(state.base_tree) ► base   {path → Entry}
                 │
reconcile.Reconcile(base, local, remote) → []Action
   Upload / DeleteRemote  → WriteBlobs → WriteTree(remote ± changes)
                            → CommitTree(parent = remote head,
                                         author = host,
                                         "sync from <host>: +A ~M -D")
                            → Push (fast-forward only)
   Download / DeleteLocal → apply.Apply (temp file + rename)
                 │
state.base_tree = new tree (or the remote tree when nothing was uploaded)
```

Key invariants:

- **The base is a tree hash, not a commit**, so history compaction (#12) does not invalidate it.
- **No merge, rebase, pull, index or checkout.** gcd builds trees itself from a throwaway `GIT_INDEX_FILE` and `commit-tree`.
- **All content is binary.** It goes through `hash-object --no-filters` and `cat-file blob`, and `core.autocrlf=false` is set in the git-dir. Local hashes are computed in Go (SHA-1 of `blob <n>\0<data>`), so they compare directly with tree entries.
- Commits are created only when there are local changes to upload. A download-only sync makes no commit.
- Only `100644` entries are written: no exec bit, no symlinks.

Hooks later tickets plug into: lock and push retry go around fetch → push in `Sync` (#5). The mass-delete check goes between reconcile and commit (#7). The mtime/size re-check goes before commit and before replace (#8). `Status` is the same pipeline without commit, push or apply (#9).

## Repo config and managed paths (#3)

- `.gcd/` and `.github/` (matched case-insensitively) are managed paths. `disk` drops them from base, local and remote before reconcile, so they are never written to the folder, never uploaded from it and never deleted for being absent. The commit keeps them because it starts from the remote tree.
- `Sync` parses `.gcd/config.toml` from the fetched remote tree right after `ReadTree`. A missing file means defaults. A newer `format` stops the sync before any change: `Result.FormatTooNew` plus a `*config.FormatError` that tells the user to upgrade. An invalid config also stops the sync. Later tickets read the parsed `config.Config` there (ignore rules and file limit #6, mass delete #7). Sync parses leniently and ignores unknown keys.
- `disk.RepoConfig` / `SetRepoConfig` / `EnsureRepoConfig` read and commit the config as a single-file commit on top of the remote head (fast-forward push, `ErrRemoteChanged` if the remote moved). `gcd init` calls `EnsureRepoConfig`. `gcd config edit` opens `$EDITOR` and commits only if `ParseStrict` accepts the file, which also rejects unknown keys.
- Bump `config.Format` only when the repo layout or meaning changes.

## Testing

- **Seam A**: `internal/disk` tests through `fixture.NewDevices(t, n)`. Each device has its own folder, state dir and injectable `Host`. Assertions use `Device.Files` / `AssertFiles`, `Remote.Files` / `AssertFiles` and `Remote.Log`.
- **Seam B** (compaction, #12) gets its own fixture for bare repos with chosen commit times.
- CLI: smoke tests only (`internal/cli`). They use `GCD_HOME` to simulate separate machines.
- Tests run offline and must pass on macOS, Linux and Windows (CI matrix in `.github/workflows/ci.yml`).
