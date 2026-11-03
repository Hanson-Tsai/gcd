// Package registry is the per-machine list of Disks (written by `gcd init`)
// and the lookup of which Disk a command operates on.
//
// Layout under the gcd home directory (see Home):
//
//	registry.json        the Disk list
//	disks/<name>/        per-Disk state dir: state.json and git/ (the git-dir)
//
// Named Disks, --disk and case-insensitive path matching are ticket #11.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Hanson-Tsai/gcd/internal/apply"
)

// HomeEnv overrides the gcd home directory (used by tests and power users).
const HomeEnv = "GCD_HOME"

// Home returns the directory holding gcd's per-machine data:
// $GCD_HOME, else the OS user-data dir + "/gcd":
// macOS ~/Library/Application Support/gcd, Windows %LocalAppData%\gcd,
// others $XDG_DATA_HOME/gcd or ~/.local/share/gcd.
func Home() (string, error) {
	if h := os.Getenv(HomeEnv); h != "" {
		return filepath.Abs(h)
	}
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LocalAppData"); d != "" {
			return filepath.Join(d, "gcd"), nil
		}
	case "darwin", "ios":
		d, err := os.UserConfigDir() // ~/Library/Application Support
		if err != nil {
			return "", err
		}
		return filepath.Join(d, "gcd"), nil
	default:
		if d := os.Getenv("XDG_DATA_HOME"); d != "" {
			return filepath.Join(d, "gcd"), nil
		}
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "share", "gcd"), nil
}

// Entry is one registered Disk.
type Entry struct {
	Name     string `json:"name"`
	Folder   string `json:"folder"`    // absolute synced folder
	Remote   string `json:"remote"`    // git URL
	StateDir string `json:"state_dir"` // holds state.json and git/ (the git-dir)
}

// Registry is the loaded Disk list of one gcd home.
type Registry struct {
	home  string
	Disks []Entry `json:"disks"`
}

// Load reads the registry under home; a missing file is an empty registry.
func Load(home string) (*Registry, error) {
	r := &Registry{home: home}
	b, err := os.ReadFile(r.file())
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("parse %s: %w", r.file(), err)
	}
	return r, nil
}

func (r *Registry) file() string { return filepath.Join(r.home, "registry.json") }

// StateDirFor is the default state dir for a Disk named name.
func (r *Registry) StateDirFor(name string) string {
	return filepath.Join(r.home, "disks", name)
}

// Add registers e, refusing a duplicate name or folder.
func (r *Registry) Add(e Entry) error {
	for _, d := range r.Disks {
		if d.Name == e.Name {
			return fmt.Errorf("a disk named %q already exists (%s)", e.Name, d.Folder)
		}
		if d.Folder == e.Folder {
			return fmt.Errorf("%s is already disk %q", e.Folder, d.Name)
		}
	}
	r.Disks = append(r.Disks, e)
	return nil
}

// Save writes the registry atomically.
func (r *Registry) Save() error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return apply.WriteFile(r.file(), strings.NewReader(string(b)+"\n"))
}

// Resolve picks the Disk whose folder contains cwd; failing that, the only
// registered Disk.
func (r *Registry) Resolve(cwd string) (Entry, error) {
	for _, d := range r.Disks {
		if rel, err := filepath.Rel(d.Folder, cwd); err == nil &&
			(rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			return d, nil
		}
	}
	switch len(r.Disks) {
	case 0:
		return Entry{}, errors.New("no disk registered; run `gcd init <git-url> <folder>` first")
	case 1:
		return r.Disks[0], nil
	}
	names := make([]string, len(r.Disks))
	for i, d := range r.Disks {
		names[i] = d.Name + " (" + d.Folder + ")"
	}
	return Entry{}, fmt.Errorf("not inside a disk folder; disks: %s", strings.Join(names, ", "))
}
