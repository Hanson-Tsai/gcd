package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/registry"
)

func init() {
	register(command{
		name:    "init",
		usage:   "init <git-url> <folder>",
		summary: "bind a folder to a git remote",
		run:     runInit,
	})
}

func runInit(ctx context.Context, e *env, args []string) error {
	fs := newFlags(e, "init")
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
	if err := os.MkdirAll(folder, 0o755); err != nil {
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
	name := filepath.Base(folder)
	ent := registry.Entry{Name: name, Folder: folder, Remote: url, StateDir: reg.StateDirFor(name)}
	if err := reg.Add(ent); err != nil {
		return err
	}
	d, err := disk.Open(disk.Options{Folder: ent.Folder, Remote: ent.Remote, StateDir: ent.StateDir})
	if err != nil {
		return err
	}
	createdConfig, err := d.EnsureRepoConfig(ctx)
	if err != nil {
		return err
	}
	if err := reg.Save(); err != nil {
		return err
	}
	if createdConfig {
		fmt.Fprintf(e.stdout, "wrote the default repo config %s\n", config.Path)
	}
	fmt.Fprintf(e.stdout, "initialized disk %q: %s <-> %s\nrun `gcd sync` to sync\n", name, folder, url)
	return nil
}
