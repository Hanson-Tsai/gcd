package disk_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

func TestManagedPathsStayOutOfTheFolderAndSurviveSyncs(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	managed := map[string]string{
		".gcd/config.toml":          "format = 1\n",
		".github/workflows/gcd.yml": "on: push\n",
	}
	remote.Commit(t, managed, "install gcd")

	a.Write(t, "notes.md", "hello")
	a.Sync(t)
	b.Sync(t)
	b.Write(t, "todo.md", "milk")
	b.Sync(t)
	a.Sync(t)

	want := map[string]string{"notes.md": "hello", "todo.md": "milk"}
	a.AssertFiles(t, want)
	b.AssertFiles(t, want)
	remote.AssertFiles(t, map[string]string{
		".gcd/config.toml":          "format = 1\n",
		".github/workflows/gcd.yml": "on: push\n",
		"notes.md":                  "hello",
		"todo.md":                   "milk",
	})
}

func TestLocalFoldersNamedLikeManagedPathsAreNotUploaded(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{".gcd/config.toml": "format = 1\n"}, "install gcd")

	a.Write(t, ".gcd/config.toml", "retention_days = 1\n")
	a.Write(t, ".GitHub/workflows/evil.yml", "on: push\n")
	a.Write(t, "notes.md", "hello")
	a.Sync(t)

	remote.AssertFiles(t, map[string]string{
		".gcd/config.toml": "format = 1\n",
		"notes.md":         "hello",
	})
}

func TestSyncRefusesARepoWithANewerFormat(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{
		".gcd/config.toml": "format = 2\nsome_future_setting = true\n",
		"from-new-gcd.md":  "new",
	}, "upgrade repo")
	before := remote.Head(t)
	a.Write(t, "notes.md", "hello")

	res, err := a.Disk.Sync(context.Background(), disk.SyncOptions{})

	if !errors.Is(err, config.ErrFormatTooNew) {
		t.Fatalf("sync error = %v, want ErrFormatTooNew", err)
	}
	if !res.FormatTooNew {
		t.Error("result does not report the format incompatibility")
	}
	if !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("error %q does not tell the user to upgrade", err)
	}
	if got := remote.Head(t); got != before {
		t.Errorf("remote moved from %s to %s", before, got)
	}
	a.AssertFiles(t, map[string]string{"notes.md": "hello"})
}

func TestSyncAcceptsTheCurrentFormatAndAMissingConfig(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]

	a.Write(t, "one.md", "1")
	a.Sync(t) // no repo config at all: defaults apply
	remote.Commit(t, map[string]string{".gcd/config.toml": "format = 1\nretention_days = 7\n"}, "config")
	b.Write(t, "two.md", "2")
	b.Sync(t)
	a.Sync(t)

	a.AssertFiles(t, map[string]string{"one.md": "1", "two.md": "2"})
}

func TestSyncRefusesAnInvalidRepoConfig(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{".gcd/config.toml": "retention_days = \"a month\"\n"}, "bad config")
	before := remote.Head(t)
	a.Write(t, "notes.md", "hello")

	_, err := a.Disk.Sync(context.Background(), disk.SyncOptions{})

	if err == nil || !strings.Contains(err.Error(), "config edit") {
		t.Fatalf("sync error = %v, want an invalid-config error pointing at `gcd config edit`", err)
	}
	if got := remote.Head(t); got != before {
		t.Errorf("remote moved from %s to %s", before, got)
	}
}
