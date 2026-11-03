// Package scan walks a synced folder and produces its file list: slash-
// separated relative path -> content hash plus the size and mtime observed.
//
// Hashes are git blob hashes (SHA-1 of "blob <size>\x00<content>"), so they
// compare directly with tree entries read through gitstore.
//
// Only regular files are listed: symlinks, empty directories and other
// special files are never synced. Ignore rules, file-name policy and the
// large-file limit (ticket #6) belong here.
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
	"strings"
	"time"
)

// TempPrefix starts the name of every temp file gcd writes into the folder
// (see package apply). Such files are never listed.
const TempPrefix = ".gcd-tmp-"

// Entry is one local file as observed during the scan.
type Entry struct {
	Hash    string // git blob hash of the content
	Size    int64
	ModTime time.Time
}

// Result is the outcome of scanning a folder.
type Result struct {
	Files map[string]Entry // slash-separated relative path -> entry
}

// Scan walks root and hashes every regular file below it.
func Scan(root string) (Result, error) {
	res := Result{Files: map[string]Entry{}}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil // directories are walked; symlinks and specials are skipped
		}
		if strings.HasPrefix(d.Name(), TempPrefix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hash, err := HashFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		res.Files[filepath.ToSlash(rel)] = Entry{Hash: hash, Size: info.Size(), ModTime: info.ModTime()}
		return nil
	})
	return res, err
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
