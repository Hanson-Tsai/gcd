// Package fixture provides the Seam A test fixture: a local bare repo standing
// in for GitHub (reached through a file:// URL) plus any number of Disk
// instances, each simulating a separate device with its own synced folder,
// state directory and hostname.
//
// Typical use from an external test package:
//
//	remote, devs := fixture.NewDevices(t, 2)
//	devs[0].Write(t, "a.txt", "hello")
//	devs[0].Sync(t)
//	devs[1].Sync(t)
//	devs[1].AssertFiles(t, map[string]string{"a.txt": "hello"})
//
// Everything lives under t.TempDir(); no network or token is needed.
package fixture

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/disk"
)

// Remote is a bare git repository acting as the shared GitHub repo.
type Remote struct {
	Dir string // filesystem path of the bare repo
	URL string // file:// URL handed to disk.Options.Remote
}

// NewRemote creates an empty bare repo in a temp dir.
func NewRemote(t testing.TB) *Remote {
	t.Helper()
	dir := filepath.Join(tempDir(t), "remote.git")
	git(t, "", "init", "--quiet", "--bare", "--initial-branch=main", dir)
	return &Remote{Dir: dir, URL: FileURL(dir)}
}

// FileURL turns an absolute filesystem path into a file:// URL that system git
// accepts on every platform (file:///tmp/x, file:///C:/Users/x).
func FileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// Head returns the commit hash of refs/heads/main, or "" when the remote is empty.
func (r *Remote) Head(t testing.TB) string {
	t.Helper()
	out, err := gitOut(r.Dir, "rev-parse", "--verify", "--quiet", "refs/heads/main")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Files returns path -> content of the tree at the remote's main branch.
// Paths use forward slashes. An empty remote yields an empty map.
func (r *Remote) Files(t testing.TB) map[string]string {
	t.Helper()
	files := map[string]string{}
	head := r.Head(t)
	if head == "" {
		return files
	}
	out := git(t, r.Dir, "ls-tree", "-r", "-z", head)
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, path, _ := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		files[path] = git(t, r.Dir, "cat-file", "blob", fields[2])
	}
	return files
}

// Commit describes one commit on the remote's main branch.
type Commit struct {
	Hash        string
	Tree        string
	AuthorName  string
	AuthorEmail string
	Subject     string
}

// Log returns the remote's main-branch commits, newest first.
func (r *Remote) Log(t testing.TB) []Commit {
	t.Helper()
	if r.Head(t) == "" {
		return nil
	}
	out := git(t, r.Dir, "log", "-z", "--format=%H%x1f%T%x1f%an%x1f%ae%x1f%s", "refs/heads/main")
	var commits []Commit
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		commits = append(commits, Commit{Hash: f[0], Tree: f[1], AuthorName: f[2], AuthorEmail: f[3], Subject: f[4]})
	}
	return commits
}

// Device is one simulated machine: a synced folder and a Disk bound to the remote.
type Device struct {
	Host     string
	Folder   string // the synced folder (contains only user files)
	StateDir string // local state + git-dir, outside Folder
	Disk     *disk.Disk
}

// NewDevice opens a Disk for host against remote, with a fresh folder and a
// state dir that lives outside that folder.
func NewDevice(t testing.TB, remote *Remote, host string) *Device {
	t.Helper()
	root := tempDir(t)
	d := &Device{
		Host:     host,
		Folder:   filepath.Join(root, "folder"),
		StateDir: filepath.Join(root, "state"),
	}
	if err := os.MkdirAll(d.Folder, 0o755); err != nil {
		t.Fatal(err)
	}
	dk, err := disk.Open(disk.Options{
		Folder:   d.Folder,
		Remote:   remote.URL,
		StateDir: d.StateDir,
		Host:     host,
	})
	if err != nil {
		t.Fatalf("open disk for %s: %v", host, err)
	}
	d.Disk = dk
	return d
}

// NewDevices creates a remote and n devices named host1..hostN.
func NewDevices(t testing.TB, n int) (*Remote, []*Device) {
	t.Helper()
	remote := NewRemote(t)
	devs := make([]*Device, n)
	for i := range devs {
		devs[i] = NewDevice(t, remote, fmt.Sprintf("host%d", i+1))
	}
	return remote, devs
}

// Sync runs one sync and fails the test on error.
func (d *Device) Sync(t testing.TB) disk.Result {
	t.Helper()
	res, err := d.Disk.Sync(context.Background(), disk.SyncOptions{})
	if err != nil {
		t.Fatalf("sync on %s: %v", d.Host, err)
	}
	return res
}

// Write creates or overwrites a file (slash-separated rel path) in the folder.
func (d *Device) Write(t testing.TB, rel, content string) {
	t.Helper()
	p := d.Path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Remove deletes a file from the folder.
func (d *Device) Remove(t testing.TB, rel string) {
	t.Helper()
	if err := os.Remove(d.Path(rel)); err != nil {
		t.Fatal(err)
	}
}

// Path maps a slash-separated rel path to an OS path inside the folder.
func (d *Device) Path(rel string) string {
	return filepath.Join(d.Folder, filepath.FromSlash(rel))
}

// Files returns path -> content of every regular file in the folder.
func (d *Device) Files(t testing.TB) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(d.Folder, func(p string, e fs.DirEntry, err error) error {
		if err != nil || !e.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(d.Folder, p)
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// AssertFiles fails unless the folder holds exactly want.
func (d *Device) AssertFiles(t testing.TB, want map[string]string) {
	t.Helper()
	if got := d.Files(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s folder:\n got  %q\n want %q", d.Host, got, want)
	}
}

// AssertFiles fails unless the remote tree holds exactly want.
func (r *Remote) AssertFiles(t testing.TB, want map[string]string) {
	t.Helper()
	if got := r.Files(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("remote tree:\n got  %q\n want %q", got, want)
	}
}

// tempDir is t.TempDir plus a cleanup that makes git's read-only object files
// writable first, so removal also succeeds on Windows.
func tempDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
			if err == nil {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	return dir
}

func git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := gitOut(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func gitOut(dir string, args ...string) (string, error) {
	if dir != "" {
		args = append([]string{"--git-dir=" + dir}, args...)
	}
	cmd := exec.Command("git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
