// Package registry is the per-machine list of Disks (written by `gcd init`)
// and the lookup of which Disk a command operates on.
//
// Layout under the gcd home directory (see Home):
//
//	registry.json        the Disk list
//	disks/<name>/        per-Disk state dir: state.json and git/ (the git-dir)
//
// Disk names are unique ignoring case and Disk folders never overlap (no
// Disk inside another). Folders are matched by file identity, so letter case
// on case-insensitive file systems and symlinked parents do not matter.
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
	"github.com/Hanson-Tsai/gcd/internal/scan"
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

// CheckName validates a Disk name. A name is typed after --disk and is also
// a directory under the gcd home, so it is 1-64 ASCII letters, digits, '.',
// '_' or '-', starts with a letter or digit, does not end with '.', and is
// not a Windows reserved device name.
func CheckName(name string) error {
	bad := func(why string) error {
		return fmt.Errorf("invalid disk name %q: %s", name, why)
	}
	switch {
	case name == "":
		return bad("empty")
	case len(name) > 64:
		return bad("longer than 64 characters")
	case !isAlnum(name[0]):
		return bad("must start with a letter or digit")
	case strings.HasSuffix(name, "."):
		return bad("must not end with '.'")
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; !isAlnum(c) && c != '.' && c != '_' && c != '-' {
			return bad("use only letters, digits, '.', '_' and '-'")
		}
	}
	if scan.ReservedOnWindows(name) {
		return bad("reserved on Windows")
	}
	return nil
}

func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// Add registers e. It refuses a name already taken (ignoring case, since the
// name is also a directory under the gcd home) and a folder that is, contains
// or lies inside another Disk's folder.
func (r *Registry) Add(e Entry) error {
	if err := CheckName(e.Name); err != nil {
		return err
	}
	for _, d := range r.Disks {
		if strings.EqualFold(d.Name, e.Name) {
			return fmt.Errorf("a disk named %q already exists (%s)", d.Name, d.Folder)
		}
	}
	for _, d := range r.Disks {
		switch {
		case contains(d.Folder, e.Folder) && contains(e.Folder, d.Folder):
			return fmt.Errorf("%s is already disk %q", e.Folder, d.Name)
		case contains(d.Folder, e.Folder):
			return fmt.Errorf("%s is inside disk %q (%s)", e.Folder, d.Name, d.Folder)
		case contains(e.Folder, d.Folder):
			return fmt.Errorf("%s contains disk %q (%s)", e.Folder, d.Name, d.Folder)
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

// Resolve picks the Disk a command operates on:
//
//  1. the Disk called name, when name is set (the --disk flag);
//  2. else the Disk whose folder is cwd or contains it;
//  3. else the only registered Disk.
//
// Otherwise the error lists the registered Disks.
func (r *Registry) Resolve(cwd, name string) (Entry, error) {
	if len(r.Disks) == 0 {
		return Entry{}, errors.New("no disk registered; run `gcd init <git-url> <folder>` first")
	}
	if name != "" {
		for _, d := range r.Disks {
			if strings.EqualFold(d.Name, name) {
				return d, nil
			}
		}
		return Entry{}, fmt.Errorf("no disk named %q; registered disks:\n%s", name, r.list())
	}
	for _, d := range r.Disks {
		if contains(d.Folder, cwd) {
			return d, nil
		}
	}
	if len(r.Disks) == 1 {
		return r.Disks[0], nil
	}
	return Entry{}, fmt.Errorf("not inside a disk folder; cd into one or pass --disk <name>:\n%s", r.list())
}

// list formats the registered Disks one per line for error messages.
func (r *Registry) list() string {
	width := 0
	for _, d := range r.Disks {
		width = max(width, len(d.Name))
	}
	var b strings.Builder
	for _, d := range r.Disks {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, d.Name, d.Folder)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
