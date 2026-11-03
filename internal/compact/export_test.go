package compact

// WithBeforePush returns opts with a hook that runs after the rewritten
// history is written and right before it is pushed. Tests use it to push
// from another device in between, so the lease fails.
func WithBeforePush(opts Options, f func()) Options {
	opts.beforePush = f
	return opts
}
