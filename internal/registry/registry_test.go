package registry_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/registry"
)

// newRegistry returns an empty registry in a temp gcd home and a temp root
// for Disk folders.
func newRegistry(t *testing.T) (*registry.Registry, string) {
	t.Helper()
	reg, err := registry.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return reg, t.TempDir()
}

// mkdir creates dir (and parents) and returns it.
func mkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func add(t *testing.T, reg *registry.Registry, name, folder string) registry.Entry {
	t.Helper()
	e := registry.Entry{Name: name, Folder: folder, Remote: "file:///" + name, StateDir: reg.StateDirFor(name)}
	if err := reg.Add(e); err != nil {
		t.Fatalf("Add(%s, %s): %v", name, folder, err)
	}
	return e
}

func TestAddRejectsDuplicateNameIgnoringCase(t *testing.T) {
	reg, root := newRegistry(t)
	add(t, reg, "notes", mkdir(t, filepath.Join(root, "a")))

	err := reg.Add(registry.Entry{Name: "Notes", Folder: mkdir(t, filepath.Join(root, "b"))})
	if err == nil || !strings.Contains(err.Error(), "notes") {
		t.Fatalf("Add duplicate name = %v, want an error naming the existing disk", err)
	}
}

func TestAddRejectsFolderUsedByAnotherDisk(t *testing.T) {
	reg, root := newRegistry(t)
	notes := add(t, reg, "notes", mkdir(t, filepath.Join(root, "notes")))

	for name, folder := range map[string]string{
		"same folder":    notes.Folder,
		"inside it":      mkdir(t, filepath.Join(notes.Folder, "sub", "deeper")),
		"containing it":  root,
		"trailing slash": notes.Folder + string(filepath.Separator),
	} {
		t.Run(name, func(t *testing.T) {
			err := reg.Add(registry.Entry{Name: "other", Folder: folder})
			if err == nil || !strings.Contains(err.Error(), `"notes"`) {
				t.Fatalf("Add(%s) = %v, want an error naming disk \"notes\"", folder, err)
			}
		})
	}

	add(t, reg, "docs", mkdir(t, filepath.Join(root, "notes-2"))) // sibling with a shared prefix is fine
}

func TestAddRejectsBadNames(t *testing.T) {
	reg, root := newRegistry(t)
	for _, name := range []string{"", ".", "..", ".hidden", "a/b", `a\b`, "my notes", "notes.", "con", "NUL", "com1", "lpt9",
		strings.Repeat("x", 65)} {
		if err := reg.Add(registry.Entry{Name: name, Folder: mkdir(t, filepath.Join(root, "f"))}); err == nil {
			t.Errorf("Add accepted disk name %q", name)
		}
	}
	for _, name := range []string{"notes", "Work-2026", "a.b_c", "console"} {
		add(t, reg, name, mkdir(t, filepath.Join(root, "ok-"+name)))
	}
}

// twoDisks registers "notes" and "docs" in sibling folders under root.
func twoDisks(t *testing.T) (reg *registry.Registry, root string, notes, docs registry.Entry) {
	t.Helper()
	reg, root = newRegistry(t)
	notes = add(t, reg, "notes", mkdir(t, filepath.Join(root, "notes")))
	docs = add(t, reg, "docs", mkdir(t, filepath.Join(root, "docs")))
	return reg, root, notes, docs
}

func resolve(t *testing.T, reg *registry.Registry, cwd, name string) string {
	t.Helper()
	e, err := reg.Resolve(cwd, name)
	if err != nil {
		t.Fatalf("Resolve(%s, %q): %v", cwd, name, err)
	}
	return e.Name
}

func TestResolvePicksTheDiskContainingCwd(t *testing.T) {
	reg, _, notes, docs := twoDisks(t)

	if got := resolve(t, reg, docs.Folder, ""); got != "docs" {
		t.Errorf("in docs folder: got %q", got)
	}
	if got := resolve(t, reg, mkdir(t, filepath.Join(notes.Folder, "a", "b")), ""); got != "notes" {
		t.Errorf("in notes subfolder: got %q", got)
	}
}

func TestResolveByNameWhenOutsideAnyDisk(t *testing.T) {
	reg, root, notes, _ := twoDisks(t)

	if got := resolve(t, reg, root, "docs"); got != "docs" {
		t.Errorf("--disk docs outside: got %q", got)
	}
	if got := resolve(t, reg, notes.Folder, "docs"); got != "docs" {
		t.Errorf("--disk docs inside notes: got %q, want the named disk to win", got)
	}
}

func TestResolveUnknownNameListsDisks(t *testing.T) {
	reg, root, _, _ := twoDisks(t)

	_, err := reg.Resolve(root, "music")
	if err == nil || !strings.Contains(err.Error(), "music") ||
		!strings.Contains(err.Error(), "notes") || !strings.Contains(err.Error(), "docs") {
		t.Fatalf("Resolve unknown name = %v, want an error naming it and listing the disks", err)
	}
}

func TestResolveOutsideAnyDiskListsDisks(t *testing.T) {
	reg, root, notes, docs := twoDisks(t)

	_, err := reg.Resolve(root, "")
	if err == nil {
		t.Fatal("Resolve outside any disk with two disks succeeded")
	}
	for _, want := range []string{"--disk", "notes", notes.Folder, "docs", docs.Folder} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestResolveOnlyDiskNeedsNoFlag(t *testing.T) {
	reg, root := newRegistry(t)
	add(t, reg, "notes", mkdir(t, filepath.Join(root, "notes")))

	if got := resolve(t, reg, t.TempDir(), ""); got != "notes" {
		t.Errorf("only disk: got %q", got)
	}
}

func TestResolveWithNoDisks(t *testing.T) {
	reg, root := newRegistry(t)
	if _, err := reg.Resolve(root, ""); err == nil || !strings.Contains(err.Error(), "gcd init") {
		t.Fatalf("Resolve with no disks = %v, want a hint to run gcd init", err)
	}
}

// TestResolveIgnoresCaseOnCaseInsensitiveFS covers macOS and Windows default
// volumes; it skips where the temp dir is case-sensitive (typical Linux).
func TestResolveIgnoresCaseOnCaseInsensitiveFS(t *testing.T) {
	reg, root, notes, _ := twoDisks(t)
	mkdir(t, filepath.Join(notes.Folder, "Sub"))
	upper := filepath.Join(root, "NOTES", "SUB")
	if _, err := os.Stat(upper); err != nil {
		t.Skip("temp dir is on a case-sensitive file system")
	}

	if got := resolve(t, reg, upper, ""); got != "notes" {
		t.Errorf("cwd %s: got %q, want notes", upper, got)
	}
	if err := reg.Add(registry.Entry{Name: "other", Folder: filepath.Join(root, "Docs")}); err == nil {
		t.Error("Add accepted a folder differing only in case from disk docs")
	}
}

// TestResolveRespectsCaseOnCaseSensitiveFS checks the opposite: where the
// file system tells NOTES and notes apart, they are different folders.
func TestResolveRespectsCaseOnCaseSensitiveFS(t *testing.T) {
	reg, root := newRegistry(t)
	notes := add(t, reg, "notes", mkdir(t, filepath.Join(root, "notes")))
	other := filepath.Join(root, "NOTES")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Skip("temp dir is on a case-insensitive file system")
	}
	add(t, reg, "docs", mkdir(t, filepath.Join(root, "docs")))

	if _, err := reg.Resolve(other, ""); err == nil {
		t.Errorf("cwd %s resolved, want it outside disk %s", other, notes.Folder)
	}
	add(t, reg, "upper", other)
}

func TestResolveThroughSymlinkedParent(t *testing.T) {
	reg, root, notes, _ := twoDisks(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	if got := resolve(t, reg, filepath.Join(link, filepath.Base(notes.Folder)), ""); got != "notes" {
		t.Errorf("cwd via symlink: got %q, want notes", got)
	}
}
