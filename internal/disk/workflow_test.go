package disk_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

func testPin(version string) workflow.Pin {
	return workflow.Pin{
		Version: version,
		SHA256:  strings.Repeat(version[:1], 64),
		URL:     "https://example.test/v" + version + "/" + workflow.AssetName(version),
	}
}

// remoteWorkflow parses the workflow on the remote's main branch.
func remoteWorkflow(t *testing.T, remote *fixture.Remote) workflow.Spec {
	t.Helper()
	raw, ok := remote.Files(t)[workflow.Path]
	if !ok {
		t.Fatalf("remote has no %s", workflow.Path)
	}
	spec, err := workflow.Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestInstallWorkflowWritesItOnceWithTheRepoSchedule(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{config.Path: "compact_schedule = \"5 1 * * 3\"\n", "notes.md": "hi"}, "setup")
	ctx := context.Background()

	res, err := a.Disk.InstallWorkflow(ctx, testPin("1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Existed {
		t.Errorf("first install = %+v, want Changed and not Existed", res)
	}
	want := workflow.Spec{Pin: testPin("1.0.0"), Schedule: "5 1 * * 3"}
	if got := remoteWorkflow(t, remote); got != want {
		t.Errorf("remote workflow = %+v, want %+v", got, want)
	}

	// Another machine running another gcd version leaves it alone: only
	// `gcd workflow upgrade` changes the pinned release.
	head := remote.Head(t)
	res, err = a.Disk.InstallWorkflow(ctx, testPin("0.9.0"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || !res.Existed || res.Previous.Pin.Version != "1.0.0" {
		t.Errorf("second install = %+v, want unchanged, existing 1.0.0", res)
	}
	if remote.Head(t) != head {
		t.Error("second install committed")
	}

	a.Sync(t)
	a.AssertFiles(t, map[string]string{"notes.md": "hi"})
}

func TestUpgradeWorkflowRepinsAndKeepsTheSchedule(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{config.Path: "compact_schedule = \"5 1 * * 3\"\n"}, "setup")
	ctx := context.Background()
	if _, err := a.Disk.InstallWorkflow(ctx, testPin("1.0.0")); err != nil {
		t.Fatal(err)
	}

	res, err := a.Disk.UpgradeWorkflow(ctx, testPin("2.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Previous.Pin.Version != "1.0.0" {
		t.Errorf("upgrade = %+v, want Changed from 1.0.0", res)
	}
	want := workflow.Spec{Pin: testPin("2.0.0"), Schedule: "5 1 * * 3"}
	if got := remoteWorkflow(t, remote); got != want {
		t.Errorf("remote workflow = %+v, want %+v", got, want)
	}

	head := remote.Head(t)
	if res, err = a.Disk.UpgradeWorkflow(ctx, testPin("2.0.0")); err != nil || res.Changed {
		t.Errorf("upgrade to the same pin = %+v, %v; want unchanged", res, err)
	}
	if remote.Head(t) != head {
		t.Error("no-op upgrade committed")
	}
}

func TestUpgradeWorkflowReplacesAHandEditedWorkflow(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	remote.Commit(t, map[string]string{workflow.Path: "name: mine\n"}, "hand-written")

	res, err := devs[0].Disk.UpgradeWorkflow(context.Background(), testPin("1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || !res.Existed || res.Previous != (workflow.Spec{}) {
		t.Errorf("upgrade = %+v, want Changed over an unreadable existing workflow", res)
	}
	want := workflow.Spec{Pin: testPin("1.0.0"), Schedule: config.Default().CompactSchedule}
	if got := remoteWorkflow(t, remote); got != want {
		t.Errorf("remote workflow = %+v, want %+v", got, want)
	}
}

func TestWorkflowIsNotWrittenIntoANewerFormatRepo(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	remote.Commit(t, map[string]string{config.Path: "format = 2\n"}, "newer gcd")
	head := remote.Head(t)
	ctx := context.Background()
	if _, err := devs[0].Disk.InstallWorkflow(ctx, testPin("1.0.0")); !errors.Is(err, config.ErrFormatTooNew) {
		t.Errorf("install = %v, want ErrFormatTooNew", err)
	}
	if _, err := devs[0].Disk.UpgradeWorkflow(ctx, testPin("1.0.0")); !errors.Is(err, config.ErrFormatTooNew) {
		t.Errorf("upgrade = %v, want ErrFormatTooNew", err)
	}
	if remote.Head(t) != head {
		t.Error("remote moved")
	}
}

func TestChangingTheScheduleRewritesTheWorkflowInTheSameCommit(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	ctx := context.Background()
	if _, err := a.Disk.EnsureRepoConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Disk.InstallWorkflow(ctx, testPin("1.0.0")); err != nil {
		t.Fatal(err)
	}
	commits := len(remote.Log(t))

	_, head, err := a.Disk.RepoConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(config.DefaultTOML()), `compact_schedule = "17 3 * * 0"`, `compact_schedule = "0 12 * * 1"`, 1)
	if _, err := a.Disk.SetRepoConfig(ctx, []byte(edited), head); err != nil {
		t.Fatal(err)
	}
	if n := len(remote.Log(t)); n != commits+1 {
		t.Errorf("config edit made %d commits, want 1", n-commits)
	}
	want := workflow.Spec{Pin: testPin("1.0.0"), Schedule: "0 12 * * 1"}
	if got := remoteWorkflow(t, remote); got != want {
		t.Errorf("remote workflow = %+v, want %+v", got, want)
	}
	if got := remote.Files(t)[config.Path]; got != edited {
		t.Errorf("remote config = %q, want the edit", got)
	}
}

func TestConfigEditDoesNotInstallAMissingWorkflow(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	ctx := context.Background()
	_, head, err := a.Disk.RepoConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Disk.SetRepoConfig(ctx, []byte("compact_schedule = \"0 12 * * 1\"\n"), head); err != nil {
		t.Fatal(err)
	}
	if _, ok := remote.Files(t)[workflow.Path]; ok {
		t.Error("config edit installed the workflow")
	}
}
