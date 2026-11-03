package config

import "strings"

// Path is where the repo config lives in the repo (slash-separated).
const Path = ".gcd/config.toml"

// ManagedDirs are the top-level repo directories gcd owns: the repo config
// and the Actions workflow. They never appear in the synced folder, a folder
// lacking them is not a deletion, and same-named folders a user creates
// locally are not uploaded. They change only through `gcd config edit` and
// `gcd workflow upgrade`.
var ManagedDirs = []string{".gcd", ".github"}

// IsManaged reports whether the slash-separated repo path lies in a managed
// directory. The match is case-insensitive, because on macOS and Windows
// ".GitHub/x" and ".github/x" are the same file.
func IsManaged(path string) bool {
	top, _, _ := strings.Cut(path, "/")
	for _, d := range ManagedDirs {
		if strings.EqualFold(top, d) {
			return true
		}
	}
	return false
}
