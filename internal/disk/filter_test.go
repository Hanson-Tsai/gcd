package disk_test

import (
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/scan"
)

// File filters and cross-platform file-name rules (ticket #6).

func TestBuiltInIgnoresAreNotSynced(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	for _, p := range []string{
		".DS_Store", "sub/.DS_Store", "Thumbs.db", "desktop.ini",
		"~$report.docx", ".notes.md.swp", "notes.md~", "._notes.md",
		".~lock.sheet.ods#", "Icon\r", ".gcd-tmp-123",
	} {
		if runtime.GOOS == "windows" && strings.ContainsRune(p, '\r') {
			continue // not a valid Windows name in the first place
		}
		a.Write(t, p, "clutter")
	}
	a.Write(t, "notes.md", "keep")
	a.Write(t, "sub/plan.md", "keep too")

	res := a.Sync(t)

	remote.AssertFiles(t, map[string]string{"notes.md": "keep", "sub/plan.md": "keep too"})
	if len(res.Rejected) != 0 {
		t.Errorf("ignored files reported as rejected: %+v", res.Rejected)
	}
}

func TestRepoConfigAndGcdignoreRulesAreApplied(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	cfg := "ignore = [\"*.log\", \"cache/\"]\n"
	remote.Commit(t, map[string]string{".gcd/config.toml": cfg}, "config")
	kept := map[string]string{
		".gcdignore":        "# build output\nbuild/\n!keep.log\n/top-only.txt\n",
		"docs/.gcdignore":   "secret.txt\r\n*.bak\r\n",
		"keep.log":          "re-included",
		"docs/top-only.txt": "anchored rule only matches at the root",
		"secret.txt":        "nested rule only applies below docs/",
		"notes.md":          "plain",
		"builder/notes.md":  "build/ does not match builder/",
	}
	for p, c := range kept {
		a.Write(t, p, c)
	}
	for _, p := range []string{
		"app.log", "docs/deep/app.log", "cache/x", "docs/cache/y", "build/out.bin",
		"top-only.txt", "docs/secret.txt", "docs/old.bak", "docs/deep/older.bak",
	} {
		a.Write(t, p, "ignored")
	}

	res := a.Sync(t)

	want := map[string]string{".gcd/config.toml": cfg}
	for p, c := range kept {
		want[p] = c
	}
	remote.AssertFiles(t, want)
	if len(res.Rejected) != 0 {
		t.Errorf("ignored files reported as rejected: %+v", res.Rejected)
	}
	b.Sync(t)
	b.AssertFiles(t, kept)
}

func TestIgnoredPathOnTheRemoteIsNeitherDownloadedNorDeleted(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{
		"debug.log": "pushed before the rule existed",
		"notes.md":  "n",
		".DS_Store": "pushed by another client",
		"sub/x.tmp": "t",
	}, "other client")
	remote.Commit(t, map[string]string{".gcd/config.toml": "ignore = [\"*.log\", \"sub/\"]\n"}, "config")
	want := remote.Files(t)

	a.Sync(t)
	a.AssertFiles(t, map[string]string{"notes.md": "n"})

	a.Write(t, "debug.log", "local log")
	a.Write(t, "notes.md", "n2")
	a.Sync(t)
	want["notes.md"] = "n2"
	remote.AssertFiles(t, want)
	a.AssertFiles(t, map[string]string{"notes.md": "n2", "debug.log": "local log"})
}

func TestPathIsSyncedAgainWhenItsIgnoreRuleIsRemoved(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	a.Write(t, "data.csv", "v1")
	a.Sync(t)
	b.Sync(t)

	remote.Commit(t, map[string]string{".gcd/config.toml": "ignore = [\"*.csv\"]\n"}, "ignore csv")
	b.Remove(t, "data.csv") // deleted while ignored: not a deletion of the remote file
	b.Sync(t)
	a.Write(t, "data.csv", "v2 while ignored")
	a.Sync(t)
	remote.AssertFiles(t, map[string]string{".gcd/config.toml": "ignore = [\"*.csv\"]\n", "data.csv": "v1"})

	remote.Commit(t, map[string]string{".gcd/config.toml": "ignore = []\n"}, "unignore csv")
	a.Sync(t) // a's edit made while ignored is an ordinary local change now
	b.Sync(t)
	b.AssertFiles(t, map[string]string{"data.csv": "v2 while ignored"})
	remote.AssertFiles(t, map[string]string{".gcd/config.toml": "ignore = []\n", "data.csv": "v2 while ignored"})
}

func TestJoiningMachineAppliesTheSharedGcdignoreBeforeItsFirstUpload(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	a.Write(t, ".gcdignore", "private/\n")
	a.Write(t, "docs/.gcdignore", "*.key\n")
	a.Sync(t)

	b.Write(t, "private/diary.md", "dear diary")
	b.Write(t, "docs/api.key", "secret")
	b.Write(t, "notes.md", "shared")
	b.Sync(t)

	remote.AssertFiles(t, map[string]string{
		".gcdignore": "private/\n", "docs/.gcdignore": "*.key\n", "notes.md": "shared",
	})
}

// rejected is what a test expects of one entry of Result.Rejected.
type rejected struct {
	reason scan.Reason
	remote bool
}

func assertRejected(t *testing.T, res disk.Result, want map[string]rejected) {
	t.Helper()
	got := map[string]rejected{}
	for _, r := range res.Rejected {
		got[r.Path] = rejected{r.Reason, r.Remote}
		if r.Detail == "" {
			t.Errorf("rejection of %q has no explanation", r.Path)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rejected:\n got  %v\n want %v\n (%v)", got, want, res.Rejected)
	}
}

func TestNamesWindowsCannotHoldAreNotUploaded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("these names cannot be created on Windows")
	}
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	for _, p := range []string{
		"a:b.txt", "what?.md", "pipe|.txt", `back\slash.txt`, "ctl\x01.txt",
		"CON", "nul.txt", "Com1.log", "lpt9.tar.gz", "AUX .md",
		"dot.", "space ", "bad<dir>/inner.txt", "ok/trailing./x.md",
	} {
		a.Write(t, p, "bad")
	}
	a.Write(t, "ok.txt", "fine")
	a.Write(t, "CONsole.md", "fine: not a device name")
	a.Write(t, "nul-ish.txt", "fine")

	res := a.Sync(t)

	remote.AssertFiles(t, map[string]string{
		"ok.txt": "fine", "CONsole.md": "fine: not a device name", "nul-ish.txt": "fine",
	})
	assertRejected(t, res, map[string]rejected{
		"a:b.txt":        {scan.BadChar, false},
		"what?.md":       {scan.BadChar, false},
		"pipe|.txt":      {scan.BadChar, false},
		`back\slash.txt`: {scan.BadChar, false},
		"ctl\x01.txt":    {scan.BadChar, false},
		"CON":            {scan.ReservedName, false},
		"nul.txt":        {scan.ReservedName, false},
		"Com1.log":       {scan.ReservedName, false},
		"lpt9.tar.gz":    {scan.ReservedName, false},
		"AUX .md":        {scan.ReservedName, false},
		"dot.":           {scan.BadEnding, false},
		"space ":         {scan.BadEnding, false},
		"bad<dir>":       {scan.BadChar, false},
		"ok/trailing.":   {scan.BadEnding, false},
	})
}

func TestRemoteFilesWithBadNamesAreLeftAlone(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	planted := map[string]string{
		"a:b.txt": "x", "CON.md": "x", "trail.": "x", "dir?/inner.md": "x", "ok.md": "ok",
	}
	remote.Commit(t, planted, "pushed by another git client")

	res := a.Sync(t)

	a.AssertFiles(t, map[string]string{"ok.md": "ok"})
	assertRejected(t, res, map[string]rejected{
		"a:b.txt":       {scan.BadChar, true},
		"CON.md":        {scan.ReservedName, true},
		"trail.":        {scan.BadEnding, true},
		"dir?/inner.md": {scan.BadChar, true},
	})

	a.Write(t, "ok.md", "ok2")
	a.Sync(t)
	planted["ok.md"] = "ok2"
	remote.AssertFiles(t, planted)
}

// git refuses some names in an index when core.protectNTFS/protectHFS are on
// (Git for Windows also refuses every NTFS-invalid name, such as the ones the
// test above leaves on the remote). gcd never checks files out through git,
// so its tree building must still carry such files, whether downloaded or
// left on the remote. Forcing the guards on reproduces this on every OS.
func TestNamesGitGuardsInAnIndexStillSync(t *testing.T) {
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.protectntfs=true' 'core.protecthfs=true'")
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	planted := map[string]string{
		"GIT~1/notes.md":      "ntfs short name of .git",
		"hfs/.g\u200cit/x.md": "hfs ignores the zero-width char",
		"other.md":            "1",
	}
	remote.Commit(t, planted, "pushed by another git client")

	a.Sync(t)
	a.AssertFiles(t, planted)

	a.Write(t, "sub/GIT~1/up.md", "uploaded")
	a.Write(t, "other.md", "2")
	a.Sync(t)

	planted["sub/GIT~1/up.md"] = "uploaded"
	planted["other.md"] = "2"
	remote.AssertFiles(t, planted)
}

func TestPathsDifferingOnlyInCaseOnTheRemoteAreNotDownloaded(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	remote.Commit(t, map[string]string{
		"README.md": "1", "readme.md": "2",
		"Docs/a.md": "3", "docs/b.md": "4",
		"café.md": "nfd", "café.md": "nfc",
		"other.md": "ok",
	}, "pushed from Linux by another git client")
	before := remote.Head(t)

	res := a.Sync(t)

	a.AssertFiles(t, map[string]string{"other.md": "ok"})
	assertRejected(t, res, map[string]rejected{
		"README.md": {scan.CaseCollision, true}, "readme.md": {scan.CaseCollision, true},
		"Docs/a.md": {scan.CaseCollision, true}, "docs/b.md": {scan.CaseCollision, true},
		"café.md": {scan.CaseCollision, true}, "café.md": {scan.CaseCollision, true},
	})
	if remote.Head(t) != before {
		t.Error("remote changed")
	}
}

func TestLocalFileCollidingInCaseWithARemoteFileIsLeftAlone(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	a.Write(t, "Notes/plan.md", "a's plan")
	a.Sync(t)
	b.Write(t, "notes/todo.md", "b's todo")

	res := b.Sync(t)

	b.AssertFiles(t, map[string]string{"notes/todo.md": "b's todo"})
	remote.AssertFiles(t, map[string]string{"Notes/plan.md": "a's plan"})
	assertRejected(t, res, map[string]rejected{
		"Notes/plan.md": {scan.CaseCollision, true},
		"notes/todo.md": {scan.CaseCollision, false},
	})

	// The user resolves it by moving the file into a's folder.
	if err := os.RemoveAll(b.Path("notes")); err != nil {
		t.Fatal(err)
	}
	b.Write(t, "Notes/todo.md", "b's todo")
	b.Sync(t)
	b.AssertFiles(t, map[string]string{"Notes/plan.md": "a's plan", "Notes/todo.md": "b's todo"})
}

func TestLocalPathsDifferingOnlyInCaseAreNotUploaded(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "a.md", "lower")
	if _, err := os.Stat(a.Path("A.md")); err == nil {
		t.Skip("case-insensitive file system: such paths cannot exist side by side")
	}
	a.Write(t, "A.md", "upper")
	a.Write(t, "b.md", "fine")

	res := a.Sync(t)

	remote.AssertFiles(t, map[string]string{"b.md": "fine"})
	assertRejected(t, res, map[string]rejected{
		"a.md": {scan.CaseCollision, false}, "A.md": {scan.CaseCollision, false},
	})
}

func TestFilesOverTheSizeLimitAreNotSynced(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	cfg := "max_file_mb = 1\n"
	remote.Commit(t, map[string]string{".gcd/config.toml": cfg}, "config")
	const mib = 1 << 20
	a.Write(t, "edge.bin", strings.Repeat("e", mib))
	a.Write(t, "big.bin", strings.Repeat("b", mib+1))
	a.Write(t, "grows.bin", "small")
	a.Sync(t)
	remote.AssertFiles(t, map[string]string{
		".gcd/config.toml": cfg, "edge.bin": strings.Repeat("e", mib), "grows.bin": "small",
	})

	a.Write(t, "grows.bin", strings.Repeat("g", 2*mib))
	res := a.Sync(t)

	remote.AssertFiles(t, map[string]string{
		".gcd/config.toml": cfg, "edge.bin": strings.Repeat("e", mib), "grows.bin": "small",
	})
	assertRejected(t, res, map[string]rejected{
		"big.bin": {scan.TooLarge, false}, "grows.bin": {scan.TooLarge, false},
	})
	if d := res.Rejected[0].Detail; !strings.Contains(d, "1 MiB") {
		t.Errorf("detail %q does not name the limit", d)
	}
}

func TestSymlinksAreNotSynced(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	a.Write(t, "notes.md", "n")
	a.Write(t, "sub/x.md", "x")
	if err := os.Symlink("notes.md", a.Path("link.md")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	if err := os.Symlink("sub", a.Path("linkdir")); err != nil {
		t.Fatal(err)
	}
	remote.CommitMode(t, "remote-link", "120000", "notes.md", "symlink from another git client")
	b.Write(t, "link.md", "b's real file") // same path as a's symlink

	res := a.Sync(t)
	b.Sync(t)

	remote.AssertFiles(t, map[string]string{
		"notes.md": "n", "sub/x.md": "x", "remote-link": "notes.md", "link.md": "b's real file",
	})
	assertRejected(t, res, map[string]rejected{
		"link.md": {scan.NotRegular, false}, "linkdir": {scan.NotRegular, false},
		"remote-link": {scan.NotRegular, true},
	})
	a.Sync(t)
	if target, err := os.Readlink(a.Path("link.md")); err != nil || target != "notes.md" {
		t.Errorf("a's symlink was replaced: %q, %v", target, err)
	}
	b.AssertFiles(t, map[string]string{"notes.md": "n", "sub/x.md": "x", "link.md": "b's real file"})
}

func TestExecutableBitIsNotSynced(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	a.Write(t, "run.sh", "#!/bin/sh\n")
	if err := os.Chmod(a.Path("run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	remote.CommitMode(t, "tool.sh", "100755", "#!/bin/sh\n", "executable from another git client")

	a.Sync(t)
	b.Sync(t)

	if m := remote.Mode(t, "run.sh"); m != "100644" {
		t.Errorf("run.sh uploaded with mode %s, want 100644", m)
	}
	b.AssertFiles(t, map[string]string{"run.sh": "#!/bin/sh\n", "tool.sh": "#!/bin/sh\n"})
	if runtime.GOOS != "windows" {
		for _, p := range []string{"run.sh", "tool.sh"} {
			if fi, err := os.Stat(b.Path(p)); err != nil || fi.Mode().Perm()&0o111 != 0 {
				t.Errorf("%s downloaded executable: %v %v", p, fi.Mode(), err)
			}
		}
	}
}

func TestGitattributesInTheFolderDoNotConvertLineEndings(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 2)
	a, b := devs[0], devs[1]
	files := map[string]string{
		".gitattributes": "* text eol=crlf\n",
		"lf.txt":         "one\ntwo\n",
		"crlf.txt":       "one\r\ntwo\r\n",
	}
	for p, c := range files {
		a.Write(t, p, c)
	}
	a.Sync(t)
	b.Sync(t)
	remote.AssertFiles(t, files)
	b.AssertFiles(t, files)
}
