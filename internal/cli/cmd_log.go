package cli

import (
	"context"
	"fmt"

	"github.com/Hanson-Tsai/gcd/internal/disk"
)

func init() {
	register(command{
		name:    "log",
		usage:   "log [path] [--disk <name>] [--json]",
		summary: "show the sync history of the disk or of one path",
		run:     runLog,
		json:    true,
	})
}

// logJSON is the object of `log --json`.
type logJSON struct {
	Disk    string         `json:"disk"`
	Path    string         `json:"path"` // "" for the whole Disk
	Entries []logEntryJSON `json:"entries"`
}

type logEntryJSON struct {
	Commit  string       `json:"commit"`
	Time    string       `json:"time"`
	Host    string       `json:"host"`
	Changes []changeJSON `json:"changes"`
}

// timeFormat prints history times in a form `gcd restore --at` accepts.
const timeFormat = "2006-01-02 15:04:05"

func runLog(ctx context.Context, e *env, args []string) error {
	fs := newDiskFlags(e, "log")
	jsonFlag(fs, e)
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return errUsage
	}
	d, ent, err := openCurrentDisk(e)
	if err != nil {
		return err
	}
	rel := ""
	if len(pos) == 1 {
		if rel, err = folderPath(d.Folder(), pos[0]); err != nil {
			return err
		}
	}
	log, err := d.Log(ctx, rel)
	if err != nil {
		return err
	}
	if e.json {
		out := logJSON{Disk: ent.Name, Path: rel, Entries: []logEntryJSON{}}
		for _, l := range log {
			out.Entries = append(out.Entries, logEntryJSON{Commit: l.Commit, Time: jsonTime(l.Time), Host: l.Host, Changes: changesJSON(l.Changes)})
		}
		return writeJSON(e.stdout, out)
	}
	if len(log) == 0 {
		if rel == "" {
			fmt.Fprintln(e.stdout, "no history yet")
		} else {
			fmt.Fprintf(e.stdout, "no history for %s\n", rel)
		}
		return nil
	}
	for _, l := range log {
		fmt.Fprintf(e.stdout, "%.7s  %s  %-12s  %s\n", l.Commit, l.Time.Local().Format(timeFormat), l.Host, summary(l.Changes))
		if rel != "" {
			for _, c := range l.Changes {
				fmt.Fprintf(e.stdout, "    %s %s\n", sign(c.Kind), c.Path)
			}
		}
	}
	return nil
}

// summary formats changes as "+added ~modified -deleted".
func summary(changes []disk.Change) string {
	var add, mod, del int
	for _, c := range changes {
		switch c.Kind {
		case disk.Added:
			add++
		case disk.Deleted:
			del++
		default:
			mod++
		}
	}
	return fmt.Sprintf("+%d ~%d -%d", add, mod, del)
}
