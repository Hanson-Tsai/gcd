package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/registry"
	"github.com/Hanson-Tsai/gcd/internal/watch"
)

func init() {
	register(command{
		name:    "watch",
		usage:   "watch [--disk <name>] [--until <path>] [--json]",
		summary: "keep every disk (or one) in sync until Ctrl+C",
		run:     runWatch,
		json:    true,
	})
}

// Test seams (export_watch_test.go); zero means "from the repo config".
var watchDebounce, watchPoll time.Duration

func runWatch(ctx context.Context, e *env, args []string) error {
	fs := newDiskFlags(e, "watch")
	jsonFlag(fs, e)
	until := fs.String("until", "", "stop once a sync downloads this `path` (added, modified or deleted); needs one disk: the current one or --disk")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errUsage
	}
	home, err := registry.Home()
	if err != nil {
		return err
	}
	reg, err := registry.Load(home)
	if err != nil {
		return err
	}
	if err := reg.CheckAny(); err != nil {
		return err
	}
	entries := reg.Disks
	if e.disk != "" {
		ent, err := reg.Named(e.disk)
		if err != nil {
			return err
		}
		entries = []registry.Entry{ent}
	}
	var untilPath string
	if *until != "" {
		if e.disk == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			ent, ok := reg.Containing(cwd)
			if !ok {
				fmt.Fprintln(e.stderr, "gcd: --until needs one disk: run it inside a disk folder or pass --disk <name>")
				return errUsage
			}
			entries = []registry.Entry{ent}
		}
		if untilPath, err = folderPath(entries[0].Folder, *until); err != nil {
			return err
		}
	}

	p := &watchPrinter{e: e, folders: map[string]string{}, rejected: map[string]map[string]bool{}, until: untilPath}
	var targets []watch.Target
	for _, ent := range entries {
		d, err := openEntry(ent)
		if err != nil {
			if e.disk != "" || untilPath != "" {
				return err // the one disk asked for
			}
			p.failed(ent.Name, explain(err))
			continue
		}
		targets = append(targets, watch.Target{Name: ent.Name, Disk: d})
		p.folders[ent.Name] = ent.Folder
	}
	if len(targets) == 0 {
		return errors.New("no disk could be opened")
	}

	// runCtx ends the watch: Ctrl+C (ctx) or --until reached (p.stop).
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	p.stop = stop
	stopping := make(chan struct{})
	stopNote := context.AfterFunc(ctx, func() {
		defer close(stopping)
		if !e.json {
			p.printf(e.stderr, "stopping once running syncs finish (Ctrl+C again to abort)\n")
		}
	})
	err = watch.Run(runCtx, targets, watch.Options{Debounce: watchDebounce, Poll: watchPoll, Report: p.event})
	if !stopNote() {
		<-stopping
	}
	return err
}

// watchPrinter prints watch events as timestamped lines, or with --json as
// one JSON object per line (watchEventJSON).
type watchPrinter struct {
	e       *env
	folders map[string]string // disk name -> folder
	// rejected: disk name -> rejections the last sync reported (printed)
	rejected map[string]map[string]bool

	until string // --until path ("" = none)
	stop  func() // ends the watch once until is reached
	done  bool   // until reached: print nothing more

	mu sync.Mutex
}

// watchEventJSON is one line of `watch --json`. The fields of a "synced"
// event are those of `sync --json` (resultJSON).
type watchEventJSON struct {
	Time  string `json:"time"`
	Disk  string `json:"disk"`
	Event string `json:"event"`
	*resultJSON
	Folder         string `json:"folder,omitempty"`  // watching
	Path           string `json:"path,omitempty"`    // until_reached
	Kind           string `json:"kind,omitempty"`    // error: error kind; until_reached: change kind
	Message        string `json:"message,omitempty"` // needs_confirm, error, warning
	PendingDeletes *int   `json:"pending_deletes,omitempty"`
}

// Watch event names (stable, like the error kinds).
const (
	evWatching     = "watching"
	evSynced       = "synced"
	evNeedsConfirm = "needs_confirm"
	evBusy         = "busy"
	evError        = "error"
	evWarning      = "warning"
	evUntilReached = "until_reached"
)

func (p *watchPrinter) emit(ev watchEventJSON) {
	ev.Time = jsonTime(time.Now())
	_ = writeJSON(p.e.stdout, ev)
}

func (p *watchPrinter) printf(w io.Writer, format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(w, time.Now().Format("15:04:05")+" "+format, a...)
}

func (p *watchPrinter) line(w io.Writer, name, format string, a ...any) {
	p.printf(w, "%s: "+format+"\n", append([]any{name}, a...)...)
}

// failed reports a Disk that could not be opened (and is not watched).
func (p *watchPrinter) failed(name string, err error) {
	if p.e.json {
		p.emit(watchEventJSON{Disk: name, Event: evError, Kind: errorKind(err), Message: "not watched: " + err.Error()})
		return
	}
	p.line(p.e.stderr, name, "not watched: %v", err)
}

func (p *watchPrinter) event(ev watch.Event) {
	if p.done {
		return
	}
	if p.e.json {
		p.jsonEvent(ev)
	} else {
		p.textEvent(ev)
	}
	if ev.Kind == watch.Synced && p.until != "" {
		if c, ok := untilChange(ev.Result.Downloaded, p.until); ok {
			p.done = true
			if p.e.json {
				p.emit(watchEventJSON{Disk: ev.Disk, Event: evUntilReached, Path: c.Path, Kind: c.Kind.String()})
			} else {
				p.line(p.e.stdout, ev.Disk, "%s was downloaded (%s); stopping", c.Path, c.Kind)
			}
			p.stop()
		}
	}
}

// untilChange finds the download of path, or of a file below it when path
// is a folder.
func untilChange(downloaded []disk.Change, path string) (disk.Change, bool) {
	for _, c := range downloaded {
		if c.Path == path || strings.HasPrefix(c.Path, path+"/") {
			return c, true
		}
	}
	return disk.Change{}, false
}

func (p *watchPrinter) jsonEvent(ev watch.Event) {
	name := ev.Disk
	switch ev.Kind {
	case watch.Watching:
		p.emit(watchEventJSON{Disk: name, Event: evWatching, Folder: p.folders[name]})
	case watch.Synced:
		r := syncedJSON(name, ev.Result)
		// The outer pending_deletes hides the embedded one: carry it over.
		p.emit(watchEventJSON{Disk: name, Event: evSynced, resultJSON: r, PendingDeletes: &r.PendingDeletes})
	case watch.NeedsConfirm:
		n := ev.Result.PendingDeletes
		msg := fmt.Sprintf("%v; nothing was synced; if these deletions are intended, run `gcd sync --disk %s --yes`", ev.Err, name)
		p.emit(watchEventJSON{Disk: name, Event: evNeedsConfirm, Message: msg, PendingDeletes: &n})
	case watch.Busy:
		p.emit(watchEventJSON{Disk: name, Event: evBusy, Message: "another gcd is syncing this disk; trying again shortly"})
	case watch.Failed:
		err := explain(ev.Err)
		p.emit(watchEventJSON{Disk: name, Event: evError, Kind: errorKind(err), Message: err.Error()})
	case watch.Warning:
		p.emit(watchEventJSON{Disk: name, Event: evWarning, Message: ev.Err.Error()})
	}
}

// syncedJSON is the sync object of a "synced" event.
func syncedJSON(name string, res disk.Result) *resultJSON {
	r := newResultJSON(name, res)
	r.Commit = ptr(res.Commit)
	return r
}

func (p *watchPrinter) textEvent(ev watch.Event) {
	out, errOut, name := p.e.stdout, p.e.stderr, ev.Disk
	switch ev.Kind {
	case watch.Watching:
		p.line(out, name, "watching %s", p.folders[name])
	case watch.Synced:
		res := ev.Result
		for _, c := range res.Uploaded {
			p.line(out, name, "up    %s %s", sign(c.Kind), c.Path)
		}
		for _, c := range res.Downloaded {
			p.line(out, name, "down  %s %s", sign(c.Kind), c.Path)
		}
		for _, c := range res.Conflicts {
			p.line(out, name, "conflict %s: local version kept as %s", c.Path, c.Copy)
		}
		for _, s := range res.Skipped {
			p.line(out, name, "skipped %s: changed during sync, next sync picks it up", s)
		}
		// Every sync lists every rejected file; show each one once.
		seen := map[string]bool{}
		for _, r := range res.Rejected {
			msg := r.String()
			seen[msg] = true
			if !p.rejected[name][msg] {
				p.line(out, name, "not synced %s", msg)
			}
		}
		p.rejected[name] = seen
	case watch.NeedsConfirm:
		p.line(errOut, name, "warning: %v\n  nothing was synced; if these deletions are intended, run `gcd sync --disk %s --yes`", ev.Err, name)
	case watch.Busy:
		p.line(out, name, "another gcd is syncing this disk; trying again shortly")
	case watch.Failed:
		p.line(errOut, name, "error (retrying): %v", explain(ev.Err))
	case watch.Warning:
		p.line(errOut, name, "warning: %v", ev.Err)
	}
}
