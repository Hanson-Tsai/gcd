package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/cli"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/registry"
)

type watchEvent struct {
	Time           string `json:"time"`
	Disk           string `json:"disk"`
	Event          string `json:"event"`
	Folder         string `json:"folder"`
	Path           string `json:"path"`
	Kind           string `json:"kind"`
	Message        string `json:"message"`
	PendingDeletes *int   `json:"pending_deletes"`
	jsonResult
}

// runningWatch is a `gcd watch` started in the background.
type runningWatch struct {
	out, errOut syncBuffer
	exit        chan int
	interrupt   context.CancelFunc
}

func startWatch(t *testing.T, m machine, args ...string) *runningWatch {
	t.Helper()
	t.Setenv(registry.HomeEnv, m.home)
	ctx, interrupt := context.WithCancel(context.Background())
	t.Cleanup(interrupt)
	w := &runningWatch{exit: make(chan int, 1), interrupt: interrupt}
	go func() { w.exit <- cli.Run(ctx, append([]string{"watch"}, args...), &w.out, &w.errOut) }()
	return w
}

// events parses stdout, failing on any line that is not a JSON event.
func (w *runningWatch) events(t *testing.T) []watchEvent {
	t.Helper()
	var evs []watchEvent
	for _, line := range strings.Split(strings.TrimSuffix(w.out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var ev watchEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("watch --json line %q is not JSON: %v", line, err)
		}
		if ev.Time == "" || ev.Disk == "" || ev.Event == "" {
			t.Fatalf("watch --json line %q lacks time, disk or event", line)
		}
		evs = append(evs, ev)
	}
	return evs
}

func (w *runningWatch) has(t *testing.T, match func(watchEvent) bool) bool {
	for _, ev := range w.events(t) {
		if match(ev) {
			return true
		}
	}
	return false
}

// wait returns the exit code once watch ends on its own.
func (w *runningWatch) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-w.exit:
		return code
	case <-time.After(15 * time.Second):
		t.Fatalf("gcd watch did not stop; stdout:\n%s", w.out.String())
		return -1
	}
}

// stop interrupts watch like Ctrl+C and returns the exit code.
func (w *runningWatch) stop(t *testing.T) int {
	t.Helper()
	w.interrupt()
	return w.wait(t)
}

func (w *runningWatch) running() bool {
	select {
	case code := <-w.exit:
		w.exit <- code
		return false
	default:
		return true
	}
}

func TestWatchDiskJSONWatchesOnlyThatDisk(t *testing.T) {
	cli.SetWatchTiming(t, 100*time.Millisecond, 200*time.Millisecond)
	notesRemote, docsRemote := fixture.NewRemote(t), fixture.NewRemote(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home")}
	notes, docs := filepath.Join(root, "notes"), filepath.Join(root, "docs")
	a.gcd(t, "init", notesRemote.URL, notes)
	a.gcd(t, "init", docsRemote.URL, docs)

	w := startWatch(t, a, "--json", "--disk", "notes")
	waitUntil(t, "watch to start", func() bool {
		return w.has(t, func(ev watchEvent) bool { return ev.Event == "watching" })
	})
	writeFile(t, filepath.Join(notes, "n.md"), "note")
	writeFile(t, filepath.Join(docs, "d.md"), "doc")
	waitUntil(t, "a synced event uploading n.md", func() bool {
		return w.has(t, func(ev watchEvent) bool {
			return ev.Event == "synced" && hasChange(ev.Uploaded, jsonChange{"n.md", "added"})
		})
	})
	time.Sleep(500 * time.Millisecond) // a few debounce and poll rounds
	if code := w.stop(t); code != 0 {
		t.Errorf("exit %d after Ctrl+C, want 0; stderr: %s", code, w.errOut.String())
	}

	if _, ok := docsRemote.Files(t)["d.md"]; ok {
		t.Error("watch --disk notes uploaded docs/d.md")
	}
	for _, ev := range w.events(t) {
		if ev.Disk != "notes" {
			t.Errorf("event for disk %q: %+v", ev.Disk, ev)
		}
		if ev.Event == "watching" && ev.Folder != notes {
			t.Errorf("watching folder = %q, want %q", ev.Folder, notes)
		}
		if ev.Event == "synced" && (ev.Commit == nil || ev.Uploaded == nil || ev.PendingDeletes == nil) {
			t.Errorf("synced event lacks the sync fields: %+v", ev)
		}
	}
	if s := w.errOut.String(); s != "" {
		t.Errorf("stderr = %q, want nothing with --json", s)
	}
}

func TestWatchUntilStopsWhenAnotherMachineUploadsThePath(t *testing.T) {
	cli.SetWatchTiming(t, 100*time.Millisecond, 200*time.Millisecond)
	remote := fixture.NewRemote(t)
	a, b := newMachine(t, "a"), newMachine(t, "b")
	a.gcd(t, "init", remote.URL, a.folder)
	b.gcd(t, "init", remote.URL, b.folder)
	writeFile(t, filepath.Join(a.folder, "results", "draft.md"), "x")

	t.Chdir(filepath.Join(a.folder, "results"))
	w := startWatch(t, a, "--json", "--until", "report.md")
	waitUntil(t, "watch to upload draft.md", func() bool { return remote.Files(t)["results/draft.md"] == "x" })

	// The same path written here is an upload: it does not end the wait.
	writeFile(t, filepath.Join(a.folder, "results", "report.md"), "mine")
	waitUntil(t, "watch to upload report.md", func() bool { return remote.Files(t)["results/report.md"] == "mine" })
	time.Sleep(500 * time.Millisecond)
	if !w.running() {
		t.Fatalf("watch --until stopped after its own upload; stdout:\n%s", w.out.String())
	}

	b.gcd(t, "sync")
	writeFile(t, filepath.Join(b.folder, "results", "report.md"), "theirs")
	b.gcd(t, "sync")

	if code := w.wait(t); code != 0 {
		t.Fatalf("watch --until exit %d, want 0; stderr: %s", code, w.errOut.String())
	}
	evs := w.events(t)
	last := evs[len(evs)-1]
	if last.Event != "until_reached" || last.Path != "results/report.md" || last.Kind != "modified" {
		t.Errorf("last event = %+v, want until_reached results/report.md modified", last)
	}
	prev := evs[len(evs)-2]
	if prev.Event != "synced" || !hasChange(prev.Downloaded, jsonChange{"results/report.md", "modified"}) {
		t.Errorf("event before until_reached = %+v, want the sync that downloaded it", prev)
	}
}

func TestWatchUntilCountsARemoteDelete(t *testing.T) {
	cli.SetWatchTiming(t, 100*time.Millisecond, 200*time.Millisecond)
	remote := fixture.NewRemote(t)
	a, b := newMachine(t, "a"), newMachine(t, "b")
	a.gcd(t, "init", remote.URL, a.folder)
	b.gcd(t, "init", remote.URL, b.folder)
	writeFile(t, filepath.Join(b.folder, "lock.txt"), "busy")
	writeFile(t, filepath.Join(b.folder, "keep.md"), "k")
	b.gcd(t, "sync")
	a.gcd(t, "sync")

	w := startWatch(t, a, "--until", filepath.Join(a.folder, "lock.txt"), "--disk", "a-folder")
	waitUntil(t, "watch to start", func() bool { return strings.Contains(w.out.String(), "watching") })
	if err := os.Remove(filepath.Join(b.folder, "lock.txt")); err != nil {
		t.Fatal(err)
	}
	b.gcd(t, "sync")

	if code := w.wait(t); code != 0 {
		t.Fatalf("watch --until exit %d, want 0; stderr: %s", code, w.errOut.String())
	}
	if out := w.out.String(); !strings.Contains(out, "lock.txt was downloaded (deleted); stopping") {
		t.Errorf("watch --until output does not end with the reached line:\n%s", out)
	}
}

func TestWatchUntilNeedsOneDisk(t *testing.T) {
	notesRemote, docsRemote := fixture.NewRemote(t), fixture.NewRemote(t)
	root := t.TempDir()
	a := machine{home: filepath.Join(root, "a-home")}
	a.gcd(t, "init", notesRemote.URL, filepath.Join(root, "notes"))
	a.gcd(t, "init", docsRemote.URL, filepath.Join(root, "docs"))
	t.Chdir(root)

	code, out, errOut := a.run(t, "watch", "--json", "--until", "x.md")
	if code != 2 || out != "" || !strings.Contains(errOut, "--disk") {
		t.Errorf("watch --until outside a disk: exit %d, stdout %q, stderr %q; want exit 2 asking for --disk", code, out, errOut)
	}

	var e jsonError
	if code := a.gcdJSON(t, &e, "watch", "--json", "--disk", "nope"); code != 1 || e.Error == nil || e.Error.Kind != "no_disk" {
		t.Errorf("watch --disk nope: exit %d, %+v; want a no_disk error", code, e.Error)
	}
}

func TestWatchJSONReportsARefusedMassDelete(t *testing.T) {
	cli.SetWatchTiming(t, 100*time.Millisecond, 200*time.Millisecond)
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		writeFile(t, filepath.Join(a.folder, n+".md"), "x")
	}
	a.gcd(t, "sync")

	w := startWatch(t, a, "--json", "--disk", "a-folder")
	waitUntil(t, "watch to start", func() bool { return strings.Contains(w.out.String(), "watching") })
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		if err := os.Remove(filepath.Join(a.folder, n+".md")); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, "a needs_confirm event", func() bool {
		return w.has(t, func(ev watchEvent) bool {
			return ev.Event == "needs_confirm" && ev.PendingDeletes != nil && *ev.PendingDeletes == 8 &&
				strings.Contains(ev.Message, "gcd sync --disk a-folder --yes")
		})
	})
	w.stop(t)
	if n := len(remote.Files(t)); n < 8 {
		t.Errorf("remote has %d files, want the 8 files kept", n)
	}
}
