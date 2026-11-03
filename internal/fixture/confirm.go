package fixture

import (
	"context"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/disk"
)

// SyncYes runs one sync with the mass-delete confirmation given (gcd sync
// --yes), failing the test on error. Tests use it where a sync deletes every
// synced file on one side, which always needs confirmation.
func (d *Device) SyncYes(t testing.TB) disk.Result {
	t.Helper()
	res, err := d.Disk.Sync(context.Background(), disk.SyncOptions{Yes: true})
	if err != nil {
		t.Fatalf("sync --yes on %s: %v", d.Host, err)
	}
	return res
}
