package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
	"github.com/Hanson-Tsai/gcd/internal/registry"
	"github.com/Hanson-Tsai/gcd/internal/scan"
)

// The --json output (docs/json.md). Fields are only ever added, never
// renamed, removed or retyped: old Claude Code plugins read new gcd output.
// Keep docs/json.md in step with every change here.

// jsonFlag adds --json to fs, stored in e.json.
func jsonFlag(fs *flag.FlagSet, e *env) {
	fs.BoolVar(&e.json, "json", false, "print one JSON object instead of text (see docs/json.md)")
}

// wantsJSON reports whether args (after the command name) ask for --json, for
// errors that happen before the flags are parsed.
func wantsJSON(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "--json", "-json", "--json=true", "-json=true":
			return true
		}
	}
	return false
}

// writeJSON prints v as one line of JSON.
func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// Error kinds: a stable contract. Add values, never rename one; readers treat
// unknown kinds as kindOther.
const (
	kindNeedsConfirm = "needs_confirm"  // the sync deletes more files than the repo config allows
	kindAuth         = "auth"           // the token is missing, expired, revoked or lacks permissions
	kindBusy         = "busy"           // another gcd is syncing the Disk
	kindNoDisk       = "no_disk"        // no Disk here, or no Disk by that name
	kindFormatTooNew = "format_too_new" // the repo format is newer than this gcd
	kindExists       = "exists"         // restore: a different file is in the way (needs --force)
	kindOther        = "other"
)

// errorKind classifies err for the --json error object.
func errorKind(err error) string {
	switch {
	case errors.Is(err, disk.ErrMassDelete):
		return kindNeedsConfirm
	case errors.Is(err, disk.ErrAlreadySyncing):
		return kindBusy
	case errors.Is(err, gitstore.ErrAuth), errors.Is(err, gitstore.ErrWorkflowsPermission),
		errors.Is(err, auth.ErrNoToken), errors.Is(err, auth.ErrTokenInvalid),
		errors.Is(err, auth.ErrMissingPermissions):
		return kindAuth
	case errors.Is(err, registry.ErrNoDisk):
		return kindNoDisk
	case errors.Is(err, config.ErrFormatTooNew):
		return kindFormatTooNew
	case errors.Is(err, disk.ErrExists):
		return kindExists
	}
	return kindOther
}

// confirmError is a sync refused for a mass delete, with the count.
type confirmError struct {
	err     error
	pending int
}

func (e *confirmError) Error() string { return e.err.Error() }
func (e *confirmError) Unwrap() error { return e.err }

type errorJSON struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	// PendingDeletes is set for kindNeedsConfirm only.
	PendingDeletes *int `json:"pending_deletes,omitempty"`
}

// newErrorBody builds the error object for err (already explained).
func newErrorBody(err error) errorBody {
	b := errorBody{Kind: errorKind(err), Message: err.Error()}
	var ce *confirmError
	if errors.As(err, &ce) {
		n := ce.pending
		b.PendingDeletes = &n
	}
	return b
}

type changeJSON struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // added, modified, deleted
}

type conflictJSON struct {
	Path string `json:"path"`
	Copy string `json:"copy"`
}

type rejectedJSON struct {
	Path   string `json:"path"`
	Reason string `json:"reason"` // see rejectReason
	Detail string `json:"detail"` // human-readable explanation
	Remote bool   `json:"remote"` // the path exists only on the remote
}

// resultJSON is the object of `status --json` and `sync --json`, and the
// fields of a watch "synced" event.
type resultJSON struct {
	Disk           string         `json:"disk"`
	Uploaded       []changeJSON   `json:"uploaded"`
	Downloaded     []changeJSON   `json:"downloaded"`
	Conflicts      []conflictJSON `json:"conflicts"`
	Skipped        []string       `json:"skipped"`
	Rejected       []rejectedJSON `json:"rejected"`
	NeedsConfirm   bool           `json:"needs_confirm"`
	PendingDeletes int            `json:"pending_deletes"`
	// Commit: sync only ("" when nothing was pushed).
	Commit *string `json:"commit,omitempty"`
	// WorkflowVersion: status only ("" when the repo has no workflow).
	WorkflowVersion *string `json:"workflow_version,omitempty"`
}

func newResultJSON(name string, res disk.Result) *resultJSON {
	r := &resultJSON{
		Disk:           name,
		Uploaded:       changesJSON(res.Uploaded),
		Downloaded:     changesJSON(res.Downloaded),
		Conflicts:      []conflictJSON{},
		Skipped:        []string{},
		Rejected:       []rejectedJSON{},
		NeedsConfirm:   res.NeedsConfirm,
		PendingDeletes: res.PendingDeletes,
	}
	for _, c := range res.Conflicts {
		r.Conflicts = append(r.Conflicts, conflictJSON{Path: c.Path, Copy: c.Copy})
	}
	r.Skipped = append(r.Skipped, res.Skipped...)
	for _, rj := range res.Rejected {
		r.Rejected = append(r.Rejected, rejectedJSON{Path: rj.Path, Reason: rejectReason(rj.Reason), Detail: rj.Detail, Remote: rj.Remote})
	}
	return r
}

func changesJSON(changes []disk.Change) []changeJSON {
	out := []changeJSON{}
	for _, c := range changes {
		out = append(out, changeJSON{Path: c.Path, Kind: c.Kind.String()})
	}
	return out
}

// rejectReason is the stable name of a scan.Reason.
func rejectReason(r scan.Reason) string {
	switch r {
	case scan.BadChar:
		return "bad_char"
	case scan.ReservedName:
		return "reserved_name"
	case scan.BadEnding:
		return "bad_ending"
	case scan.CaseCollision:
		return "case_collision"
	case scan.TooLarge:
		return "too_large"
	case scan.NotRegular:
		return "not_regular"
	}
	return strings.ReplaceAll(r.String(), " ", "_")
}

// jsonTime formats t as RFC 3339 in UTC.
func jsonTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func ptr[T any](v T) *T { return &v }
