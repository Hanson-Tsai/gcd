package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/registry"
)

func init() {
	register(command{
		name:    "init",
		usage:   "init [--disk <name>] <git-url> <folder>",
		summary: "bind a folder to a git remote",
		run:     runInit,
	})
}

func runInit(ctx context.Context, e *env, args []string) error {
	fs := newDiskFlags(e, "init")
	fs.Lookup("disk").Usage = "`name` of the new disk (default: the folder's name)"
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errUsage
	}
	url := fs.Arg(0)
	folder, err := filepath.Abs(fs.Arg(1))
	if err != nil {
		return err
	}

	home, err := registry.Home()
	if err != nil {
		return err
	}
	reg, err := registry.Load(home)
	if err != nil {
		return err
	}
	name, err := newDiskName(e.disk, folder)
	if err != nil {
		return err
	}
	ent := registry.Entry{Name: name, Folder: folder, Remote: url, StateDir: reg.StateDirFor(name)}
	if err := reg.Add(ent); err != nil { // before MkdirAll: a refused folder is not created
		return err
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return err
	}
	if _, err := disk.Open(disk.Options{Folder: ent.Folder, Remote: ent.Remote, StateDir: ent.StateDir}); err != nil {
		return err
	}
	if err := reg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "initialized disk %q: %s <-> %s\nrun `gcd sync` to sync\n", name, folder, url)
	return nil
}

// newDiskName is the --disk value, or else the folder's base name when that
// is a valid Disk name.
func newDiskName(flagValue, folder string) (string, error) {
	if flagValue != "" {
		return flagValue, registry.CheckName(flagValue)
	}
	name := filepath.Base(folder)
	if registry.CheckName(name) != nil {
		return "", fmt.Errorf("folder name %q cannot be a disk name; choose one with --disk <name> "+
			"(letters, digits, '.', '_', '-')", name)
	}
	return name, nil
}
