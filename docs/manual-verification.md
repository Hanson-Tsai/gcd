# Manual verification

Checks that need real GitHub and cannot run in the offline test suite. Run
them before a release that changes the area, and tick them in the PR.

## Release (#14)

Offline coverage: `go test ./...` builds gcd with the release ldflags and
checks `gcd version` (`cmd/gcd/main_test.go`). Locally,
`go run github.com/goreleaser/goreleaser/v2@v2.18.3 check` and
`... release --snapshot --clean` validate `.goreleaser.yaml` and build all six
archives plus `checksums.txt` into `dist/` without publishing.

What only GitHub can show:

1. Push a pre-release tag from the commit to release:
   `git tag v0.0.1-rc.1 && git push origin v0.0.1-rc.1`.
2. The **Release** workflow (`.github/workflows/release.yml`) runs and passes.
3. The release `v0.0.1-rc.1` exists, is marked **pre-release** (tags with a
   `-suffix` are), and has these 7 assets:
   - `gcd_0.0.1-rc.1_{darwin,linux}_{amd64,arm64}.tar.gz` (4)
   - `gcd_0.0.1-rc.1_windows_{amd64,arm64}.zip` (2)
   - `checksums.txt`
4. Download `checksums.txt` and one archive per OS you have. Check it with
   `sha256sum --check --ignore-missing checksums.txt`
   (macOS: `shasum -a 256 ...`; Windows: `Get-FileHash`).
5. Unpack and run `gcd version` on macOS, Linux and Windows. Each prints
   `gcd version 0.0.1-rc.1 <os>/<arch>` and the tagged commit.
6. `go install github.com/Hanson-Tsai/gcd/cmd/gcd@v0.0.1-rc.1` on a clean
   machine (the source repo must be public, or set `GOPRIVATE`), then
   `gcd version` prints `gcd version 0.0.1-rc.1`.
7. Delete the test release and tag when done:
   `gh release delete v0.0.1-rc.1 --cleanup-tag --yes`.

## GitHub auth (#13)

Offline coverage: `internal/auth` (checks against a fake GitHub API,
keychain lookup order, prompt), `internal/gitstore` (token reaches git only
through `GIT_ASKPASS`, user credential helpers ignored, rejected token is
`ErrAuth` with the token scrubbed) and `internal/cli` (`init owner/repo`,
`login` and `sync` against a fake GitHub serving real repos over smart HTTP).

Setup: a private throwaway repo `<you>/gcd-e2e` and fine-grained PATs on it:
`FULL` (Contents, Workflows, Actions: read and write) and `READONLY`
(Contents: read only).

Automated part (manual trigger only):

1. Set repository variable `GCD_E2E_REPO=<you>/gcd-e2e` and secrets
   `GCD_E2E_TOKEN=FULL`, `GCD_E2E_READONLY_TOKEN=READONLY` on the gcd repo.
2. Run the **E2E** workflow (Actions tab, *Run workflow*). It runs
   `go test -run E2E ./...` on all three OSes. Locally:
   `GCD_E2E_REPO=... GCD_TOKEN=... go test -count=1 -v -run E2E ./...`.
3. It checks: `Check` passes for the private repo, refuses
   `octocat/Hello-World` as public, reports a missing repo with how to create
   it, reports an invalid token, lists Contents as missing for `READONLY`,
   and two machines `init` + `sync` a file over HTTPS with the token, then an
   invalid `GCD_TOKEN` makes `sync` print the token page and `gcd login`.

By hand (keychain and prompts, once per OS):

4. With `GCD_TOKEN` unset, `gcd init <you>/gcd-e2e ~/gcd-e2e`. Paste `FULL`
   at the prompt: it is not echoed, init succeeds, and the token is in the
   keychain (macOS: Keychain Access, item `gcd`, account
   `github.com/<you>/gcd-e2e`; Windows: Credential Manager, `gcd:github.com/...`;
   Linux: `secret-tool search service gcd`).
5. `gcd sync` with no prompt. While it runs, `ps -ef | grep git` (Windows:
   Task Manager command line) shows no token. `git --git-dir
   "<gcd home>/disks/gcd-e2e/git" config --list` shows no token, and
   `~/.gitconfig` helpers (osxkeychain/manager) gained no github.com entry.
6. Revoke `FULL` on GitHub, then `gcd sync`: the error says the token was
   rejected, links the pre-filled token page and suggests `gcd login`.
   `gcd login <you>/gcd-e2e` with a new token, and `gcd sync` works again.
7. `gcd init` a public repo: refused before any folder or registry entry is
   created. `gcd init <you>/does-not-exist`: error with the
   `https://github.com/new?...` link.
8. A token **without Actions** write: init lists `Actions: read and write`
   as missing. This verifies that GitHub checks the permission before looking
   up the (nonexistent) workflow in the dispatch probe. If it instead passes,
   the probe is wrong.
9. A token **without Workflows** write: there is no probe. Once #15 installs
   the workflow during init, init must fail with
   "missing permission: Workflows (read and write)".
10. Confirm that username `x-access-token` with a fine-grained PAT works for
    git over HTTPS (step 5 passing covers it).
11. Linux without a Secret Service (e.g. SSH session, container): init says the
    keychain is unavailable and suggests `GCD_TOKEN`. With `GCD_TOKEN` set,
    everything works without the keychain.

## History compaction (#12)

Offline coverage: `internal/compact` (Seam B, `file://` repos with chosen
commit times, a lease race and a Seam A two-device sync across a compaction)
and `internal/cli`. There, `TestCompactSmoke` covers `--local` and `--run`
with the `GITHUB_*` defaults, and `TestCompactRunAsInActions` covers `--run`
over HTTPS with `GCD_TOKEN` against the fake GitHub. What only GitHub can show:

1. On a test private repo with history older than `retention_days`, run
   `gcd compact --local` from a machine with the user's PAT (Contents +
   Workflows). The push is accepted. `git log` on GitHub shows the snapshot
   root and the kept commits with their original authors and dates.
2. Same, but with `.github/workflows/gcd-compact.yml` present in squashed and
   kept commits. Check whether GitHub accepts the force push (it rewrites
   commits that touch workflow files). Repeat in step 3 with the Actions
   `GITHUB_TOKEN`, which has no `workflows` permission. That is the riskiest
   case (see #15).
3. In Actions (#15): `GCD_TOKEN=${{ github.token }} ./gcd compact --run`
   without `--repo` uses `$GITHUB_SERVER_URL/$GITHUB_REPOSITORY.git` and
   succeeds. The token never appears in the job log.
4. Lease on real GitHub: start `gcd compact --local` and push from another
   machine while it runs (or run it against a repo while another machine's
   `gcd watch` is active). Compaction exits 1 with "lease failed" and the
   other push stays on GitHub. GitHub reports this as `[rejected] (stale
   info)`, which gcd classifies as a lease failure.
5. A protected branch that blocks force pushes: compaction fails with
   GitHub's `[remote rejected]` text, not a lease failure.
6. After compaction, `gcd sync` on two other machines reports no conflicts,
   and `gcd log` / `gcd restore --at <before snapshot>` report the retention
   boundary as the snapshot's date.

## gcd watch (#16)

The smoke tests cover watching on the CI OS with `file://` remotes and short
timings. By hand, once per OS (macOS, Linux, Windows):

1. Two disks registered: `gcd watch`. Edit and save a file in each folder
   several times within a few seconds: about 10 s after the last save, one
   `up` line per disk, and one commit per burst on GitHub.
2. Create a nested folder with files (`mkdir -p a/b && echo x > a/b/c.md`),
   then edit `a/b/c.md` later: both are uploaded (new directories are watched).
3. Push a change from another machine: it is downloaded within about 60 s.
4. Delete most files of a disk: watch prints a warning with the
   `gcd sync --disk <name> --yes` hint and the remote keeps the files.
5. While watch is running, run `gcd sync` in another terminal: both succeed or
   one reports the other is syncing; no errors in watch.
6. Copy many large files in, wait ~10 s, press Ctrl+C while the `up` lines are
   still coming: "stopping once running syncs finish", the sync completes and
   gcd exits 0 (on Windows this checks that the child processes in their own
   process group ignore the console Ctrl+C). A second Ctrl+C during that wait
   kills gcd at once; the next `gcd sync` completes normally.
7. Linux: `sysctl fs.inotify.max_user_watches=100` and a folder with more
   directories: watch prints a "file watching limited" warning and still
   uploads changes within about a poll interval plus the debounce.

## Actions compaction workflow (#15)

**Verify first: does GitHub accept the compaction push made with the Actions
`GITHUB_TOKEN`?** That token can never have the Workflows permission, and
compaction rewrites commits that contain `.github/workflows/gcd-compact.yml`
(the new snapshot root adds it, and kept commits from `gcd workflow upgrade`
or schedule changes touch it). It is undocumented whether GitHub checks only
the old tip against the new tip, or every pushed commit. The offline fake
GitHub assumes per-commit checks (`authtest` pre-receive hook).

1. On a throwaway private repo with history older than `retention_days`
   (set `retention_days = 1` with `gcd config edit` and wait a day, or plant
   back-dated commits with `GIT_AUTHOR_DATE`/`GIT_COMMITTER_DATE`), with the
   workflow installed and **no** `GCD_TOKEN` repository secret, run the
   workflow (`gcd compact`).
   - **Accepted**: compaction works out of the box. Note it here and in the
     README, and the secret stays an optional extra.
   - **Refused** with `refusing to allow a GitHub App to create or update
     workflow`: the job log must show gcd's hint (add the repository secret
     `GCD_TOKEN`, or `gcd compact --local`). Then add a fine-grained PAT
     (Contents + Workflows: read and write on this repo only) as the secret
     `GCD_TOKEN`, re-run, and it must succeed. Record the outcome; if the
     secret is always needed, make `gcd init` say so (follow-up ticket).

Offline coverage: `internal/workflow` (render, checksum pinning from
`checksums.txt` served by httptest, version rules, YAML shape),
`internal/disk` (install/upgrade/schedule re-render on `file://` repos),
`internal/auth` (workflow_dispatch against the fake GitHub) and
`internal/cli` (`init` installs the pinned workflow and is the Workflows
probe, `compact` dispatches and prints the run page, `workflow upgrade`, dev
builds, the Actions-token refusal hint). Opt-in:
`TestE2EWorkflowUpgradeAndDispatch` (set repository variable
`GCD_E2E_GCD_VERSION` to a published release).

What only GitHub can show:

2. With a **release** build (a dev build skips the workflow and says so),
   `gcd init <you>/gcd-e2e ~/gcd-e2e` prints `installed the compaction
   workflow .github/workflows/gcd-compact.yml (gcd X.Y.Z)`. On GitHub the
   file pins `GCD_VERSION`, and `GCD_SHA256` equals the
   `gcd_X.Y.Z_linux_amd64.tar.gz` line of the release's `checksums.txt`. The
   Actions tab lists **gcd compact** with a schedule.
3. A token **without Workflows** write: `gcd init` fails naming
   `Workflows: read and write`; no disk is registered and nothing was pushed
   (this also closes #13 step 9).
4. `gcd compact` prints `https://github.com/<you>/gcd-e2e/actions/runs/<id>`
   (GitHub returns run details since 2026-02-19; on 204 it prints the
   workflow's page). The run: downloads, `sha256sum --check` passes, prints
   `gcd version X.Y.Z linux/amd64`, compacts (or says nothing to compact).
   The token never appears in the log.
5. Tamper check: edit `GCD_SHA256` in the workflow on GitHub (one hex digit)
   and run it. The job must fail at the checksum step and never run gcd.
   Restore with `gcd workflow upgrade`.
6. A token **without Actions** write: `gcd compact` fails naming
   `Actions: read and write`.
7. `gcd config edit`, change `compact_schedule` to a time a few minutes
   ahead (UTC): one commit changes both `.gcd/config.toml` and the workflow's
   `cron:`, and the scheduled run starts (GitHub may delay it by several
   minutes).
8. With an older release pinned, `gcd workflow upgrade` from a newer release
   prints `gcd A -> B` and the workflow pins B with B's checksum.
9. After the Actions run compacts, `gcd sync` on two machines reports no
   conflicts and `gcd log` starts at the snapshot (the #12 step 6 check, with
   Actions doing the compaction). This is the ticket's end-to-end criterion.
