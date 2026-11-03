package disk_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

func TestTwoDevicesExchangeAddsAndModifications(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]

	a.Write(t, "notes/todo.md", "buy milk\n")
	a.Sync(t)
	b.Sync(t)
	b.AssertFiles(t, map[string]string{"notes/todo.md": "buy milk\n"})

	b.Write(t, "notes/todo.md", "buy milk\nbuy eggs\n")
	b.Write(t, "photo.txt", "pixels")
	b.Sync(t)
	a.Sync(t)

	want := map[string]string{
		"notes/todo.md": "buy milk\nbuy eggs\n",
		"photo.txt":     "pixels",
	}
	a.AssertFiles(t, want)
	b.AssertFiles(t, want)
	remote.AssertFiles(t, want)
}

func TestEachSyncMakesOneCommitAuthoredByHost(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]

	a.Write(t, "a.txt", "1")
	a.Write(t, "b.txt", "2")
	a.Sync(t)
	a.Write(t, "a.txt", "1 changed")
	a.Write(t, "c.txt", "3")
	a.Sync(t)

	log := remote.Log(t)
	if len(log) != 2 {
		t.Fatalf("got %d commits, want 2: %+v", len(log), log)
	}
	if got, want := log[1].Subject, "sync from host1: +2 ~0 -0"; got != want {
		t.Errorf("first commit subject = %q, want %q", got, want)
	}
	if got, want := log[0].Subject, "sync from host1: +1 ~1 -0"; got != want {
		t.Errorf("second commit subject = %q, want %q", got, want)
	}
	for _, c := range log {
		if c.AuthorName != "host1" {
			t.Errorf("commit %s author = %q, want host1", c.Hash, c.AuthorName)
		}
	}
}

func TestSyncRecordsRemoteTreeAsBase(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]

	a.Write(t, "a.txt", "1")
	resA := a.Sync(t)
	head := remote.Log(t)[0]
	if resA.BaseTree != head.Tree {
		t.Errorf("uploader base = %q, want remote tree %q", resA.BaseTree, head.Tree)
	}
	resB := b.Sync(t)
	if resB.BaseTree != head.Tree {
		t.Errorf("downloader base = %q, want remote tree %q", resB.BaseTree, head.Tree)
	}
}

func TestSyncWithoutChangesMakesNoCommit(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]

	a.Write(t, "a.txt", "1")
	a.Sync(t)
	b.Sync(t)
	res := a.Sync(t)
	b.Sync(t)

	if res.Commit != "" || len(res.Uploaded) != 0 || len(res.Downloaded) != 0 {
		t.Errorf("idle sync result = %+v, want empty", res)
	}
	if n := len(remote.Log(t)); n != 1 {
		t.Errorf("got %d commits, want 1", n)
	}
}

func TestSyncedFolderHasNoGitDir(t *testing.T) {
	_, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "a.txt", "1")
	a.Sync(t)

	if _, err := os.Stat(filepath.Join(a.Folder, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git in synced folder: stat err = %v", err)
	}
	a.AssertFiles(t, map[string]string{"a.txt": "1"})
}

func TestContentIsSyncedByteForByte(t *testing.T) {
	_, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	content := map[string]string{
		"crlf.txt":           "line1\r\nline2\r\n",
		"lf.txt":             "line1\nline2\n",
		"binary.bin":         "\x00\xff\x01\r\n\x1a",
		"empty.txt":          "",
		"深/路徑 with space.md": "unicode",
	}
	for p, c := range content {
		a.Write(t, p, c)
	}
	a.Sync(t)
	b.Sync(t)
	b.AssertFiles(t, content)
}
