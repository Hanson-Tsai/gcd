package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/registry"
	"github.com/Hanson-Tsai/gcd/internal/watch"
)

func init() {
	register(command{
		name:    "watch",
		usage:   "watch",
		summary: "keep every disk in sync until Ctrl+C",
		run:     runWatch,
	})
}

// Test seams (export_watch_test.go); zero means "from the repo config".
var watchDebounce, watchPoll time.Duration

func runWatch(ctx context.Context, e *env, args []string) error {
	fs := newFlags(e, "watch")
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
	if len(reg.Disks) == 0 {
		return errors.New("no disk registered; run `gcd init <git-url> <folder>` first")
	}

	p := &watchPrinter{e: e, folders: map[string]string{}, rejected: map[string]map[string]bool{}}
	var targets []watch.Target
	for _, ent := range reg.Disks {
		d, err := openEntry(ent)
		if err != nil {
			p.line(e.stderr, ent.Name, "not watched: %v", explain(err))
			continue
		}
		targets = append(targets, watch.Target{Name: ent.Name, Disk: d})
		p.folders[ent.Name] = ent.Folder
	}
	if len(targets) == 0 {
		return errors.New("no disk could be opened")
	}

	stopping := make(chan struct{})
	stopNote := context.AfterFunc(ctx, func() {
		defer close(stopping)
		p.printf(e.stderr, "stopping once running syncs finish (Ctrl+C again to abort)\n")
	})
	err = watch.Run(ctx, targets, watch.Options{Debounce: watchDebounce, Poll: watchPoll, Report: p.event})
	if !stopNote() {
		<-stopping
	}
	return err
}

// watchPrinter prints watch events as timestamped lines.
type watchPrinter struct {
	e       *env
	folders map[string]string // disk name -> folder
	// rejected: disk name -> rejections the last sync reported (printed)
	rejected map[string]map[string]bool

	mu sync.Mutex
}

func (p *watchPrinter) printf(w io.Writer, format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(w, time.Now().Format("15:04:05")+" "+format, a...)
}

func (p *watchPrinter) line(w io.Writer, name, format string, a ...any) {
	p.printf(w, "%s: "+format+"\n", append([]any{name}, a...)...)
}

func (p *watchPrinter) event(ev watch.Event) {
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
