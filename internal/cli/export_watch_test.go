package cli

import (
	"testing"
	"time"
)

// SetWatchTiming overrides the repo config's debounce and poll interval for
// gcd watch until the test ends.
func SetWatchTiming(t testing.TB, debounce, poll time.Duration) {
	oldD, oldP := watchDebounce, watchPoll
	watchDebounce, watchPoll = debounce, poll
	t.Cleanup(func() { watchDebounce, watchPoll = oldD, oldP })
}
