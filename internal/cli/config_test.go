package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/cli"
	"github.com/Hanson-Tsai/gcd/internal/config"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
	"github.com/Hanson-Tsai/gcd/internal/registry"
)

// TestMain lets the test binary double as $EDITOR: when GCD_TEST_EDITOR is
// set it overwrites the file it is given with GCD_TEST_EDITOR_CONTENT and
// exits, like a user editing and saving.
func TestMain(m *testing.M) {
	if os.Getenv("GCD_TEST_EDITOR") != "" {
		file := os.Args[len(os.Args)-1]
		if err := os.WriteFile(file, []byte(os.Getenv("GCD_TEST_EDITOR_CONTENT")), 0o644); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// useEditor makes `gcd config edit` save content as the edited config.
func useEditor(t *testing.T, content string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", self)
	t.Setenv("GCD_TEST_EDITOR", "1")
	t.Setenv("GCD_TEST_EDITOR_CONTENT", content)
	tmp := t.TempDir() // rejected edits are kept in a temp file
	for _, v := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(v, tmp)
	}
}

// gcdFails runs gcd expecting a non-zero exit and returns stdout+stderr.
func (m machine) gcdFails(t *testing.T, args ...string) string {
	t.Helper()
	t.Setenv(registry.HomeEnv, m.home)
	var out bytes.Buffer
	if code := cli.Run(context.Background(), args, &out, &out); code == 0 {
		t.Fatalf("gcd %v exited 0, want failure: %s", args, out.String())
	}
	return out.String()
}

func newMachine(t *testing.T, name string) machine {
	root := t.TempDir()
	return machine{home: filepath.Join(root, name+"-home"), folder: filepath.Join(root, name+"-folder")}
}

func TestInitWritesTheDefaultRepoConfigOnce(t *testing.T) {
	remote := fixture.NewRemote(t)
	a, b := newMachine(t, "a"), newMachine(t, "b")

	a.gcd(t, "init", remote.URL, a.folder)
	raw, ok := remote.Files(t)[config.Path]
	if !ok {
		t.Fatalf("init wrote no %s; remote has %q", config.Path, remote.Files(t))
	}
	if cfg, err := config.ParseStrict([]byte(raw)); err != nil || cfg.RetentionDays != 30 {
		t.Errorf("written config parses to %+v, %v; want the defaults", cfg, err)
	}
	head := remote.Head(t)

	b.gcd(t, "init", remote.URL, b.folder)
	if got := remote.Head(t); got != head {
		t.Errorf("second init changed the remote (%s -> %s); the config already existed", head, got)
	}
	b.gcd(t, "sync")
	if _, err := os.Stat(filepath.Join(b.folder, ".gcd")); !os.IsNotExist(err) {
		t.Errorf(".gcd appeared in the synced folder: %v", err)
	}
}

func TestInitRefusesARepoWithANewerFormat(t *testing.T) {
	remote := fixture.NewRemote(t)
	remote.Commit(t, map[string]string{config.Path: "format = 99\n"}, "future")
	a := newMachine(t, "a")

	out := a.gcdFails(t, "init", remote.URL, a.folder)
	if !strings.Contains(out, "upgrade gcd") {
		t.Errorf("init output %q does not ask to upgrade gcd", out)
	}
}

func TestSyncTellsTheUserToUpgradeOnANewerFormat(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	remote.Commit(t, map[string]string{config.Path: "format = 99\n"}, "future")

	out := a.gcdFails(t, "sync")
	if !strings.Contains(out, "upgrade gcd") {
		t.Errorf("sync output %q does not ask to upgrade gcd", out)
	}
}

func TestConfigEditCommitsAValidConfig(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	edited := "format = 1\nretention_days = 7\n"
	useEditor(t, edited)

	a.gcd(t, "config", "edit")

	if got := remote.Files(t)[config.Path]; got != edited {
		t.Errorf("remote config = %q, want %q", got, edited)
	}
	if subj := remote.Log(t)[0].Subject; !strings.Contains(subj, "config") {
		t.Errorf("commit subject %q does not mention the config", subj)
	}
}

func TestConfigEditRejectsAnInvalidConfigWithoutCommitting(t *testing.T) {
	for name, edited := range map[string]string{
		"bad value":   "retention_days = 0\n",
		"typo in key": "retension_days = 7\n",
		"not toml":    "retention_days = \n",
	} {
		t.Run(name, func(t *testing.T) {
			remote := fixture.NewRemote(t)
			a := newMachine(t, "a")
			a.gcd(t, "init", remote.URL, a.folder)
			head := remote.Head(t)
			useEditor(t, edited)

			out := a.gcdFails(t, "config", "edit")

			if !strings.Contains(out, "retention_days") && !strings.Contains(out, "retension_days") {
				t.Errorf("output %q does not show what is wrong", out)
			}
			if got := remote.Head(t); got != head {
				t.Errorf("invalid config was committed (%s -> %s)", head, got)
			}
		})
	}
}

func TestConfigEditWithoutChangesCommitsNothing(t *testing.T) {
	remote := fixture.NewRemote(t)
	a := newMachine(t, "a")
	a.gcd(t, "init", remote.URL, a.folder)
	head := remote.Head(t)
	useEditor(t, string(config.DefaultTOML()))

	a.gcd(t, "config", "edit")

	if got := remote.Head(t); got != head {
		t.Errorf("unchanged config was committed (%s -> %s)", head, got)
	}
}
