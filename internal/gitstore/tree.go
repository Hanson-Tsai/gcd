package gitstore

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Trees are built without an index. git refuses to put some names into an
// index: with core.protectNTFS/protectHFS ("GIT~1", ".g‌it") and, on
// Windows, unconditionally for NTFS-invalid names such as "a:b.txt". gcd
// never checks files out through git, and a Disk must carry every name the
// remote holds, so it encodes tree objects itself and only asks git to store
// them.

// WriteTree builds a (nested) tree object from slash-separated path -> Entry
// and returns its hash. A path git could never hold in a tree (an empty, "."
// or ".." component, a ".git" component, or a file that is also a directory)
// is an error rather than being dropped, which would delete it remotely.
func (s *Store) WriteTree(ctx context.Context, entries map[string]Entry) (string, error) {
	root, err := newTreeDir(entries)
	if err != nil {
		return "", err
	}
	var raws [][]byte
	rootHash, err := root.encode(&raws)
	if err != nil {
		return "", err
	}
	// --literally: newer git fsck-checks objects in hash-object and would
	// refuse the very names this exists to carry.
	written, err := s.writeObjects(ctx, "tree", raws, "--literally")
	if err != nil {
		return "", err
	}
	for i, raw := range raws {
		if want := objectHash("tree", raw); written[i] != want {
			return "", fmt.Errorf("git stored tree %s, expected %s", written[i], want)
		}
	}
	return rootHash, nil
}

type treeDir struct {
	files map[string]Entry
	dirs  map[string]*treeDir
}

func newTreeDir(entries map[string]Entry) (*treeDir, error) {
	root := &treeDir{files: map[string]Entry{}, dirs: map[string]*treeDir{}}
	for p, e := range entries {
		parts := strings.Split(p, "/")
		for _, c := range parts {
			if c == "" || c == "." || c == ".." || strings.EqualFold(c, ".git") || strings.ContainsRune(c, 0) {
				return nil, fmt.Errorf("cannot store %q in a git tree: invalid path", p)
			}
		}
		d := root
		for _, c := range parts[:len(parts)-1] {
			if _, ok := d.files[c]; ok {
				return nil, fmt.Errorf("cannot store %q in a git tree: %q is a file", p, c)
			}
			sub := d.dirs[c]
			if sub == nil {
				sub = &treeDir{files: map[string]Entry{}, dirs: map[string]*treeDir{}}
				d.dirs[c] = sub
			}
			d = sub
		}
		name := parts[len(parts)-1]
		if _, ok := d.dirs[name]; ok {
			return nil, fmt.Errorf("cannot store %q in a git tree: it is also a directory", p)
		}
		d.files[name] = e
	}
	return root, nil
}

// encode appends the raw tree objects of d (children first) to raws and
// returns d's hash.
func (d *treeDir) encode(raws *[][]byte) (string, error) {
	type item struct{ name, sortKey, mode, hash string }
	items := make([]item, 0, len(d.files)+len(d.dirs))
	for name, e := range d.files {
		items = append(items, item{name, name, e.Mode, e.Hash})
	}
	for name, sub := range d.dirs {
		h, err := sub.encode(raws)
		if err != nil {
			return "", err
		}
		// git orders a tree's entries as if directory names ended in "/".
		items = append(items, item{name, name + "/", "40000", h})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].sortKey < items[j].sortKey })
	var raw bytes.Buffer
	for _, it := range items {
		bin, err := hex.DecodeString(it.hash)
		if err != nil || len(bin) != sha1.Size {
			return "", fmt.Errorf("bad object hash %q for %q", it.hash, it.name)
		}
		raw.WriteString(it.mode + " " + it.name + "\x00")
		raw.Write(bin)
	}
	*raws = append(*raws, raw.Bytes())
	return objectHash("tree", raw.Bytes()), nil
}

// objectHash computes the hash git gives a raw object of type typ (SHA-1,
// the object format of GitHub repos), without writing it.
func objectHash(typ string, raw []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", typ, len(raw))
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// writeObjects stores raw objects of type typ in one hash-object process and
// returns their hashes in order.
func (s *Store) writeObjects(ctx context.Context, typ string, raws [][]byte, flags ...string) ([]string, error) {
	if len(raws) == 0 {
		return nil, nil
	}
	dir, err := os.MkdirTemp(s.GitDir, "gcd-objects-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	var in strings.Builder
	for i, raw := range raws {
		p := filepath.Join(dir, strconv.Itoa(i))
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			return nil, err
		}
		in.WriteString(cQuote(p))
		in.WriteByte('\n')
	}
	args := append([]string{"hash-object", "-t", typ, "-w", "--no-filters", "--stdin-paths"}, flags...)
	out, err := s.git(ctx, strings.NewReader(in.String()), args...)
	if err != nil {
		return nil, err
	}
	hashes := strings.Fields(out)
	if len(hashes) != len(raws) {
		return nil, fmt.Errorf("hash-object returned %d hashes for %d %s objects", len(hashes), len(raws), typ)
	}
	return hashes, nil
}
