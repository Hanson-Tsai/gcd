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

## History compaction (#12)

Offline coverage: `internal/compact` (Seam B, `file://` repos with chosen
commit times, a lease race and a Seam A two-device sync across a compaction)
and `TestCompactSmoke` in `internal/cli` (`--local`, and `--run` with the
`GITHUB_*` defaults). What only GitHub can show:

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
