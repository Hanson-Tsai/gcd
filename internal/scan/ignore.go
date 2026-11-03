package scan

import (
	"path"
	"strings"
)

// builtinIgnore is ignored in every folder before any other rule, so a
// .gcdignore or repo config rule can still re-include (!pattern) one of them.
var builtinIgnore = []string{
	TempPrefix + "*", // gcd's own temp files (package apply)
	// macOS
	".DS_Store", "._*", "Icon\r", ".Spotlight-V100/", ".Trashes/", ".fseventsd/", ".TemporaryItems/",
	// Windows
	"Thumbs.db", "ehthumbs.db", "desktop.ini", "$RECYCLE.BIN/", "System Volume Information/",
	// Office and LibreOffice lock files
	"~$*", ".~lock.*#",
	// editor swap, backup and lock files (vim, emacs)
	"*.swp", "*.swo", "*.swx", "*~", "\\#*#", ".#*",
}

// IgnoreFile is the name of the per-directory ignore file (.gitignore syntax).
const IgnoreFile = ".gcdignore"

// ignorer holds ignore patterns: built-ins, then repo config rules, then
// .gcdignore files. As in git, a pattern from a deeper .gcdignore beats one
// from a shallower file (built-ins and config rules count as the root), and
// among those the last matching pattern decides.
type ignorer struct{ patterns []pattern }

// pattern is one parsed .gitignore line.
type pattern struct {
	domain   []string // directory of the ignore file, as path segments ("" root = none)
	segs     []string // pattern split on "/"
	anchored bool     // contains a "/" other than a trailing one: match from domain
	dirOnly  bool     // trailing "/": matches directories only
	negate   bool     // leading "!": re-include
}

// add parses the lines of an ignore file in directory dir (slash-separated,
// "" for the folder root).
func (ig *ignorer) add(dir string, lines []string) {
	var domain []string
	if dir != "" {
		domain = strings.Split(dir, "/")
	}
	for _, l := range lines {
		if p, ok := parsePattern(l, domain); ok {
			ig.patterns = append(ig.patterns, p)
		}
	}
}

func parsePattern(line string, domain []string) (pattern, bool) {
	line = trimTrailingSpaces(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return pattern{}, false
	}
	p := pattern{domain: domain}
	if strings.HasPrefix(line, "!") {
		p.negate, line = true, line[1:]
	}
	if strings.HasSuffix(line, "/") {
		p.dirOnly, line = true, strings.TrimSuffix(line, "/")
	}
	if strings.Contains(line, "/") {
		p.anchored, line = true, strings.TrimPrefix(line, "/")
	}
	if line == "" {
		return pattern{}, false
	}
	p.segs = strings.Split(line, "/")
	for i, s := range p.segs {
		p.segs[i] = gitClass(s)
	}
	return p, true
}

// trimTrailingSpaces drops trailing spaces unless escaped with a backslash.
func trimTrailingSpaces(s string) string {
	for strings.HasSuffix(s, " ") && !strings.HasSuffix(s, "\\ ") {
		s = s[:len(s)-1]
	}
	return s
}

// gitClass rewrites git's negated character class "[!...]" to the "[^...]"
// that path.Match understands.
func gitClass(s string) string {
	return strings.ReplaceAll(s, "[!", "[^")
}

// ignored reports whether rel (slash-separated, relative to the folder) is
// ignored, looking at rel itself only. Callers walk from the root down and
// stop at an ignored directory, which is how git treats excluded parents.
func (ig *ignorer) ignored(rel string, isDir bool) bool {
	segs := strings.Split(rel, "/")
	ignored, depth := false, -1
	for _, p := range ig.patterns {
		if len(p.domain) >= depth && p.match(segs, isDir) {
			ignored, depth = !p.negate, len(p.domain)
		}
	}
	return ignored
}

func (p pattern) match(segs []string, isDir bool) bool {
	if p.dirOnly && !isDir {
		return false
	}
	if len(segs) <= len(p.domain) {
		return false
	}
	for i, d := range p.domain {
		if segs[i] != d {
			return false
		}
	}
	segs = segs[len(p.domain):]
	if !p.anchored {
		return segMatch(p.segs[0], segs[len(segs)-1])
	}
	return matchSegs(p.segs, segs)
}

// matchSegs matches pattern segments against path segments. A "**" segment
// matches any number of path segments; a trailing "**" at least one.
func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		min := 0
		if len(pat) == 1 {
			min = 1
		}
		for n := min; n <= len(segs); n++ {
			if matchSegs(pat[1:], segs[n:]) {
				return true
			}
		}
		return false
	}
	return len(segs) > 0 && segMatch(pat[0], segs[0]) && matchSegs(pat[1:], segs[1:])
}

func segMatch(pat, name string) bool {
	ok, err := path.Match(pat, name)
	return err == nil && ok
}
