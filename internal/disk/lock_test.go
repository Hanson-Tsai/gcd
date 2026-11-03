package disk_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/Hanson-Tsai/gcd/internal/disk"
	"github.com/Hanson-Tsai/gcd/internal/fixture"
)

// Environment for TestHelperProcessHoldsSyncLock, run as a child process.
const (
	envHolderFolder = "GCD_TEST_HOLDER_FOLDER"
	envHolderState  = "GCD_TEST_HOLDER_STATE"
	envHolderRemote = "GCD_TEST_HOLDER_REMOTE"
	holderReady     = "syncing"
)

// TestHelperProcessHoldsSyncLock is not a real test. Started as a child
// process, it begins a sync and hangs in the middle of it until killed.
func TestHelperProcessHoldsSyncLock(t *testing.T) {
	folder := os.Getenv(envHolderFolder)
	if folder == "" {
		t.Skip("helper process only")
	}
	d, err := disk.Open(disk.Options{
		Folder: folder, StateDir: os.Getenv(envHolderState), Remote: os.Getenv(envHolderRemote), Host: "holder",
	})
	if err != nil {
		t.Fatal(err)
	}
	disk.SetBeforePush(d, func() {
		os.Stdout.WriteString(holderReady + "\n")
		time.Sleep(time.Hour) // hang mid-sync until the parent kills us
	})
	_, err = d.Sync(context.Background(), disk.SyncOptions{})
	t.Fatalf("sync returned although it should hang: %v", err)
}

// startSyncInOtherProcess runs a sync of dev in a child process and returns
// once that sync holds the lock.
func startSyncInOtherProcess(t *testing.T, remote *fixture.Remote, dev *fixture.Device) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcessHoldsSyncLock$")
	cmd.Env = append(os.Environ(),
		envHolderFolder+"="+dev.Folder, envHolderState+"="+dev.StateDir, envHolderRemote+"="+remote.URL)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	ready := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if sc.Text() == holderReady {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("helper process exited before it started syncing")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("helper process did not start syncing within 30s")
	}
	return cmd
}

func TestSyncFailsFastWhileAnotherProcessSyncsAndRecoversAfterItDies(t *testing.T) {
	remote, devs := fixture.NewDevices(t, 1)
	a := devs[0]
	a.Write(t, "a.txt", "1")

	holder := startSyncInOtherProcess(t, remote, a)

	start := time.Now()
	_, err := a.Disk.Sync(context.Background(), disk.SyncOptions{})
	if !errors.Is(err, disk.ErrAlreadySyncing) {
		t.Fatalf("sync while another process syncs: err = %v, want ErrAlreadySyncing", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("busy sync took %v, want it to fail at once", d)
	}

	// The holder dies mid-sync and leaves its lock file behind.
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()

	// Windows releases a dead process's locks shortly after it exits, not
	// necessarily by the time Wait returns, so allow a brief grace period.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err = a.Disk.Sync(context.Background(), disk.SyncOptions{})
		if !errors.Is(err, disk.ErrAlreadySyncing) || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("sync after the other process died: %v", err)
	}
	remote.AssertFiles(t, map[string]string{"a.txt": "1"})
	a.AssertFiles(t, map[string]string{"a.txt": "1"})
}
