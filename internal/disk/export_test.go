package disk

// SetBeforePush installs a hook that runs after the new commit is built and
// right before it is pushed. Tests use it to make another device push in
// between, so the push is rejected as non-fast-forward.
func SetBeforePush(d *Disk, f func()) { d.beforePush = f }
