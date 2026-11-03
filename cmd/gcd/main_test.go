package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestReleaseVersionIsEmbedded builds gcd the way .goreleaser.yaml does and
// checks that `gcd version` reports the injected version.
func TestReleaseVersionIsEmbedded(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "gcd")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	const pkg = "github.com/Hanson-Tsai/gcd/internal/version"
	ldflags := "-X " + pkg + ".version=1.2.3 -X " + pkg + ".commit=abc1234 -X " + pkg + ".date=2026-01-02T03:04:05Z"
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("gcd version: %v\n%s", err, out)
	}
	want := "gcd version 1.2.3 " + runtime.GOOS + "/" + runtime.GOARCH + "\n"
	if !strings.HasPrefix(string(out), want) {
		t.Errorf("gcd version printed %q, want first line %q", out, want)
	}
	for _, s := range []string{"abc1234", "2026-01-02T03:04:05Z"} {
		if !strings.Contains(string(out), s) {
			t.Errorf("gcd version printed %q, want it to contain %q", out, s)
		}
	}
}

// Tags like v0.1.0-rc1 are published as GitHub pre-releases, so they are
// never "latest".
func TestPreReleaseTagsArePublishedAsPreReleases(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^release:\n(?:[ \t].*\n|\n)*?[ \t]+prerelease: auto[ \t]*$`).Match(b) {
		t.Errorf(".goreleaser.yaml does not set release.prerelease: auto")
	}
}
