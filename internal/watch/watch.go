// Package watch keeps Disks in sync continuously (`gcd watch`): it syncs a
// Disk once local files have stopped changing for the debounce time, and
// whenever a cheap ls-remote poll shows the remote moved.
//
// Each Disk runs in its own goroutine, so one Disk failing never stops the
// others:
//
//	start: sync once (catch up on changes made while not watching)
//	file event         -> (re)arm the debounce timer
//	debounce fires     -> sync if the folder differs from what the last sync saw
//	poll tick          -> sync if the last sync failed or the remote moved;
//	                      else arm the debounce timer if the folder changed
//	                      unnoticed (missed events, no file watching)
//
// "Differs from what the last sync saw" compares stat snapshots (size and
// mtime), so the events caused by gcd's own downloads do not set off another
// sync. Debounce and poll interval come from the repo config unless Options
// overrides them.
package watch

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/disk"
)

// Target is one Disk to watch.
type Target struct {
	Name string // shown in events
	Disk *disk.Disk
}

// Options tunes Run. Zero durations mean "from the repo config".
type Options struct {
	Debounce time.Duration // quiet time after the last local change before syncing
	Poll     time.Duration // interval between remote checks and fallback rescans
	// Report receives every event. Calls are serialized; nil drops events.
	Report func(Event)
}

// Kind classifies an Event.
type Kind int

const (
	// Watching: the Disk is being watched; its first sync follows.
	Watching Kind = iota + 1
	// Synced: a sync finished; Result says what it did.
	Synced
	// NeedsConfirm: a sync stopped because it would delete many files
	// (Err wraps disk.ErrMassDelete). Watch never confirms; the user runs
	// `gcd sync --yes`. It is not retried until the folder or remote changes.
	NeedsConfirm
	// Busy: another gcd process is syncing the Disk (disk.ErrAlreadySyncing);
	// watch tries again after the debounce time, reporting Busy only once.
	Busy
	// Failed: a sync or remote check failed (Err); retried every poll
	// interval. Repeats of the same error are reported once.
	Failed
	// Warning: watching works only partly (Err), e.g. the OS file watch
	// limit was hit; changes are still found by the poll-interval rescan.
	Warning
)

func (k Kind) String() string {
	switch k {
	case Watching:
		return "watching"
	case Synced:
		return "synced"
	case NeedsConfirm:
		return "needs-confirm"
	case Busy:
		return "busy"
	case Failed:
		return "failed"
	case Warning:
		return "warning"
	}
	return "unknown"
}

// Event is something that happened to one watched Disk.
type Event struct {
	Disk   string // Target.Name
	Kind   Kind
	Result disk.Result // for Synced and NeedsConfirm
	Err    error       // for NeedsConfirm, Busy, Failed and Warning
}

// Run watches every target until ctx is cancelled, then waits for syncs in
// progress to finish (they are never cut off by ctx) and returns nil.
func Run(ctx context.Context, targets []Target, opts Options) error {
	if len(targets) == 0 {
		return errors.New("watch: no disks to watch")
	}
	var mu sync.Mutex
	report := func(e Event) {
		if opts.Report == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		opts.Report(e)
	}
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			(&worker{target: t, opts: opts, report: report}).run(ctx)
		}()
	}
	wg.Wait()
	return nil
}

// worker watches one Disk.
type worker struct {
	target Target
	opts   Options
	report func(Event)

	// accepted is the folder state the last successful sync accounted for
	// (nil: none yet). A folder that still matches it has nothing to upload.
	accepted snapshot
	// blocked is the folder state a mass-delete refusal saw; the same state
	// is not retried.
	blocked snapshot
	retry   bool   // the last sync failed: try again
	busy    bool   // ... because another process was syncing
	lastErr string // last Failed error reported, to report repeats once
}

func (w *worker) run(ctx context.Context) {
	w.report(Event{Disk: w.target.Name, Kind: Watching})
	fw, err := newFolderWatcher(w.target.Disk.Folder())
	defer fw.close()
	if err != nil {
		w.warn(err)
	}

	w.sync(ctx)
	debounce, poll := w.timing(ctx) // after the first fetch
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	quiet := time.NewTimer(debounce)
	defer quiet.Stop()
	quiet.Stop()
	armed := false // quiet is running
	arm := func() { quiet.Reset(debounce); armed = true }

	for {
		var quietC <-chan time.Time
		if armed {
			quietC = quiet.C
		}
		synced := false
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-fw.events():
			if !ok {
				continue
			}
			relevant, err := fw.handle(ev)
			if err != nil {
				w.warn(err)
			}
			if relevant {
				arm()
			}
		case err, ok := <-fw.errors():
			if !ok {
				continue
			}
			w.warn(err) // e.g. event overflow: assume something changed
			arm()
		case <-quietC:
			armed = false
			if w.retry || w.localChanged() {
				w.sync(ctx)
				synced = true
			}
		case <-ticker.C:
			switch {
			case w.retry || w.remoteChanged(ctx):
				w.sync(ctx)
				synced = true
			case !armed && w.localChanged():
				arm()
			}
		}
		if synced {
			if d, p := w.timing(ctx); d != debounce || p != poll {
				debounce, poll = d, p
				ticker.Reset(poll)
			}
			if w.busy && !armed {
				arm() // another process holds the lock: look again soon
			}
		}
	}
}

// sync runs one sync and records its outcome. The sync is not cancelled with
// ctx: Ctrl+C lets it finish (a second Ctrl+C kills the process, which an
// interrupted sync also survives).
func (w *worker) sync(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	folder := w.target.Disk.Folder()
	before, beforeErr := takeSnapshot(folder)
	res, err := w.target.Disk.Sync(context.WithoutCancel(ctx), disk.SyncOptions{})
	wasBusy := w.busy
	w.busy = false
	switch {
	case err == nil:
		after, afterErr := takeSnapshot(folder)
		w.accepted = nil
		if beforeErr == nil && afterErr == nil {
			w.accepted = settle(before, after, res)
		}
		w.retry, w.blocked, w.lastErr = false, nil, ""
		w.report(Event{Disk: w.target.Name, Kind: Synced, Result: res})
	case res.NeedsConfirm || errors.Is(err, disk.ErrMassDelete):
		w.retry, w.blocked, w.lastErr = false, before, ""
		w.report(Event{Disk: w.target.Name, Kind: NeedsConfirm, Result: res, Err: err})
	case errors.Is(err, disk.ErrAlreadySyncing):
		w.retry, w.busy = true, true
		if !wasBusy {
			w.report(Event{Disk: w.target.Name, Kind: Busy, Err: err})
		}
	default:
		w.retry = true
		w.fail(err)
	}
}

// localChanged reports whether the folder differs from what the last sync
// accounted for (and from a state already refused as a mass delete).
func (w *worker) localChanged() bool {
	cur, err := takeSnapshot(w.target.Disk.Folder())
	if err != nil {
		return true // let a sync report it
	}
	if w.blocked != nil && maps.Equal(cur, w.blocked) {
		return false
	}
	return w.accepted == nil || !maps.Equal(cur, w.accepted)
}

// remoteChanged asks the remote whether it moved since the last fetch.
func (w *worker) remoteChanged(ctx context.Context) bool {
	changed, err := w.target.Disk.RemoteChanged(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.fail(err)
		}
		return false
	}
	return changed
}

// timing returns the debounce and poll interval: Options, else the repo
// config as of the last fetch (its defaults when that cannot be read; the
// next sync reports why).
func (w *worker) timing(ctx context.Context) (debounce, poll time.Duration) {
	debounce, poll = w.opts.Debounce, w.opts.Poll
	if debounce > 0 && poll > 0 {
		return debounce, poll
	}
	cfg, err := w.target.Disk.KnownRepoConfig(ctx)
	if err != nil {
		cfg = config.Default()
	}
	if debounce <= 0 {
		debounce = cfg.Debounce
	}
	if poll <= 0 {
		poll = cfg.PollInterval
	}
	return debounce, poll
}

func (w *worker) fail(err error) {
	if err.Error() == w.lastErr {
		return
	}
	w.lastErr = err.Error()
	w.report(Event{Disk: w.target.Name, Kind: Failed, Err: err})
}

func (w *worker) warn(err error) {
	w.report(Event{Disk: w.target.Name, Kind: Warning, Err: err})
}
