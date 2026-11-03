package auth_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/auth"
)

// TestE2ECheckAgainstGitHub runs auth.GitHub.Check against api.github.com.
// It runs only when triggered by hand (the E2E workflow or a developer):
//
//	GCD_E2E_REPO           private throwaway repo, owner/repo (required)
//	GCD_TOKEN              fine-grained PAT with Contents, Workflows and
//	                       Actions read/write on it (required)
//	GCD_E2E_READONLY_TOKEN PAT with only Contents: read on it (optional)
//	GCD_E2E_PUBLIC_REPO    a public repo (default octocat/Hello-World)
func TestE2ECheckAgainstGitHub(t *testing.T) {
	name, token := os.Getenv("GCD_E2E_REPO"), os.Getenv(auth.TokenEnv)
	if name == "" || token == "" {
		t.Skip("set GCD_E2E_REPO=<owner/repo> and GCD_TOKEN to run against real GitHub")
	}
	repo, err := auth.ParseRepo(name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	gh := auth.GitHub{}

	t.Run("private repo with full token", func(t *testing.T) {
		if _, err := gh.Check(ctx, repo, token); err != nil {
			t.Fatalf("Check = %v", err)
		}
	})
	t.Run("public repo", func(t *testing.T) {
		pub := os.Getenv("GCD_E2E_PUBLIC_REPO")
		if pub == "" {
			pub = "octocat/Hello-World"
		}
		r, err := auth.ParseRepo(pub)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gh.Check(ctx, r, token); !errors.Is(err, auth.ErrPublicRepo) {
			t.Fatalf("Check = %v, want ErrPublicRepo", err)
		}
	})
	t.Run("missing repo", func(t *testing.T) {
		r := auth.Repo{Owner: repo.Owner, Name: fmt.Sprintf("gcd-e2e-missing-%d", time.Now().UnixNano())}
		if _, err := gh.Check(ctx, r, token); !errors.Is(err, auth.ErrRepoNotFound) {
			t.Fatalf("Check = %v, want ErrRepoNotFound", err)
		}
	})
	t.Run("invalid token", func(t *testing.T) {
		_, err := gh.Check(ctx, repo, "github_pat_11AAAAAAA0invalidinvalidinvalid")
		if !errors.Is(err, auth.ErrTokenInvalid) {
			t.Fatalf("Check = %v, want ErrTokenInvalid", err)
		}
	})
	t.Run("read-only token", func(t *testing.T) {
		ro := os.Getenv("GCD_E2E_READONLY_TOKEN")
		if ro == "" {
			t.Skip("set GCD_E2E_READONLY_TOKEN (Contents: read only) to run")
		}
		var mp *auth.MissingPermissionsError
		if _, err := gh.Check(ctx, repo, ro); !errors.As(err, &mp) || !slices.Contains(mp.Missing, auth.PermContents) {
			t.Fatalf("Check = %v, want Contents listed as missing", err)
		}
	})
}
