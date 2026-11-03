package cli_test

import (
	"os"
	"testing"

	"github.com/Hanson-Tsai/gcd/internal/gitstore"
)

// TestMain lets the test binary double as the helper programs gcd starts:
//
//   - $EDITOR: when GCD_TEST_EDITOR is set it overwrites the file it is given
//     with GCD_TEST_EDITOR_CONTENT and exits, like a user editing and saving;
//   - GIT_ASKPASS: git runs the gcd executable (here, the test binary) to get
//     the token.
func TestMain(m *testing.M) {
	if os.Getenv("GCD_TEST_EDITOR") != "" {
		file := os.Args[len(os.Args)-1]
		if err := os.WriteFile(file, []byte(os.Getenv("GCD_TEST_EDITOR_CONTENT")), 0o644); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	if gitstore.ServeAskpass() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}
