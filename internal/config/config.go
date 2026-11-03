// Package config is the repo config: a TOML file at .gcd/config.toml inside
// the synced repo, shared by every machine. It parses and validates the file,
// fills in defaults for missing keys and checks the repo format version
// against the one this gcd supports.
//
// A missing file means "all defaults". Unknown keys are ignored when syncing
// (so a newer gcd may add optional keys without a format bump) but rejected
// by ParseStrict, which `gcd config edit` uses to catch typos.
package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/Hanson-Tsai/gcd/internal/workflow"
)

// Format is the newest repo format this gcd understands. Bump it only when the
// repo layout or the meaning of existing data changes, so that older clients
// refuse to sync instead of writing with the wrong rules.
const Format = 1

// ErrFormatTooNew is matched (errors.Is) by a *FormatError.
var ErrFormatTooNew = errors.New("repo format too new")

// FormatError reports a repo whose format is newer than this gcd supports.
type FormatError struct {
	Repo      int // format declared by the repo config
	Supported int // Format of this gcd
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("repo format %d is newer than this gcd supports (format %d); upgrade gcd to use this repo",
		e.Repo, e.Supported)
}

func (e *FormatError) Is(target error) bool { return target == ErrFormatTooNew }

// Config is the parsed repo config with defaults applied.
type Config struct {
	Format            int
	RetentionDays     int           // full history kept by compaction
	CompactSchedule   string        // cron expression (UTC) for the compaction workflow
	MaxFileSize       int64         // bytes; larger files are not synced
	Debounce          time.Duration // gcd watch: quiet time before syncing local changes
	PollInterval      time.Duration // gcd watch: interval between remote checks
	MassDeletePercent int           // a sync deleting more than this % of files needs --yes
	MassDeleteCount   int           // ... or more than this many files
	Ignore            []string      // extra ignore rules, .gitignore syntax
}

// file is the on-disk shape; field names are the TOML keys.
type file struct {
	Format            int      `toml:"format"`
	RetentionDays     int      `toml:"retention_days"`
	CompactSchedule   string   `toml:"compact_schedule"`
	MaxFileMB         int64    `toml:"max_file_mb"`
	DebounceSeconds   int      `toml:"debounce_seconds"`
	PollSeconds       int      `toml:"poll_seconds"`
	MassDeletePercent int      `toml:"mass_delete_percent"`
	MassDeleteCount   int      `toml:"mass_delete_count"`
	Ignore            []string `toml:"ignore"`
}

const mib = 1 << 20

func defaultFile() file {
	return file{
		Format:            Format,
		RetentionDays:     30,
		CompactSchedule:   "17 3 * * 0", // weekly, Sunday 03:17 UTC (off the hour, as GitHub advises)
		MaxFileMB:         50,
		DebounceSeconds:   10,
		PollSeconds:       60,
		MassDeletePercent: 20,
		MassDeleteCount:   50,
		Ignore:            []string{},
	}
}

// Default returns the config used when the repo has no config file.
func Default() Config {
	c, err := defaultFile().config()
	if err != nil {
		panic(err) // the defaults are valid by construction
	}
	return c
}

// DefaultTOML is the commented config file `gcd init` writes into a repo.
func DefaultTOML() []byte {
	return []byte(defaultTOML)
}

const defaultTOML = `# gcd repo config, shared by every machine that syncs this repo.
# Change it with ` + "`gcd config edit`" + `, which checks it before committing.

# Repo format version. Do not edit.
format = 1

# Days of full history kept when history is compacted; older history is
# squashed into one snapshot.
retention_days = 30

# When the compaction workflow runs: a cron expression in UTC.
# Default: weekly, Sunday 03:17.
compact_schedule = "17 3 * * 0"

# Files larger than this many MiB are not synced (GitHub rejects > 100 MiB).
max_file_mb = 50

# gcd watch: seconds without local changes before syncing them.
debounce_seconds = 10

# gcd watch: seconds between checks for changes on the remote.
poll_seconds = 60

# A sync that would delete more than mass_delete_percent % of the files (and
# more than 5 files), or more than mass_delete_count files, stops and asks
# for ` + "`gcd sync --yes`" + `.
mass_delete_percent = 20
mass_delete_count = 50

# Extra ignore rules (.gitignore syntax), on top of the built-in list and
# the .gcdignore file in the synced folder. Example: ["*.tmp", "build/"]
ignore = []
`

// Parse parses a repo config, applying defaults for missing keys and ignoring
// unknown keys. A format newer than Format returns a *FormatError before any
// other key is looked at, since a newer format may change their meaning.
func Parse(data []byte) (Config, error) {
	return parse(data, false)
}

// ParseStrict is Parse that also rejects unknown keys (typos), reported
// together with any invalid values.
func ParseStrict(data []byte) (Config, error) {
	return parse(data, true)
}

func parse(data []byte, strict bool) (Config, error) {
	var v struct {
		Format int `toml:"format"`
	}
	md, err := toml.Decode(string(data), &v)
	if err != nil {
		return Config{}, err
	}
	if md.IsDefined("format") {
		if v.Format < 1 {
			return Config{}, fmt.Errorf("format must be 1 or more, got %d", v.Format)
		}
		if v.Format > Format {
			return Config{}, &FormatError{Repo: v.Format, Supported: Format}
		}
	}

	f := defaultFile()
	if md, err = toml.Decode(string(data), &f); err != nil {
		return Config{}, err
	}
	var unknown error
	if und := md.Undecoded(); strict && len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		sort.Strings(keys)
		unknown = fmt.Errorf("unknown key(s): %s", strings.Join(keys, ", "))
	}
	c, err := f.config()
	if err = errors.Join(unknown, err); err != nil {
		return Config{}, err
	}
	return c, nil
}

// config validates f and converts it to a Config.
func (f file) config() (Config, error) {
	var errs []error
	check := func(ok bool, format string, args ...any) {
		if !ok {
			errs = append(errs, fmt.Errorf(format, args...))
		}
	}
	check(f.RetentionDays >= 1, "retention_days must be at least 1, got %d", f.RetentionDays)
	if err := workflow.CheckSchedule(f.CompactSchedule); err != nil {
		errs = append(errs, err) // it goes verbatim into the compaction workflow
	}
	check(f.MaxFileMB >= 1 && f.MaxFileMB <= 100, "max_file_mb must be between 1 and 100, got %d", f.MaxFileMB)
	check(f.DebounceSeconds >= 1, "debounce_seconds must be at least 1, got %d", f.DebounceSeconds)
	check(f.PollSeconds >= 1, "poll_seconds must be at least 1, got %d", f.PollSeconds)
	check(f.MassDeletePercent >= 1 && f.MassDeletePercent <= 100,
		"mass_delete_percent must be between 1 and 100, got %d", f.MassDeletePercent)
	check(f.MassDeleteCount >= 1, "mass_delete_count must be at least 1, got %d", f.MassDeleteCount)
	for _, p := range f.Ignore {
		check(strings.TrimSpace(p) != "", "ignore must not contain empty rules")
	}
	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return Config{
		Format:            f.Format,
		RetentionDays:     f.RetentionDays,
		CompactSchedule:   strings.Join(strings.Fields(f.CompactSchedule), " "),
		MaxFileSize:       f.MaxFileMB * mib,
		Debounce:          time.Duration(f.DebounceSeconds) * time.Second,
		PollInterval:      time.Duration(f.PollSeconds) * time.Second,
		MassDeletePercent: f.MassDeletePercent,
		MassDeleteCount:   f.MassDeleteCount,
		Ignore:            append([]string{}, f.Ignore...),
	}, nil
}
