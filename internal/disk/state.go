package disk

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/apply"
)

const stateFile = "state.json"

// state is the per-Disk local state, stored as <StateDir>/state.json.
type state struct {
	BaseTree string    `json:"base_tree"` // tree hash of the last successful sync
	LastSync time.Time `json:"last_sync"`
}

func loadState(dir string) (state, error) {
	var st state
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil // never synced: base is the empty tree
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

func saveState(dir string, st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return apply.WriteFile(filepath.Join(dir, stateFile), strings.NewReader(string(b)+"\n"))
}
