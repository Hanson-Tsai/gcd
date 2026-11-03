package workflow

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// DefaultReleasesURL is where gcd's GitHub releases keep their assets:
// <base>/v<version>/<asset>.
const DefaultReleasesURL = "https://github.com/Hanson-Tsai/gcd/releases/download"

var (
	// ErrDevBuild: the version is a development build (no release exists
	// for it), so there is nothing to pin.
	ErrDevBuild = errors.New("development build")
	// ErrNoRelease: the release or its checksums.txt was not found.
	ErrNoRelease = errors.New("release not found")
)

// Releases finds gcd release assets. BaseURL is injectable so tests run
// against an httptest server.
type Releases struct {
	BaseURL string       // default DefaultReleasesURL
	HTTP    *http.Client // default: 30 s timeout
}

// AssetName is the archive the workflow runs: GoReleaser's linux/amd64
// archive (GitHub's ubuntu runners are x86_64).
func AssetName(version string) string { return "gcd_" + version + "_linux_amd64.tar.gz" }

// pseudoRE matches the Go pseudo-version suffix (yyyymmddhhmmss-commit)
// that `go build` stamps into binaries built from a checkout.
var pseudoRE = regexp.MustCompile(`(^|[.-])[0-9]{14}-[0-9a-f]{12}$`)

// IsRelease reports whether version can name a published release: not "dev",
// not a Go pseudo-version, no +build metadata (e.g. +dirty).
func IsRelease(version string) bool {
	return versionRE.MatchString(version) && !strings.Contains(version, "+") && !pseudoRE.MatchString(version)
}

// Pin downloads the checksums.txt of gcd release version (without "v") and
// returns the pin for its linux/amd64 archive.
func (r Releases) Pin(ctx context.Context, version string) (Pin, error) {
	if !versionRE.MatchString(version) && version != "dev" {
		return Pin{}, fmt.Errorf("%q is not a gcd version (want e.g. 1.2.3)", version)
	}
	if !IsRelease(version) {
		return Pin{}, fmt.Errorf("%w: gcd %s has no release whose checksum the workflow could pin", ErrDevBuild, version)
	}
	base := r.BaseURL
	if base == "" {
		base = DefaultReleasesURL
	}
	dir := strings.TrimSuffix(base, "/") + "/v" + version + "/"
	sums, err := r.get(ctx, dir+"checksums.txt")
	if err != nil {
		return Pin{}, err
	}
	asset := AssetName(version)
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		sum, name, ok := strings.Cut(strings.TrimSpace(sc.Text()), "  ")
		if ok && strings.TrimPrefix(name, "*") == asset {
			sum = strings.ToLower(sum)
			if !sha256RE.MatchString(sum) {
				return Pin{}, fmt.Errorf("%schecksums.txt: invalid SHA-256 for %s", dir, asset)
			}
			return Pin{Version: version, SHA256: sum, URL: dir + asset}, nil
		}
	}
	return Pin{}, fmt.Errorf("%schecksums.txt lists no %s", dir, asset)
}

func (r Releases) get(ctx context.Context, url string) ([]byte, error) {
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gcd")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrNoRelease, url)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("download %s: HTTP %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	return b, nil
}
