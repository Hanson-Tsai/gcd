package auth_test

import (
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/auth"
)

func TestParseRepoResolvesToGitHubHTTPSURL(t *testing.T) {
	for _, in := range []string{
		"Hanson-Tsai/my.notes_1",
		"https://github.com/Hanson-Tsai/my.notes_1",
		"https://github.com/Hanson-Tsai/my.notes_1.git",
	} {
		r, err := auth.ParseRepo(in)
		if err != nil {
			t.Fatalf("ParseRepo(%q): %v", in, err)
		}
		if got, want := r.String(), "Hanson-Tsai/my.notes_1"; got != want {
			t.Errorf("ParseRepo(%q).String() = %q, want %q", in, got, want)
		}
		if got, want := (auth.GitHub{}).RemoteURL(r), "https://github.com/Hanson-Tsai/my.notes_1.git"; got != want {
			t.Errorf("RemoteURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoteURLRoundTrips(t *testing.T) {
	r := auth.Repo{Owner: "alice", Name: "notes"}
	for _, gh := range []auth.GitHub{{}, {WebURL: "http://127.0.0.1:8080"}} {
		u := gh.RemoteURL(r)
		got, ok := gh.RepoOf(u)
		if !ok || got != r {
			t.Errorf("RepoOf(RemoteURL) = %v, %v for %q", got, ok, u)
		}
	}
	if u := (auth.GitHub{}).RemoteURL(r); u != "https://github.com/alice/notes.git" {
		t.Errorf("RemoteURL = %q", u)
	}
	if _, ok := (auth.GitHub{}).RepoOf("file:///tmp/remote.git"); ok {
		t.Error("RepoOf accepted a file:// remote")
	}
}

func TestParseRepoRejectsMalformed(t *testing.T) {
	for _, in := range []string{
		"", "owner", "owner/", "/repo", "owner/repo/extra", "own er/repo",
		"owner/..", "-owner/repo", "file:///tmp/remote.git", "https://gitlab.com/o/r",
	} {
		if r, err := auth.ParseRepo(in); err == nil {
			t.Errorf("ParseRepo(%q) = %v, want error", in, r)
		}
	}
}
