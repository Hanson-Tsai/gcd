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
