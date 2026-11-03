// Package authtest provides offline stand-ins for GitHub and the OS keychain,
// for tests of auth and of the CLI.
package authtest

import (
	"encoding/json"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/auth"
)

// Grant is what one fine-grained PAT may do.
type Grant struct {
	Repos         []string // "owner/repo" selected under Repository access
	ContentsWrite bool
	ActionsWrite  bool
}

// GitHub answers the REST API (GET /repos/{o}/{r}, workflow dispatches) and
// the git smart-HTTP push advertisement (GET /{o}/{r}.git/info/refs?service=
// git-receive-pack) the way github.com does for fine-grained PATs. Unknown
// tokens are treated as expired.
type GitHub struct {
	Repos  map[string]bool // "owner/repo" -> private
	Tokens map[string]Grant
	// RateLimited makes every GET /repos call fail with a rate limit.
	RateLimited bool
	// GitRoot holds real bare repos (<owner>/<name>.git) served over smart
	// HTTP; see AddRepo. Empty means only the push check is answered.
	GitRoot string

	mu      sync.Mutex
	backend *cgi.Handler
}

// Start serves f until the test ends and returns a client pointed at it.
// After Start, change tokens only through Revoke.
func (f *GitHub) Start(t testing.TB) auth.GitHub {
	t.Helper()
	if f.GitRoot != "" {
		gitPath, err := exec.LookPath("git")
		if err != nil {
			t.Fatal(err)
		}
		f.backend = &cgi.Handler{
			Path: gitPath,
			Args: []string{"http-backend"},
			Env:  []string{"GIT_PROJECT_ROOT=" + f.GitRoot, "GIT_HTTP_EXPORT_ALL=1"},
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return auth.GitHub{APIURL: srv.URL + "/api", WebURL: srv.URL, HTTP: srv.Client()}
}

// Revoke makes token invalid from now on, as an expired PAT is.
func (f *GitHub) Revoke(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.Tokens, token)
}

func (f *GitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock() // requests are served one at a time
	defer f.mu.Unlock()
	if r.Header.Get("User-Agent") == "" {
		jsonError(w, http.StatusForbidden, "Request forbidden by administrative rules. Please make sure your request has a User-Agent header")
		return
	}
	if path, ok := strings.CutSuffix(r.URL.Path, "/dispatches"); ok && r.Method == http.MethodPost {
		f.serveDispatch(w, r, path)
		return
	}
	if path, ok := strings.CutPrefix(r.URL.Path, "/api/repos/"); ok {
		f.serveRepo(w, r, path)
		return
	}
	if name, _, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), ".git/"); ok {
		f.serveGit(w, r, name)
		return
	}
	http.NotFound(w, r)
}

func (f *GitHub) serveRepo(w http.ResponseWriter, r *http.Request, name string) {
	if f.RateLimited {
		w.Header().Set("X-RateLimit-Remaining", "0")
		jsonError(w, http.StatusForbidden, "API rate limit exceeded")
		return
	}
	private, exists := f.Repos[name]
	visible := exists && !private
	if h := r.Header.Get("Authorization"); h != "" {
		g, ok := f.Tokens[strings.TrimPrefix(h, "Bearer ")]
		if !ok {
			jsonError(w, http.StatusUnauthorized, "Bad credentials")
			return
		}
		visible = visible || (exists && slices.Contains(g.Repos, name))
	}
	if !visible {
		jsonError(w, http.StatusNotFound, "Not Found")
		return
	}
	vis := "public"
	if private {
		vis = "private"
	}
	json.NewEncoder(w).Encode(map[string]any{"full_name": name, "private": private, "visibility": vis})
}

// serveDispatch is POST /api/repos/{o}/{r}/actions/workflows/{id}/dispatches.
// GitHub checks the fine-grained permission before looking up the workflow.
func (f *GitHub) serveDispatch(w http.ResponseWriter, r *http.Request, path string) {
	rest, _ := strings.CutPrefix(path, "/api/repos/")
	name, _, _ := strings.Cut(rest, "/actions/")
	g, ok := f.Tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	switch {
	case !ok:
		jsonError(w, http.StatusUnauthorized, "Bad credentials")
	case !slices.Contains(g.Repos, name):
		jsonError(w, http.StatusNotFound, "Not Found")
	case !g.ActionsWrite:
		w.Header().Set("X-Accepted-GitHub-Permissions", "actions=write")
		jsonError(w, http.StatusForbidden, "Resource not accessible by personal access token")
	default:
		jsonError(w, http.StatusNotFound, "Not Found") // no such workflow: nothing runs
	}
}

// serveGit is git smart HTTP for repo name: basic auth with the token as
// password, Contents write for pushes. With GitRoot set the request is served
// by `git http-backend` from GitRoot/<name>.git; without it only the push
// advertisement is faked (enough for auth.GitHub.Check).
func (f *GitHub) serveGit(w http.ResponseWriter, r *http.Request, name string) {
	user, pass, ok := r.BasicAuth()
	g, valid := f.Tokens[pass]
	if !ok || user == "" || !valid {
		w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
		http.Error(w, "Invalid username or token.", http.StatusUnauthorized)
		return
	}
	if !slices.Contains(g.Repos, name) {
		http.Error(w, "Repository not found.", http.StatusNotFound)
		return
	}
	push := r.URL.Query().Get("service") == "git-receive-pack" || strings.HasSuffix(r.URL.Path, "/git-receive-pack")
	if push && !g.ContentsWrite {
		http.Error(w, "Permission to "+name+".git denied to alice.", http.StatusForbidden)
		return
	}
	if f.GitRoot != "" {
		f.backend.ServeHTTP(w, r)
		return
	}
	if push && strings.HasSuffix(r.URL.Path, "/info/refs") {
		w.Header().Set("Content-Type", "application/x-git-receive-pack-advertisement")
		w.Write([]byte("001f# service=git-receive-pack\n0000"))
		return
	}
	http.NotFound(w, r)
}

// AddRepo creates an empty bare repo "owner/name" under GitRoot (made on
// first use) and registers it in Repos. Call it before Start.
func (f *GitHub) AddRepo(t testing.TB, name string, private bool) (dir string) {
	t.Helper()
	if f.GitRoot == "" {
		f.GitRoot = t.TempDir()
	}
	if f.Repos == nil {
		f.Repos = map[string]bool{}
	}
	f.Repos[name] = private
	dir = filepath.Join(f.GitRoot, filepath.FromSlash(name)+".git")
	for _, args := range [][]string{
		{"init", "--quiet", "--bare", "--initial-branch=main", dir},
		{"--git-dir=" + dir, "config", "http.receivepack", "true"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// Keyring is an in-memory OS keychain, safe for concurrent use.
type Keyring struct {
	mu      sync.Mutex
	secrets map[string]string
	// Err, when set, makes every call fail (e.g. no Secret Service).
	Err error
}

func key(service, user string) string { return service + "|" + user }

func (k *Keyring) Get(service, user string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Err != nil {
		return "", k.Err
	}
	s, ok := k.secrets[key(service, user)]
	if !ok {
		return "", auth.ErrKeyringNotFound
	}
	return s, nil
}

func (k *Keyring) Set(service, user, secret string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Err != nil {
		return k.Err
	}
	if k.secrets == nil {
		k.secrets = map[string]string{}
	}
	k.secrets[key(service, user)] = secret
	return nil
}

// Len is the number of stored secrets.
func (k *Keyring) Len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.secrets)
}
