package config_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/config"
)

// specDefaults are the defaults the v1 spec (#1, #3) lists.
var specDefaults = config.Config{
	Format:            1,
	RetentionDays:     30,
	CompactSchedule:   "17 3 * * 0",
	MaxFileSize:       50 << 20,
	Debounce:          10 * time.Second,
	PollInterval:      60 * time.Second,
	MassDeletePercent: 20,
	MassDeleteCount:   50,
	Ignore:            []string{},
}

func TestEmptyConfigIsAllDefaults(t *testing.T) {
	got, err := config.Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, specDefaults) {
		t.Errorf("Parse(empty) =\n %+v\nwant\n %+v", got, specDefaults)
	}
	if !reflect.DeepEqual(config.Default(), specDefaults) {
		t.Errorf("Default() = %+v, want %+v", config.Default(), specDefaults)
	}
}

func TestDefaultFileParsesStrictlyToTheDefaults(t *testing.T) {
	got, err := config.ParseStrict(config.DefaultTOML())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, specDefaults) {
		t.Errorf("ParseStrict(DefaultTOML) =\n %+v\nwant\n %+v", got, specDefaults)
	}
}

func TestMissingKeysTakeDefaults(t *testing.T) {
	got, err := config.Parse([]byte("retention_days = 7\nignore = [\"*.tmp\", \"build/\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := specDefaults
	want.RetentionDays = 7
	want.Ignore = []string{"*.tmp", "build/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n %+v\nwant\n %+v", got, want)
	}
}

func TestEverySettingIsReadFromTheFile(t *testing.T) {
	got, err := config.ParseStrict([]byte(`
format = 1
retention_days = 90
compact_schedule = "0 4 * * *"
max_file_mb = 10
debounce_seconds = 3
poll_seconds = 300
mass_delete_percent = 50
mass_delete_count = 5
ignore = ["*.log"]
`))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		Format:            1,
		RetentionDays:     90,
		CompactSchedule:   "0 4 * * *",
		MaxFileSize:       10 << 20,
		Debounce:          3 * time.Second,
		PollInterval:      300 * time.Second,
		MassDeletePercent: 50,
		MassDeleteCount:   5,
		Ignore:            []string{"*.log"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got\n %+v\nwant\n %+v", got, want)
	}
}

func TestWindowsEditorOutputIsAccepted(t *testing.T) {
	// Notepad and friends may save CRLF line endings and a UTF-8 BOM.
	got, err := config.ParseStrict([]byte("\xef\xbb\xbfretention_days = 3\r\nmax_file_mb = 2\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got.RetentionDays != 3 || got.MaxFileSize != 2<<20 {
		t.Errorf("got %+v, want retention 3 and a 2 MiB limit", got)
	}
}

func TestNewerFormatIsRefusedBeforeOtherKeys(t *testing.T) {
	_, err := config.Parse([]byte("format = 2\nretention_days = \"a new meaning\"\n"))
	if !errors.Is(err, config.ErrFormatTooNew) {
		t.Fatalf("err = %v, want ErrFormatTooNew", err)
	}
	var fe *config.FormatError
	if !errors.As(err, &fe) || fe.Repo != 2 || fe.Supported != config.Format {
		t.Errorf("err = %#v, want FormatError{Repo: 2, Supported: %d}", err, config.Format)
	}
}

func TestUnknownKeysAreIgnoredButRejectedByStrict(t *testing.T) {
	data := []byte("retension_days = 7\n")
	if _, err := config.Parse(data); err != nil {
		t.Errorf("Parse: %v, want unknown keys ignored", err)
	}
	if _, err := config.ParseStrict(data); err == nil || !strings.Contains(err.Error(), "retension_days") {
		t.Errorf("ParseStrict error = %v, want it to name the unknown key", err)
	}
}

func TestInvalidConfigsAreRejected(t *testing.T) {
	for name, data := range map[string]string{
		"not toml":             "retention_days = = 3",
		"wrong type":           `retention_days = "30"`,
		"zero retention":       "retention_days = 0",
		"bad cron":             `compact_schedule = "weekly"`,
		"file limit over 100":  "max_file_mb = 200",
		"zero debounce":        "debounce_seconds = 0",
		"zero poll":            "poll_seconds = 0",
		"percent over 100":     "mass_delete_percent = 101",
		"zero delete count":    "mass_delete_count = 0",
		"empty ignore rule":    `ignore = [""]`,
		"format zero":          "format = 0",
		"format of wrong type": `format = "1"`,
	} {
		if _, err := config.Parse([]byte(data)); err == nil {
			t.Errorf("%s: Parse(%q) succeeded, want an error", name, data)
		}
	}
}

func TestManagedPaths(t *testing.T) {
	for path, want := range map[string]bool{
		".gcd/config.toml":          true,
		".github/workflows/gcd.yml": true,
		".GitHub/workflows/x.yml":   true,
		".gcdignore":                false,
		"notes/.gcd/x":              false,
		"github/x":                  false,
	} {
		if got := config.IsManaged(path); got != want {
			t.Errorf("IsManaged(%q) = %v, want %v", path, got, want)
		}
	}
}
