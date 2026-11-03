package workflow_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

const sum123 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// serveRelease serves checksums.txt for v1.2.3 the way GoReleaser publishes
// it, and 404 for every other path.
func serveRelease(t *testing.T) workflow.Releases {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.2.3/checksums.txt" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(strings.Repeat("a", 64) + "  gcd_1.2.3_darwin_arm64.tar.gz\n" +
			sum123 + "  gcd_1.2.3_linux_amd64.tar.gz\n" +
			strings.Repeat("b", 64) + "  gcd_1.2.3_linux_arm64.tar.gz\n" +
			strings.Repeat("c", 64) + "  gcd_1.2.3_windows_amd64.zip\n"))
	}))
	t.Cleanup(srv.Close)
	return workflow.Releases{BaseURL: srv.URL, HTTP: srv.Client()}
}

func TestPinTakesTheLinuxAmd64ArchiveHashFromChecksums(t *testing.T) {
	rel := serveRelease(t)
	pin, err := rel.Pin(context.Background(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	want := workflow.Pin{Version: "1.2.3", SHA256: sum123, URL: rel.BaseURL + "/v1.2.3/gcd_1.2.3_linux_amd64.tar.gz"}
	if pin != want {
		t.Errorf("Pin = %+v, want %+v", pin, want)
	}
}

func TestPinRefusesVersionsWithoutARelease(t *testing.T) {
	rel := serveRelease(t)
	for _, v := range []string{"dev", "0.0.0-20261103080000-abcdef123456", "1.2.4-0.20261103080000-abcdef123456+dirty", "1.2.3+dirty"} {
		if _, err := rel.Pin(context.Background(), v); !errors.Is(err, workflow.ErrDevBuild) {
			t.Errorf("Pin(%q) = %v, want ErrDevBuild", v, err)
		}
	}
	if _, err := rel.Pin(context.Background(), "9.9.9"); !errors.Is(err, workflow.ErrNoRelease) {
		t.Errorf("Pin(9.9.9) = %v, want ErrNoRelease", err)
	}
	for _, v := range []string{"1.2", "1.2.3\nx", `1.2.3"`} {
		if _, err := rel.Pin(context.Background(), v); err == nil {
			t.Errorf("Pin(%q) succeeded", v)
		}
	}
}

func TestPinRejectsChecksumsWithoutTheAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", 64) + "  gcd_1.2.3_darwin_arm64.tar.gz\n"))
	}))
	defer srv.Close()
	rel := workflow.Releases{BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := rel.Pin(context.Background(), "1.2.3"); err == nil || !strings.Contains(err.Error(), "gcd_1.2.3_linux_amd64.tar.gz") {
		t.Errorf("Pin = %v, want an error naming the missing asset", err)
	}
}

// ghWorkflow is the part of a GitHub Actions workflow file gcd relies on.
type ghWorkflow struct {
	On struct {
		Schedule []struct {
			Cron string `yaml:"cron"`
		} `yaml:"schedule"`
	} `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		RunsOn string            `yaml:"runs-on"`
		Env    map[string]string `yaml:"env"`
		Steps  []struct {
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestRenderPinsVersionAndChecksumAndVerifiesBeforeRunning(t *testing.T) {
	pin := workflow.Pin{Version: "1.2.3", SHA256: sum123, URL: "https://example.test/v1.2.3/gcd_1.2.3_linux_amd64.tar.gz"}
	raw, err := workflow.Render(workflow.Spec{Pin: pin, Schedule: "17 3 * * 0"})
	if err != nil {
		t.Fatal(err)
	}
	var wf ghWorkflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("not valid YAML: %v\n%s", err, raw)
	}
	if len(wf.On.Schedule) != 1 || wf.On.Schedule[0].Cron != "17 3 * * 0" {
		t.Errorf("schedule = %+v, want cron 17 3 * * 0", wf.On.Schedule)
	}
	if !strings.Contains(string(raw), "\n  workflow_dispatch:") {
		t.Error("no workflow_dispatch trigger")
	}
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "write" {
		t.Errorf("permissions = %v, want only contents: write", wf.Permissions)
	}
	job, ok := wf.Jobs["compact"]
	if !ok || len(job.Steps) != 2 {
		t.Fatalf("jobs = %+v, want job compact with 2 steps", wf.Jobs)
	}
	if job.RunsOn != "ubuntu-24.04" {
		t.Errorf("runs-on = %q, want an x86_64 Linux runner for the linux_amd64 archive", job.RunsOn)
	}
	if job.Env["GCD_VERSION"] != "1.2.3" || job.Env["GCD_SHA256"] != sum123 || job.Env["GCD_URL"] != pin.URL {
		t.Errorf("job env = %v, want the pin", job.Env)
	}
	dl, run := job.Steps[0], job.Steps[1]
	if dl.Uses != "" || run.Uses != "" {
		t.Error("steps use third-party actions")
	}
	check := strings.Index(dl.Run, "sha256sum --check")
	if check < 0 || !strings.Contains(dl.Run, "set -euo pipefail") || strings.Index(dl.Run, "tar ") < check {
		t.Errorf("download step does not verify the checksum before unpacking:\n%s", dl.Run)
	}
	if !strings.Contains(run.Run, "./gcd compact --run") {
		t.Errorf("run step = %q, want ./gcd compact --run", run.Run)
	}
	if tok := run.Env["GCD_TOKEN"]; tok != "${{ secrets.GCD_TOKEN || github.token }}" {
		t.Errorf("GCD_TOKEN = %q, want the GCD_TOKEN secret falling back to github.token", tok)
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	spec := workflow.Spec{
		Pin:      workflow.Pin{Version: "1.2.3-rc.1", SHA256: sum123, URL: "https://example.test/a.tar.gz"},
		Schedule: "*/30 1-5 * 1,6 MON-FRI",
	}
	raw, err := workflow.Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := workflow.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != spec {
		t.Errorf("Parse(Render(s)) = %+v, want %+v", got, spec)
	}
	if _, err := workflow.Parse([]byte("name: something else\n")); err == nil {
		t.Error("Parse accepted a workflow gcd did not write")
	}
}

func TestRenderRejectsValuesThatWouldBreakTheYAML(t *testing.T) {
	ok := workflow.Spec{Pin: workflow.Pin{Version: "1.2.3", SHA256: sum123, URL: "https://example.test/a.tar.gz"}, Schedule: "17 3 * * 0"}
	bad := []func(*workflow.Spec){
		func(s *workflow.Spec) { s.Schedule = `17 3 * * 0"` },
		func(s *workflow.Spec) { s.Schedule = "17 3 * *" },
		func(s *workflow.Spec) { s.Pin.Version = "1.2.3\n" },
		func(s *workflow.Spec) { s.Pin.SHA256 = "xyz" },
		func(s *workflow.Spec) { s.Pin.URL = `https://example.test/"$(id)` },
	}
	for i, f := range bad {
		s := ok
		f(&s)
		if _, err := workflow.Render(s); err == nil {
			t.Errorf("case %d: Render(%+v) succeeded", i, s)
		}
	}
}
