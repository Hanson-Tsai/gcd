package gitstore_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
)

// TestMain lets this test binary stand in for the gcd executable when git
// runs it as GIT_ASKPASS.
func TestMain(m *testing.M) {
	if gitstore.ServeAskpass() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const goodToken = "github_pat_s3cr3t"

// gitServer is a smart-HTTP git server (git http-backend behind net/http/cgi)
// that, like GitHub, demands basic auth with the token as password.
type gitServer struct {
	URL    string // http://127.0.0.1:port/remote.git
	remote *fixture.Remote

	mu        sync.Mutex
	passwords []string // every password presented
}

func newGitServer(t *testing.T) *gitServer {
	t.Helper()
	remote := fixture.NewRemote(t)
	if out, err := exec.Command("git", "--git-dir="+remote.Dir, "config", "http.receivepack", "true").CombinedOutput(); err != nil {
		t.Fatalf("enable receive-pack: %v: %s", err, out)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Dir(remote.Dir), "GIT_HTTP_EXPORT_ALL=1"},
	}
	s := &gitServer{remote: remote}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if ok {
			s.mu.Lock()
			s.passwords = append(s.passwords, pass)
			s.mu.Unlock()
		}
		if !ok || pass != goodToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			http.Error(w, "Invalid username or token.", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL + "/" + filepath.Base(remote.Dir)
	return s
}

// isolateUserGitConfig points git at a user config whose credential helper
// answers with a wrong password, as a stale osxkeychain/manager entry would.
func isolateUserGitConfig(t *testing.T) {
	home := t.TempDir()
	cfg := "[credential]\n\thelper = \"!f() { echo username=someone; echo password=stale; }; f\"\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
}

// pushOneFile commits a.txt=hello on top of nothing and pushes it.
func pushOneFile(ctx context.Context, s *gitstore.Store, dir string) error {
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		return err
	}
	blobs, err := s.WriteBlobs(ctx, []string{f})
	if err != nil {
		return err
	}
	tree, err := s.WriteTree(ctx, map[string]gitstore.Entry{"a.txt": {Mode: gitstore.RegularMode, Hash: blobs[0]}})
	if err != nil {
		return err
	}
	commit, err := s.CommitTree(ctx, tree, "", gitstore.Signature{Name: "t", Email: "t@t"}, "m")
	if err != nil {
		return err
	}
	return s.Push(ctx, commit)
}

func TestTokenReachesGitThroughCredentialsOnly(t *testing.T) {
	isolateUserGitConfig(t)
	srv := newGitServer(t)
	ctx := context.Background()
	gitDir := filepath.Join(t.TempDir(), "git")
	s, err := gitstore.Open(ctx, gitDir, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.SetToken(goodToken)

	if err := pushOneFile(ctx, s, t.TempDir()); err != nil {
		t.Fatalf("push with token: %v", err)
	}
	if got := srv.remote.Files(t); got["a.txt"] != "hello" {
		t.Fatalf("remote files = %v, want a.txt=hello", got)
	}
	head, err := s.Fetch(ctx)
	if err != nil || head == "" {
		t.Fatalf("fetch with token = %q, %v", head, err)
	}

	cfg, err := os.ReadFile(filepath.Join(gitDir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), goodToken) {
		t.Errorf("token written to git-dir config:\n%s", cfg)
	}
}

func TestRejectedTokenIsAnAuthErrorWithoutTheToken(t *testing.T) {
	isolateUserGitConfig(t)
	srv := newGitServer(t)
	ctx := context.Background()
	s, err := gitstore.Open(ctx, filepath.Join(t.TempDir(), "git"), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	const expired = "github_pat_expired"
	s.SetToken(expired)

	_, err = s.Fetch(ctx)
	if !errors.Is(err, gitstore.ErrAuth) {
		t.Fatalf("fetch with expired token: err = %v, want ErrAuth", err)
	}
	if strings.Contains(err.Error(), expired) {
		t.Errorf("error leaks the token: %v", err)
	}
	var ae *gitstore.AuthError
	if !errors.As(err, &ae) || ae.Remote != srv.URL {
		t.Errorf("AuthError remote = %+v, want %s", ae, srv.URL)
	}
}

func TestNoTokenFailsInsteadOfPrompting(t *testing.T) {
	isolateUserGitConfig(t)
	srv := newGitServer(t)
	ctx := context.Background()
	s, err := gitstore.Open(ctx, filepath.Join(t.TempDir(), "git"), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fetch(ctx); !errors.Is(err, gitstore.ErrAuth) {
		t.Fatalf("fetch without token: err = %v, want ErrAuth", err)
	}
}

// A remote that redirects git elsewhere must not get the token there: the
// askpass answers only for the configured remote's host.
func TestTokenIsNotSentToARedirectedHost(t *testing.T) {
	isolateUserGitConfig(t)
	other := newGitServer(t)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := strings.TrimSuffix(other.URL, "/"+filepath.Base(other.remote.Dir)) + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	ctx := context.Background()
	s, err := gitstore.Open(ctx, filepath.Join(t.TempDir(), "git"), redirect.URL+"/"+filepath.Base(other.remote.Dir))
	if err != nil {
		t.Fatal(err)
	}
	s.SetToken(goodToken)

	if _, err := s.Fetch(ctx); err == nil {
		t.Fatal("fetch through a redirect to another host succeeded")
	}
	other.mu.Lock()
	defer other.mu.Unlock()
	for _, p := range other.passwords {
		if p == goodToken {
			t.Fatal("the token was sent to the host the remote redirected to")
		}
	}
}
