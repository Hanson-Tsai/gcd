package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/cli"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/registry"
)

// syncBuffer is a bytes.Buffer safe to read while gcd watch writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWatchSyncsEveryDiskUntilInterrupted(t *testing.T) {
	cli.SetWatchTiming(t, 100*time.Millisecond, 200*time.Millisecond)
	notesRemote, docsRemote := fixture.NewRemote(t), fixture.NewRemote(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home")}
	b := machine{home: filepath.Join(root, "b-home"), folder: filepath.Join(root, "b-notes")}
	notes, docs := filepath.Join(root, "a-notes"), filepath.Join(root, "a-docs")
	a.gcd(t, "init", "--disk", "notes", notesRemote.URL, notes)
	a.gcd(t, "init", "--disk", "docs", docsRemote.URL, docs)
	b.gcd(t, "init", notesRemote.URL, b.folder)

	t.Setenv(registry.HomeEnv, a.home)
	ctx, interrupt := context.WithCancel(context.Background())
	defer interrupt()
	var out, errOut syncBuffer
	exit := make(chan int, 1)
	go func() { exit <- cli.Run(ctx, []string{"watch"}, &out, &errOut) }()
	waitUntil(t, "watch to start on both disks", func() bool {
		return strings.Contains(out.String(), "notes: watching") && strings.Contains(out.String(), "docs: watching")
	})

	writeFile(t, filepath.Join(notes, "n.md"), "note")
	writeFile(t, filepath.Join(docs, "sub", "d.md"), "doc")
	waitUntil(t, "both remotes to get the new files", func() bool {
		return notesRemote.Files(t)["n.md"] == "note" && docsRemote.Files(t)["sub/d.md"] == "doc"
	})

	writeFile(t, filepath.Join(b.folder, "from-b.md"), "hi")
	b.gcd(t, "sync")
	waitUntil(t, "a to download from-b.md", func() bool {
		got, err := os.ReadFile(filepath.Join(notes, "from-b.md"))
		return err == nil && string(got) == "hi"
	})

	interrupt() // Ctrl+C
	select {
	case code := <-exit:
		if code != 0 {
			t.Errorf("gcd watch exited %d after Ctrl+C, want 0; stderr: %s", code, errOut.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("gcd watch did not stop after Ctrl+C")
	}
	for _, want := range []string{"notes: up    + n.md", "docs: up    + sub/d.md", "notes: down  + from-b.md"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("watch output does not contain %q:\n%s", want, out.String())
		}
	}
}

func TestWatchWarnsInsteadOfMassDeleting(t *testing.T) {
	cli.SetWatchTiming(t, 100*time.Millisecond, 200*time.Millisecond)
	remote := fixture.NewRemote(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home"), folder: filepath.Join(root, "a-folder")}
	a.gcd(t, "init", remote.URL, a.folder)
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		writeFile(t, filepath.Join(a.folder, n+".md"), "x")
	}
	a.gcd(t, "sync")

	t.Setenv(registry.HomeEnv, a.home)
	ctx, interrupt := context.WithCancel(context.Background())
	defer interrupt()
	var out, errOut syncBuffer
	exit := make(chan int, 1)
	go func() { exit <- cli.Run(ctx, []string{"watch"}, &out, &errOut) }()
	waitUntil(t, "watch to start", func() bool { return strings.Contains(out.String(), "watching") })

	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		if err := os.Remove(filepath.Join(a.folder, n+".md")); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, "a mass-delete warning", func() bool { return strings.Contains(errOut.String(), "--yes") })
	interrupt()
	<-exit
	if n := len(remote.Files(t)); n < 8 {
		t.Errorf("remote has %d files, want the 8 files kept", n)
	}
	if msg := errOut.String(); !strings.Contains(msg, "gcd sync --disk a-folder --yes") {
		t.Errorf("warning %q does not say how to confirm", msg)
	}
}
