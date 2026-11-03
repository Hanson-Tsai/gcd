package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/auth/authtest"
)

func check(t *testing.T, f *authtest.GitHub, repo, token string) error {
	t.Helper()
	r, err := auth.ParseRepo(repo)
	if err != nil {
		t.Fatal(err)
	}
	return f.Start(t).Check(context.Background(), r, token)
}

func assertMentions(t *testing.T, err error, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not mention %q", err, w)
		}
	}
}

var fullGrant = authtest.Grant{Repos: []string{"alice/notes"}, ContentsWrite: true, ActionsWrite: true}

func TestCheckAcceptsPrivateRepoWithWritableToken(t *testing.T) {
	f := &authtest.GitHub{
		Repos:  map[string]bool{"alice/notes": true},
		Tokens: map[string]authtest.Grant{"good": fullGrant},
	}
	if err := check(t, f, "alice/notes", "good"); err != nil {
		t.Fatalf("Check = %v, want nil", err)
	}
}

func TestCheckRefusesPublicRepo(t *testing.T) {
	f := &authtest.GitHub{
		Repos: map[string]bool{"alice/blog": false},
		Tokens: map[string]authtest.Grant{
			"granted":   {Repos: []string{"alice/blog"}, ContentsWrite: true, ActionsWrite: true},
			"ungranted": fullGrant,
		},
	}
	for _, tok := range []string{"granted", "ungranted"} {
		err := check(t, f, "alice/blog", tok)
		if !errors.Is(err, auth.ErrPublicRepo) {
			t.Fatalf("token %s: Check = %v, want ErrPublicRepo", tok, err)
		}
		assertMentions(t, err, "alice/blog", "public")
	}
}

func TestCheckExplainsMissingRepo(t *testing.T) {
	f := &authtest.GitHub{
		Repos:  map[string]bool{"alice/hidden": true},
		Tokens: map[string]authtest.Grant{"good": fullGrant},
	}
	// A repo that does not exist and one the token was not granted look the
	// same through the API.
	for _, repo := range []string{"alice/nope", "alice/hidden"} {
		err := check(t, f, repo, "good")
		if !errors.Is(err, auth.ErrRepoNotFound) {
			t.Fatalf("%s: Check = %v, want ErrRepoNotFound", repo, err)
		}
		assertMentions(t, err, repo, "https://github.com/new", "private", "Repository access")
	}
}

func TestCheckListsMissingPermissions(t *testing.T) {
	f := &authtest.GitHub{
		Repos:  map[string]bool{"alice/notes": true},
		Tokens: map[string]authtest.Grant{"readonly": {Repos: []string{"alice/notes"}}},
	}
	err := check(t, f, "alice/notes", "readonly")
	var mp *auth.MissingPermissionsError
	if !errors.As(err, &mp) {
		t.Fatalf("Check = %v, want MissingPermissionsError", err)
	}
	if want := []string{auth.PermContents, auth.PermActions}; strings.Join(mp.Missing, ",") != strings.Join(want, ",") {
		t.Errorf("Missing = %q, want %q", mp.Missing, want)
	}
	if !errors.Is(err, auth.ErrMissingPermissions) {
		t.Errorf("MissingPermissionsError does not match ErrMissingPermissions")
	}
	assertMentions(t, err, "Contents: read and write", "Actions: read and write",
		"https://github.com/settings/personal-access-tokens/new?", "contents=write", "workflows=write", "actions=write")
}

func TestCheckListsOnlyTheMissingPermission(t *testing.T) {
	f := &authtest.GitHub{
		Repos:  map[string]bool{"alice/notes": true},
		Tokens: map[string]authtest.Grant{"noactions": {Repos: []string{"alice/notes"}, ContentsWrite: true}},
	}
	err := check(t, f, "alice/notes", "noactions")
	var mp *auth.MissingPermissionsError
	if !errors.As(err, &mp) || len(mp.Missing) != 1 || mp.Missing[0] != auth.PermActions {
		t.Fatalf("Check = %v, want only %q missing", err, auth.PermActions)
	}
}

func TestCheckReportsRejectedToken(t *testing.T) {
	f := &authtest.GitHub{
		Repos:  map[string]bool{"alice/notes": true},
		Tokens: map[string]authtest.Grant{},
	}
	err := check(t, f, "alice/notes", "expired")
	if !errors.Is(err, auth.ErrTokenInvalid) {
		t.Fatalf("Check = %v, want ErrTokenInvalid", err)
	}
	assertMentions(t, err, "expired", "https://github.com/settings/personal-access-tokens", "gcd login alice/notes", auth.TokenEnv)
	if strings.Contains(err.Error(), "Bearer") {
		t.Errorf("error leaks auth header: %v", err)
	}
}

func TestCheckReportsRateLimitNotPermissions(t *testing.T) {
	f := &authtest.GitHub{
		Repos:       map[string]bool{"alice/notes": true},
		Tokens:      map[string]authtest.Grant{"good": fullGrant},
		RateLimited: true,
	}
	err := check(t, f, "alice/notes", "good")
	if err == nil || errors.Is(err, auth.ErrTokenInvalid) || errors.Is(err, auth.ErrMissingPermissions) {
		t.Fatalf("Check = %v, want a rate-limit error", err)
	}
	assertMentions(t, err, "rate limit")
}

func TestTokenHelpPointsToTokenSettings(t *testing.T) {
	help := auth.TokenHelp(auth.Repo{Owner: "alice", Name: "notes"})
	for _, w := range []string{"https://github.com/settings/personal-access-tokens/new?", "target_name=alice", "Contents", "Workflows", "Actions", "gcd login alice/notes", auth.TokenEnv} {
		if !strings.Contains(help, w) {
			t.Errorf("TokenHelp = %q, missing %q", help, w)
		}
	}
}
