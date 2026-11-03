// Package version reports which gcd release this binary is.
//
// Release builds (GoReleaser, see .goreleaser.yaml) set the variables below
// with -ldflags "-X". Binaries built with `go install ...@vX.Y.Z` carry the
// module version in their build info instead, which Version falls back to.
package version

import (
	"runtime/debug"
	"strings"
)

// Set at link time by release builds, e.g.
// -X github.com/Hanson-Tsai/gcd/internal/version.version=1.2.3
var (
	version = ""
	commit  = ""
	date    = ""
)

// Dev is reported when the binary carries no version at all, e.g. `go build`
// outside a git checkout. (`go build` inside a checkout reports a Go
// pseudo-version such as 0.0.0-20261103080000-abcdef123456+dirty.)
const Dev = "dev"

// Version returns the release version without a leading "v" (e.g. "1.2.3"),
// or Dev for a development build. A release's git tag is "v" + Version().
func Version() string {
	bi, _ := debug.ReadBuildInfo()
	return resolve(version, bi)
}

// Commit returns the git commit the binary was built from, or "".
func Commit() string {
	if commit != "" {
		return commit
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return ""
}

// Date returns the commit date of a release build (RFC 3339), or "".
func Date() string { return date }

// resolve picks the link-time version, else the module version recorded by
// `go install module@version`, else Dev.
func resolve(ldflag string, bi *debug.BuildInfo) string {
	if ldflag != "" {
		return strings.TrimPrefix(ldflag, "v")
	}
	if bi != nil {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return Dev
}
