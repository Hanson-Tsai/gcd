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
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

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
