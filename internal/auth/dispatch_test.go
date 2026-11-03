package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/auth/authtest"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

// dispatchWorld is a fake GitHub with private repo alice/notes whose main
// branch has the compaction workflow, and alice/empty without it.
func dispatchWorld(t *testing.T) (*authtest.GitHub, auth.GitHub) {
	t.Helper()
	f := &authtest.GitHub{Tokens: map[string]authtest.Grant{
		"good":       {Repos: []string{"alice/notes", "alice/empty"}, ContentsWrite: true, ActionsWrite: true},
		"no-actions": {Repos: []string{"alice/notes"}, ContentsWrite: true},
	}}
	notes := &fixture.Remote{Dir: f.AddRepo(t, "alice/notes", true)}
	notes.Commit(t, map[string]string{".github/workflows/gcd-compact.yml": "on: workflow_dispatch\n"}, "install")
	f.AddRepo(t, "alice/empty", true)
	return f, f.Start(t)
}

var notesRepo = auth.Repo{Owner: "alice", Name: "notes"}

func TestDispatchWorkflowReturnsTheRunPage(t *testing.T) {
	f, gh := dispatchWorld(t)
	url, err := gh.DispatchWorkflow(context.Background(), notesRepo, "good", "gcd-compact.yml", "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := gh.WebURL + "/alice/notes/actions/runs/1"; url != want {
		t.Errorf("run page = %q, want %q", url, want)
	}
	want := []authtest.Dispatch{{Repo: "alice/notes", Workflow: "gcd-compact.yml", Ref: "main"}}
	if got := f.Dispatches(); len(got) != 1 || got[0] != want[0] {
		t.Errorf("dispatches = %+v, want %+v", got, want)
	}
}

// Without run details (older API behaviour: 204 No Content) the best link
// is the workflow's run list.
func TestDispatchWorkflowWithoutRunDetailsLinksTheWorkflow(t *testing.T) {
	f, gh := dispatchWorld(t)
	f.NoRunDetails = true
	url, err := gh.DispatchWorkflow(context.Background(), notesRepo, "good", "gcd-compact.yml", "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := gh.WebURL + "/alice/notes/actions/workflows/gcd-compact.yml"; url != want {
		t.Errorf("link = %q, want %q", url, want)
	}
	if len(f.Dispatches()) != 1 {
		t.Errorf("dispatches = %+v, want 1", f.Dispatches())
	}
}

func TestDispatchWorkflowErrors(t *testing.T) {
	f, gh := dispatchWorld(t)
	ctx := context.Background()

	_, err := gh.DispatchWorkflow(ctx, auth.Repo{Owner: "alice", Name: "empty"}, "good", "gcd-compact.yml", "main")
	if !errors.Is(err, auth.ErrWorkflowNotFound) {
		t.Errorf("missing workflow: %v, want ErrWorkflowNotFound", err)
	} else {
		assertMentions(t, err, "alice/empty", "gcd-compact.yml")
	}

	_, err = gh.DispatchWorkflow(ctx, notesRepo, "no-actions", "gcd-compact.yml", "main")
	var mp *auth.MissingPermissionsError
	if !errors.As(err, &mp) || len(mp.Missing) != 1 || mp.Missing[0] != auth.PermActions {
		t.Errorf("no Actions write: %v, want missing %s", err, auth.PermActions)
	}

	f.Revoke("good")
	if _, err = gh.DispatchWorkflow(ctx, notesRepo, "good", "gcd-compact.yml", "main"); !errors.Is(err, auth.ErrTokenInvalid) {
		t.Errorf("revoked token: %v, want ErrTokenInvalid", err)
	}
	if n := len(f.Dispatches()); n != 0 {
		t.Errorf("%d workflows ran, want none", n)
	}
}
