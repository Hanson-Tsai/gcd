package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/auth"
	"github.com/Hanson-Tsai/gcd/internal/auth/authtest"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

var notes = auth.Repo{Owner: "alice", Name: "notes"}

func TestTokenFromEnvironmentWinsOverKeychain(t *testing.T) {
	ts := auth.Tokens{Keyring: &authtest.Keyring{}, Getenv: env(map[string]string{auth.TokenEnv: "env-token"})}
	if err := ts.Store(notes, "keychain-token"); err != nil {
		t.Fatal(err)
	}
	tok, err := ts.Lookup(notes)
	if err != nil || tok != "env-token" {
		t.Fatalf("Lookup = %q, %v; want env-token", tok, err)
	}
}

func TestTokenFromKeychainPerRepo(t *testing.T) {
	ts := auth.Tokens{Keyring: &authtest.Keyring{}, Getenv: env(nil)}
	if err := ts.Store(notes, "notes-token"); err != nil {
		t.Fatal(err)
	}
	if err := ts.Store(auth.Repo{Owner: "alice", Name: "photos"}, "photos-token"); err != nil {
		t.Fatal(err)
	}
	tok, err := ts.Lookup(notes)
	if err != nil || tok != "notes-token" {
		t.Fatalf("Lookup = %q, %v; want notes-token", tok, err)
	}
}

func TestNoTokenExplainsHowToProvideOne(t *testing.T) {
	ts := auth.Tokens{Keyring: &authtest.Keyring{}, Getenv: env(nil)}
	_, err := ts.Lookup(notes)
	if !errors.Is(err, auth.ErrNoToken) {
		t.Fatalf("Lookup err = %v, want ErrNoToken", err)
	}
	assertMentions(t, err, auth.TokenEnv, "gcd login alice/notes")
}

func TestBrokenKeychainSuggestsEnvironment(t *testing.T) {
	ts := auth.Tokens{Keyring: &authtest.Keyring{Err: errors.New("dbus: no session bus")}, Getenv: env(nil)}
	_, err := ts.Lookup(notes)
	if err == nil || errors.Is(err, auth.ErrNoToken) {
		t.Fatalf("Lookup err = %v, want a keychain error", err)
	}
	if !strings.Contains(err.Error(), auth.TokenEnv) {
		t.Errorf("error %q does not suggest %s", err, auth.TokenEnv)
	}
}
