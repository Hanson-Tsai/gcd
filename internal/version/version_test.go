package version

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	installed := &debug.BuildInfo{Main: debug.Module{Path: "github.com/Hanson-Tsai/gcd", Version: "v1.4.0"}}
	checkout := &debug.BuildInfo{Main: debug.Module{Path: "github.com/Hanson-Tsai/gcd", Version: "(devel)"}}
	for _, tc := range []struct {
		name   string
		ldflag string
		bi     *debug.BuildInfo
		want   string
	}{
		{"goreleaser build", "1.2.3", installed, "1.2.3"},
		{"ldflag with v prefix", "v1.2.3", nil, "1.2.3"},
		{"go install module@version", "", installed, "1.4.0"},
		{"go build from a checkout", "", checkout, Dev},
		{"no build info", "", nil, Dev},
	} {
		if got := resolve(tc.ldflag, tc.bi); got != tc.want {
			t.Errorf("%s: resolve(%q) = %q, want %q", tc.name, tc.ldflag, got, tc.want)
		}
	}
}
