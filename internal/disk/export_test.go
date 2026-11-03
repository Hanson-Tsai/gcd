package disk

// SetBeforePush installs a hook that runs after the new commit is built and
// right before it is pushed. Tests use it to make another device push in
// between, so the push is rejected as non-fast-forward.
func SetBeforePush(d *Disk, f func()) { d.beforePush = f }

// SetAfterScan installs a hook that runs right after the folder scan, before
// anything is committed or written: the window in which a user may still be
// editing files.
func SetAfterScan(d *Disk, f func()) { d.afterScan = f }
