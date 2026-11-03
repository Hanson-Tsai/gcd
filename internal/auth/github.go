package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/gitstore"
)

// Fine-grained PAT permissions gcd needs on the Disk's repo, as GitHub's
// token settings page names them. Metadata: read is implicit.
const (
	PermContents  = "Contents: read and write"  // sync (git fetch/push)
	PermWorkflows = "Workflows: read and write" // install/upgrade the compaction workflow
	PermActions   = "Actions: read and write"   // `gcd compact` (workflow_dispatch)
)

var (
	// ErrTokenInvalid: GitHub rejected the token (expired, revoked or
	// malformed).
	ErrTokenInvalid = errors.New("GitHub rejected the token")
	// ErrRepoNotFound: the repo does not exist or the token cannot see it.
	ErrRepoNotFound = errors.New("repository not found")
	// ErrPublicRepo: gcd only binds private repos.
	ErrPublicRepo = errors.New("repository is public")
	// ErrMissingPermissions matches *MissingPermissionsError.
	ErrMissingPermissions = errors.New("token is missing permissions")
)

// MissingPermissionsError lists the fine-grained permissions the token lacks.
type MissingPermissionsError struct {
	Repo    Repo
	Missing []string // Perm* constants
}

func (e *MissingPermissionsError) Error() string {
	return fmt.Sprintf("the token is missing permissions on %s: %s.\n%s",
		e.Repo, strings.Join(e.Missing, ", "), TokenHelp(e.Repo))
}

func (e *MissingPermissionsError) Is(target error) bool { return target == ErrMissingPermissions }

// NewTokenURL is GitHub's "new fine-grained token" page pre-filled with the
// owner and every permission gcd needs. The user still selects the repo.
func NewTokenURL(r Repo) string {
	name := "gcd " + r.String()
	if len(name) > 40 {
		name = name[:40]
	}
	q := url.Values{
		"name":        {name},
		"description": {"gcd cloud disk " + r.String()},
		"target_name": {r.Owner},
		"expires_in":  {"366"},
		"contents":    {"write"},
		"workflows":   {"write"},
		"actions":     {"write"},
	}
	return "https://" + Host + "/settings/personal-access-tokens/new?" + q.Encode()
}

// TokenHelp tells the user how to get and install a working token for r.
func TokenHelp(r Repo) string {
	return fmt.Sprintf("%s,\nthen run `gcd login %s`, or set %s.", createTokenHelp(r), r, TokenEnv)
}

func createTokenHelp(r Repo) string {
	return fmt.Sprintf("Create a fine-grained token at %s\n(Repository access: only %s; permissions: %s, %s, %s)",
		NewTokenURL(r), r, PermContents, PermWorkflows, PermActions)
}

// GitHub is the GitHub REST API plus the git smart-HTTP endpoint. Both base
// URLs are injectable so tests run against httptest servers.
type GitHub struct {
	APIURL string       // default DefaultAPIURL
	WebURL string       // where git repos live; default "https://github.com"
	HTTP   *http.Client // default: 15 s timeout
}

// DefaultAPIURL is the public GitHub REST API.
const DefaultAPIURL = "https://api.github.com"

func (g GitHub) apiURL() string {
	if g.APIURL == "" {
		return DefaultAPIURL
	}
	return strings.TrimSuffix(g.APIURL, "/")
}

func (g GitHub) webURL() string {
	if g.WebURL == "" {
		return "https://" + Host
	}
	return strings.TrimSuffix(g.WebURL, "/")
}

// RemoteURL is the git remote URL of r. It never carries credentials.
func (g GitHub) RemoteURL(r Repo) string { return g.webURL() + "/" + r.String() + ".git" }

// RepoOf recognizes a remote URL made by RemoteURL. Other remotes (file://)
// need no token.
func (g GitHub) RepoOf(remote string) (Repo, bool) {
	rest, ok := strings.CutPrefix(remote, g.webURL()+"/")
	if !ok {
		return Repo{}, false
	}
	r, err := ParseRepo(strings.TrimSuffix(rest, ".git"))
	return r, err == nil
}

func (g GitHub) client() *http.Client {
	if g.HTTP == nil {
		return &http.Client{Timeout: 15 * time.Second}
	}
	return g.HTTP
}

// Check verifies, before a Disk is bound to r, that:
//   - the token is valid (else ErrTokenInvalid),
//   - r exists and the token can see it (else ErrRepoNotFound),
//   - r is private (else ErrPublicRepo),
//   - the token may push (Contents write) and dispatch workflows (Actions
//     write), else a *MissingPermissionsError listing what is missing.
//
// Workflows write has no side-effect-free probe; a push that adds or
// changes .github/workflows/ fails with gitstore.ErrWorkflowsPermission
// instead.
func (g GitHub) Check(ctx context.Context, r Repo, token string) error {
	resp, err := g.api(ctx, http.MethodGet, "/repos/"+r.String(), token, nil)
	if err != nil {
		return err
	}
	switch {
	case resp.status == http.StatusOK:
	case resp.status == http.StatusUnauthorized:
		return tokenInvalid(r)
	case resp.status == http.StatusNotFound:
		// A private repo the token was not granted is indistinguishable from
		// a missing one. Asking anonymously at least spots public repos.
		anon, err := g.api(ctx, http.MethodGet, "/repos/"+r.String(), "", nil)
		if err != nil {
			return err
		}
		if anon.status == http.StatusOK {
			return publicRepo(r)
		}
		return repoNotFound(r)
	default:
		return resp.err("GET /repos/" + r.String())
	}
	var info struct {
		Private bool `json:"private"`
	}
	if err := json.Unmarshal(resp.body, &info); err != nil {
		return fmt.Errorf("GitHub API: parse repository %s: %w", r, err)
	}
	if !info.Private {
		return publicRepo(r)
	}

	var missing []string
	ok, err := g.canPush(ctx, r, token)
	if err != nil {
		return err
	}
	if !ok {
		missing = append(missing, PermContents)
	}
	if ok, err = g.canDispatch(ctx, r, token); err != nil {
		return err
	}
	if !ok {
		missing = append(missing, PermActions)
	}
	if len(missing) > 0 {
		return &MissingPermissionsError{Repo: r, Missing: missing}
	}
	return nil
}

// canPush asks for the receive-pack ref advertisement, as `git push` does
// first. GitHub answers it only for tokens with Contents write; nothing is
// written.
func (g GitHub) canPush(ctx context.Context, r Repo, token string) (bool, error) {
	u := g.webURL() + "/" + r.String() + ".git/info/refs?service=git-receive-pack"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.SetBasicAuth(gitstore.TokenUser, token)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Git-Protocol", "version=2")
	resp, err := g.client().Do(req)
	if err != nil {
		return false, fmt.Errorf("contact %s: %w", g.webURL(), err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("git push check for %s: unexpected HTTP %s", r, resp.Status)
}

// probeWorkflow is a workflow file name that never exists, so the dispatch
// probe can never start a run.
const probeWorkflow = "gcd-permission-probe-does-not-exist.yml"

// canDispatch dispatches a workflow that does not exist. GitHub checks the
// token's Actions permission before looking up the workflow: 403 means the
// permission is missing, 404/422 means it is there and nothing ran.
func (g GitHub) canDispatch(ctx context.Context, r Repo, token string) (bool, error) {
	path := "/repos/" + r.String() + "/actions/workflows/" + probeWorkflow + "/dispatches"
	resp, err := g.api(ctx, http.MethodPost, path, token, map[string]any{"ref": "main"})
	if err != nil {
		return false, err
	}
	switch resp.status {
	case http.StatusForbidden:
		if resp.rateLimited() {
			return false, resp.err("POST " + path)
		}
		return false, nil
	case http.StatusUnauthorized:
		return false, tokenInvalid(r)
	case http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusNoContent, http.StatusOK:
		return true, nil
	}
	return false, resp.err("POST " + path)
}

const userAgent = "gcd"

type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

func (g GitHub) api(ctx context.Context, method, path, token string, body any) (*apiResponse, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.apiURL()+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("contact GitHub API: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read GitHub API response: %w", err)
	}
	return &apiResponse{status: resp.StatusCode, header: resp.Header, body: b}, nil
}

func (r *apiResponse) rateLimited() bool {
	return r.header.Get("X-RateLimit-Remaining") == "0" || r.header.Get("Retry-After") != ""
}

func (r *apiResponse) err(what string) error {
	var m struct {
		Message string `json:"message"`
	}
	json.Unmarshal(r.body, &m)
	if (r.status == http.StatusForbidden || r.status == http.StatusTooManyRequests) && r.rateLimited() {
		return fmt.Errorf("GitHub API rate limit exceeded (%s); try again later", what)
	}
	return fmt.Errorf("GitHub API %s: HTTP %d: %s", what, r.status, m.Message)
}

func tokenInvalid(r Repo) error {
	return fmt.Errorf("%w for %s: it is expired, revoked or mistyped.\n%s", ErrTokenInvalid, r, TokenHelp(r))
}

func publicRepo(r Repo) error {
	return fmt.Errorf("%w: %s is public; gcd only syncs to private repos, so your files are never published.\n"+
		"Make it private (https://%s/%s/settings, Danger Zone > Change visibility) or use another repo",
		ErrPublicRepo, r, Host, r)
}

func repoNotFound(r Repo) error {
	q := url.Values{"owner": {r.Owner}, "name": {r.Name}, "visibility": {"private"}}
	return fmt.Errorf("%w: %s does not exist, or the token has no access to it.\n"+
		"gcd does not create repos. Create it as a private repo at https://%s/new?%s\n"+
		"(or `gh repo create %s --private`), and make sure the token's Repository access includes it",
		ErrRepoNotFound, r, Host, q.Encode(), r)
}
