// Package scan walks a synced folder and produces its file list: slash-
// separated relative path -> content hash plus the size and mtime observed.
//
// Hashes are git blob hashes (SHA-1 of "blob <size>\x00<content>"), so they
// compare directly with tree entries read through gitstore.
//
// The scan applies the file rules:
//   - Ignore rules (.gitignore syntax): a built-in list of system and editor
//     clutter (and gcd's own temp files), then Rules.Ignore from the repo
//     config, then .gcdignore files from the folder root down. Ignored paths
//     are skipped silently, and an ignored folder is not entered.
//   - The file-name policy (see checkName), the size limit and "regular files
//     only": such paths are skipped and listed in Result.Rejected.
//
// Empty folders are never synced. Result.Exclude applies the same rules to a
// path from the remote or base tree, and Collisions finds case collisions;
// package disk combines them.
package scan

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TempPrefix starts the name of every temp file gcd writes into the folder
// (see package apply). Such files are always ignored.
const TempPrefix = ".gcd-tmp-"

// Entry is one local file as observed during the scan.
type Entry struct {
	Hash    string // git blob hash of the content
	Size    int64
	ModTime time.Time
}

// Rules are the repo config's settings that the scan applies.
type Rules struct {
	Ignore      []string // extra ignore rules (.gitignore syntax, relative to the folder root)
	MaxFileSize int64    // bytes; larger files are rejected (0 = no limit)
	// IgnoreFiles holds the content of .gcdignore files by folder
	// (slash-separated, "" for the root), used wherever the folder itself
	// has no .gcdignore. Package disk passes the remote's copies, so a
	// machine applies the shared rules before it has downloaded them.
	IgnoreFiles map[string][]byte
}

// Result is the outcome of scanning a folder.
type Result struct {
	Files map[string]Entry // slash-separated relative path -> entry
	// Rejected lists local files and folders that are not synced because of
	// their name, size or type, sorted by path. Ignored paths are not listed.
	Rejected []Rejection

	ignore   *ignorer
	rejected map[string]bool // Rejected paths
}

// Scan walks root, applies rules and hashes every regular file it keeps.
func Scan(root string, rules Rules) (Result, error) {
	res := Result{Files: map[string]Entry{}, ignore: &ignorer{}, rejected: map[string]bool{}}
	res.ignore.add("", builtinIgnore)
	res.ignore.add("", rules.Ignore)
	loaded := map[string]bool{}
	load := func(p, rel string) error {
		loaded[rel] = true
		content, err := os.ReadFile(filepath.Join(p, IgnoreFile))
		if errors.Is(err, fs.ErrNotExist) {
			content = rules.IgnoreFiles[rel]
		} else if err != nil {
			return err
		}
		res.ignore.add(rel, lines(content))
		return nil
	}
	reject := func(rel string, reason Reason, detail string) {
		res.Rejected = append(res.Rejected, Rejection{Path: rel, Reason: reason, Detail: detail})
		res.rejected[rel] = true
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return load(p, "")
		}
		if res.ignore.ignored(rel, d.IsDir()) {
			return skip(d)
		}
		if reason, detail, bad := checkName(d.Name()); bad {
			if d.IsDir() {
				detail = "folder " + detail
			}
			reject(rel, reason, detail)
			return skip(d)
		}
		if d.IsDir() {
			return load(p, rel)
		}
		if !d.Type().IsRegular() {
			kind := "special files are"
			if d.Type()&fs.ModeSymlink != 0 {
				kind = "symlinks are"
			}
			reject(rel, NotRegular, kind+" not synced")
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if rules.MaxFileSize > 0 && info.Size() > rules.MaxFileSize {
			reject(rel, TooLarge, fmt.Sprintf("%.1f MiB is over the %d MiB limit (max_file_mb in the repo config)",
				float64(info.Size())/(1<<20), rules.MaxFileSize>>20))
			return nil
		}
		hash, err := HashFile(p)
		if err != nil {
			return err
		}
		res.Files[rel] = Entry{Hash: hash, Size: info.Size(), ModTime: info.ModTime()}
		return nil
	})
	// Rules from folders missing locally still decide which remote paths
	// below them are left out (see Exclude).
	for rel, content := range rules.IgnoreFiles {
		if !loaded[rel] {
			res.ignore.add(rel, lines(content))
		}
	}
	sort.Slice(res.Rejected, func(i, j int) bool { return res.Rejected[i].Path < res.Rejected[j].Path })
	return res, err
}

// skip leaves out d: a folder is not entered.
func skip(d fs.DirEntry) error {
	if d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// lines splits an ignore file into lines, accepting CRLF line ends.
func lines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	return strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
}

// Exclude applies the scan's rules to path p of the remote or base tree. It
// reports whether the sync must leave p alone on every side, because p
//   - is ignored, or lies in an ignored folder;
//   - is, or lies in, a local file or folder the scan rejected; or
//   - breaks the file-name policy.
//
// Only the last case returns a Rejection to report (with Remote set); the
// others are silent or were reported by the scan already. Ignore rules come
// from the folder's .gcdignore files, not from the remote's copies.
func (r Result) Exclude(p string) (rej *Rejection, excluded bool) {
	for i := 0; i < len(p); i++ {
		if p[i] == '/' && (r.rejected[p[:i]] || r.ignore.ignored(p[:i], true)) {
			return nil, true
		}
	}
	if r.rejected[p] || r.ignore.ignored(p, false) {
		return nil, true
	}
	if reason, detail, bad := checkPath(p); bad {
		return &Rejection{Path: p, Reason: reason, Detail: detail, Remote: true}, true
	}
	return nil, false
}

// Changed reports whether the file at rel (slash-separated) differs from
// what the scan saw: it appeared, disappeared, or its size or mtime moved.
// A directory at rel counts as no file.
func (r Result) Changed(root, rel string) (bool, error) {
	e, scanned := r.Files[rel]
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) || (err == nil && info.IsDir()) {
		return scanned, nil
	}
	if err != nil {
		return false, err
	}
	return !scanned || info.Size() != e.Size || !info.ModTime().Equal(e.ModTime), nil
}

// HashFile returns the git blob hash of the file at path, byte-for-byte.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", info.Size())
	n, err := io.Copy(h, f)
	if err != nil {
		return "", err
	}
	if n != info.Size() {
		return "", fmt.Errorf("%s changed size while hashing", path)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
