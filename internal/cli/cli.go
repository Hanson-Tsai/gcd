// Package cli implements the gcd command line with the standard library only:
// one flag.FlagSet per subcommand. Each subcommand lives in its own file
// (cmd_<name>.go) and registers itself from init(), so adding a command never
// touches this file.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/gitstore"
	"github.com/Hanson-Tsai/gcd/internal/registry"
)

// command is one gcd subcommand.
type command struct {
	name    string
	usage   string // e.g. "init <git-url> <folder>"
	summary string
	run     func(ctx context.Context, e *env, args []string) error
}

var commands = map[string]command{}

func register(c command) { commands[c.name] = c }

// env carries the process environment into commands.
type env struct {
	stdout, stderr io.Writer
}

// errUsage marks errors that should print the command's usage.
var errUsage = errors.New("usage")

// Run executes gcd with args (without the program name) and returns the exit
// code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	e := &env{stdout: stdout, stderr: stderr}
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage(stdout)
		return 0
	}
	c, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "gcd: unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
	if err := gitstore.CheckVersion(); err != nil {
		fmt.Fprintln(stderr, "gcd:", err)
		return 1
	}
	if err := c.run(ctx, e, args[1:]); err != nil {
		if errors.Is(err, errUsage) || errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stderr, "usage: gcd %s\n", c.usage)
			return 2
		}
		fmt.Fprintln(stderr, "gcd:", err)
		return 1
	}
	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "gcd: sync a local folder with a private GitHub repo")
	fmt.Fprintln(w, "\nusage:")
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  gcd %-28s %s\n", commands[n].usage, commands[n].summary)
	}
}

// newFlags returns a FlagSet for a subcommand that reports errors instead of
// exiting.
func newFlags(e *env, name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	return fs
}

// openCurrentDisk resolves the Disk for the current directory and opens it.
func openCurrentDisk() (*disk.Disk, registry.Entry, error) {
	home, err := registry.Home()
	if err != nil {
		return nil, registry.Entry{}, err
	}
	reg, err := registry.Load(home)
	if err != nil {
		return nil, registry.Entry{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, registry.Entry{}, err
	}
	ent, err := reg.Resolve(cwd)
	if err != nil {
		return nil, ent, err
	}
	d, err := disk.Open(disk.Options{Folder: ent.Folder, Remote: ent.Remote, StateDir: ent.StateDir})
	return d, ent, err
}
