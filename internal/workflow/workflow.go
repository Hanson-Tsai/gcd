// Package workflow writes the GitHub Actions workflow that compacts a Disk's
// history (`gcd compact --run`, see package compact) on a schedule and on
// demand (workflow_dispatch).
//
// The workflow never runs whatever gcd is newest: it pins one gcd release
// and the SHA-256 of that release's linux/amd64 archive, verifies the
// download against it and only then runs it. The hash comes from the
// release's checksums.txt (Releases.Pin); `gcd init` installs the workflow
// and `gcd workflow upgrade` re-pins it. The schedule is the repo config's
// compact_schedule, re-rendered by `gcd config edit`.
package workflow

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Path is where the workflow lives in the repo (slash-separated). It is
// inside a managed directory (config.ManagedDirs), so it never shows up in
// the synced folder.
const Path = ".github/workflows/" + FileName

// FileName is the workflow's file name, which the GitHub API accepts as its
// id (workflow_dispatch).
const FileName = "gcd-compact.yml"

// SecretName is the optional repository secret holding a fine-grained PAT
// (Contents and Workflows: read and write). When set, the workflow pushes
// with it instead of the Actions token, which GitHub may refuse for rewritten
// commits that touch .github/workflows/.
const SecretName = "GCD_TOKEN"

// Pin is one gcd release the workflow downloads and runs.
type Pin struct {
	Version string // without "v", e.g. "1.2.3"
	SHA256  string // hex SHA-256 of the archive at URL
	URL     string // the linux/amd64 release archive
}

// Spec is everything a rendered workflow depends on.
type Spec struct {
	Pin      Pin
	Schedule string // 5-field cron expression (UTC), from compact_schedule
}

var (
	versionRE  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
	sha256RE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	urlRE      = regexp.MustCompile(`^https?://[A-Za-z0-9._~:/%+-]+$`)
	cronWordRE = regexp.MustCompile(`^[0-9A-Za-z*,/-]+$`)
)

// CheckSchedule reports whether s is a cron expression the workflow can
// carry: 5 fields of digits, letters and * , / -.
func CheckSchedule(s string) error {
	f := strings.Fields(s)
	if len(f) != 5 {
		return fmt.Errorf("compact_schedule must be a cron expression with 5 fields, got %q", s)
	}
	for _, w := range f {
		if !cronWordRE.MatchString(w) {
			return fmt.Errorf("compact_schedule field %q may only contain digits, letters and * , / -", w)
		}
	}
	return nil
}

func (s Spec) check() error {
	var errs []error
	if !versionRE.MatchString(s.Pin.Version) {
		errs = append(errs, fmt.Errorf("invalid gcd version %q", s.Pin.Version))
	}
	if !sha256RE.MatchString(s.Pin.SHA256) {
		errs = append(errs, fmt.Errorf("invalid SHA-256 %q", s.Pin.SHA256))
	}
	if !urlRE.MatchString(s.Pin.URL) {
		errs = append(errs, fmt.Errorf("invalid download URL %q", s.Pin.URL))
	}
	errs = append(errs, CheckSchedule(s.Schedule))
	return errors.Join(errs...)
}

// Render returns the workflow file for s.
func Render(s Spec) ([]byte, error) {
	if err := s.check(); err != nil {
		return nil, fmt.Errorf("compaction workflow: %w", err)
	}
	schedule := strings.Join(strings.Fields(s.Schedule), " ")
	return []byte(fmt.Sprintf(template, schedule, s.Pin.Version, s.Pin.URL, s.Pin.SHA256)), nil
}

// The job uses no actions at all (not even checkout): nothing to pin besides
// gcd itself, and no credentials persisted by a checkout. gcd fetches the
// repo with its own token handling.
const template = `# Managed by gcd: compacts this repo's history (gcd compact --run).
# Do not edit by hand:
#   - ` + "`gcd workflow upgrade`" + ` pins the gcd release running on your machine;
#   - ` + "`gcd config edit`" + ` (compact_schedule) changes the schedule.
# The job downloads the pinned gcd release, checks its SHA-256 and only then
# runs it.
#
# Pushing rewritten history needs the Workflows permission when it touches
# .github/workflows/, which the Actions token cannot have. If runs fail with
# "refusing to allow ... to create or update workflow", add a fine-grained
# token for this repo (Contents and Workflows: read and write) as the
# repository secret ` + SecretName + `.
name: gcd compact

on:
  schedule:
    - cron: "%s"
  workflow_dispatch:

permissions:
  contents: write

concurrency:
  group: gcd-compact
  cancel-in-progress: false

jobs:
  compact:
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    env:
      GCD_VERSION: "%s"
      GCD_URL: "%s"
      GCD_SHA256: "%s"
    steps:
      - name: Download and verify gcd
        run: |
          set -euo pipefail
          curl -fsSL --retry 3 -o gcd.tar.gz "$GCD_URL"
          echo "$GCD_SHA256  gcd.tar.gz" | sha256sum --check --strict -
          tar -xzf gcd.tar.gz gcd
          ./gcd version
      - name: Compact history
        env:
          GCD_TOKEN: ${{ secrets.` + SecretName + ` || github.token }}
        run: ./gcd compact --run
`

var parseRE = regexp.MustCompile(`(?m)^    - cron: "([^"\n]*)"\r?$|^      GCD_(VERSION|URL|SHA256): "([^"\n]*)"\r?$`)

// Parse reads the Spec back from a workflow written by Render. It fails for
// a file gcd did not write (or one edited beyond recognition).
func Parse(raw []byte) (Spec, error) {
	var s Spec
	seen := map[string]int{}
	for _, m := range parseRE.FindAllStringSubmatch(string(raw), -1) {
		key := m[2]
		switch key {
		case "":
			key, s.Schedule = "cron", m[1]
		case "VERSION":
			s.Pin.Version = m[3]
		case "URL":
			s.Pin.URL = m[3]
		case "SHA256":
			s.Pin.SHA256 = m[3]
		}
		seen[key]++
	}
	for _, k := range []string{"cron", "VERSION", "URL", "SHA256"} {
		if seen[k] != 1 {
			return Spec{}, fmt.Errorf("%s was not written by this gcd (no single %s line)", Path, k)
		}
	}
	if err := s.check(); err != nil {
		return Spec{}, fmt.Errorf("%s: %w", Path, err)
	}
	return s, nil
}
