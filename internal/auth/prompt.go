package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// PromptToken asks for the PAT for r on out and reads it from in. On a
// terminal the input is not echoed; from a pipe one line is read.
func PromptToken(in io.Reader, out io.Writer, r Repo) (string, error) {
	fmt.Fprintf(out, "gcd needs a fine-grained GitHub token for %s.\n%s.\n(It is saved in the OS keychain; %s overrides it.)\n\nToken: ",
		r, createTokenHelp(r), TokenEnv)
	var line string
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		if err != nil {
			return "", fmt.Errorf("read token: %w", err)
		}
		line = string(b)
	} else {
		s, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read token: %w", err)
		}
		line = s
	}
	fmt.Fprintln(out)
	tok := strings.TrimSpace(line)
	if tok == "" {
		return "", fmt.Errorf("%w for %s: none entered", ErrNoToken, r)
	}
	return tok, nil
}
