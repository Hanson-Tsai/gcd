package scan

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Reason says why a path is not synced.
type Reason int

const (
	// BadChar: a name holds a character Windows does not allow
	// (< > : " \ | ? * or a control character), or is not valid UTF-8.
	BadChar Reason = iota + 1
	// ReservedName: a name is a device name Windows reserves (CON, NUL,
	// COM1, LPT1, ...), with or without an extension.
	ReservedName
	// BadEnding: a name ends with a space or a dot, which Windows drops.
	BadEnding
	// CaseCollision: the path differs from another path only in letter case
	// or Unicode normalization, in the file name or any folder above it, so
	// the two would be one file on Windows and macOS.
	CaseCollision
	// TooLarge: the file is larger than the repo config's max_file_mb.
	TooLarge
	// NotRegular: a symlink or another special file; only regular files sync.
	NotRegular
)

func (r Reason) String() string {
	switch r {
	case BadChar:
		return "bad character"
	case ReservedName:
		return "reserved name"
	case BadEnding:
		return "bad ending"
	case CaseCollision:
		return "case collision"
	case TooLarge:
		return "too large"
	case NotRegular:
		return "not a regular file"
	}
	return "unknown"
}

// Rejection is a path the sync leaves alone, with the reason.
type Rejection struct {
	Path   string // slash-separated; a folder when a folder name is at fault
	Reason Reason
	Detail string // human-readable explanation
	// Remote: the path exists only on the remote (another client pushed it).
	// It is not downloaded, and the remote copy is kept.
	Remote bool
}

func (r Rejection) String() string {
	where := ""
	if r.Remote {
		where = " (on the remote)"
	}
	return fmt.Sprintf("%s%s: %s", r.Path, where, r.Detail)
}

// CheckPath applies the file-name policy to every segment of the
// slash-separated path p. It returns the Rejection when p breaks it, else
// nil.
func CheckPath(p string) *Rejection {
	if reason, detail, bad := checkPath(p); bad {
		return &Rejection{Path: p, Reason: reason, Detail: detail}
	}
	return nil
}

// checkPath applies the file-name policy to every segment of a
// slash-separated path.
func checkPath(p string) (Reason, string, bool) {
	for _, name := range strings.Split(p, "/") {
		if r, d, bad := checkName(name); bad {
			return r, d, true
		}
	}
	return 0, "", false
}

// checkName applies the file-name policy to one file or folder name. The
// policy is Windows' (the strictest of the three platforms) and holds on
// every OS, so whatever one machine uploads, every other can write.
func checkName(name string) (Reason, string, bool) {
	if !utf8.ValidString(name) {
		return BadChar, "name is not valid UTF-8", true
	}
	for _, c := range name {
		if c < 0x20 {
			return BadChar, fmt.Sprintf("name contains control character U+%04X, which Windows does not allow", c), true
		}
		if strings.ContainsRune(`<>:"\|?*`, c) {
			return BadChar, fmt.Sprintf("name contains %q, which Windows does not allow", c), true
		}
	}
	if strings.HasSuffix(name, " ") {
		return BadEnding, "name ends with a space, which Windows does not allow", true
	}
	if strings.HasSuffix(name, ".") {
		return BadEnding, "name ends with a dot, which Windows does not allow", true
	}
	base, _, _ := strings.Cut(name, ".")
	base = strings.TrimRight(base, " ")
	if reserved[strings.ToUpper(base)] {
		return ReservedName, fmt.Sprintf("%q is a reserved device name on Windows", base), true
	}
	return 0, "", false
}

// reserved holds Windows' reserved device names (upper case). COM0 and LPT0
// are included to be safe; Windows 10 and older reserve them even with an
// extension, which is why "nul.txt" is rejected too.
var reserved = func() map[string]bool {
	m := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}
	for _, d := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"} {
		m["COM"+d], m["LPT"+d] = true, true
	}
	return m
}()

// Collisions finds paths that would be the same file as another listed path
// on a case-insensitive, normalization-insensitive file system (Windows,
// macOS): equal after lower-casing and Unicode NFC, in the name or in any
// folder above it. It maps each such path to a message naming what it
// collides with. Every member of a colliding group is returned.
func Collisions(paths []string) map[string]string {
	groups := map[string]map[string]bool{} // folded prefix -> actual prefixes
	for _, p := range paths {
		forPrefixes(p, func(prefix string) {
			k := fold(prefix)
			if groups[k] == nil {
				groups[k] = map[string]bool{}
			}
			groups[k][prefix] = true
		})
	}
	out := map[string]string{}
	for _, p := range paths {
		forPrefixes(p, func(prefix string) {
			g := groups[fold(prefix)]
			if _, done := out[p]; done || len(g) < 2 {
				return
			}
			var others []string
			for o := range g {
				if o != prefix {
					others = append(others, o)
				}
			}
			sort.Strings(others)
			out[p] = fmt.Sprintf("%q and %q differ only in letter case or Unicode form, so they would be one file on Windows and macOS",
				prefix, others[0])
		})
	}
	return out
}

// forPrefixes calls f with every folder above p and with p itself.
func forPrefixes(p string, f func(string)) {
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			f(p[:i])
		}
	}
	f(p)
}

func fold(s string) string {
	return norm.NFC.String(strings.ToLower(norm.NFC.String(s)))
}
