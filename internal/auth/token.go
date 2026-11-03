package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

// TokenEnv overrides the keychain: when set, its value is the token for every
// repo.
const TokenEnv = "GCD_TOKEN"

// keyringService names gcd's entries in the OS keychain. Each entry's account
// is "github.com/owner/repo", so different repos may use different
// fine-grained tokens.
const keyringService = "gcd"

var (
	// ErrNoToken means neither GCD_TOKEN nor the keychain has a token.
	ErrNoToken = errors.New("no GitHub token")
	// ErrKeyringNotFound is what a Keyring returns for a missing entry.
	ErrKeyringNotFound = keyring.ErrNotFound
)

// Keyring is the OS credential store.
type Keyring interface {
	// Get returns the secret, or ErrKeyringNotFound.
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
}

// OSKeyring is the platform keychain: macOS Keychain, Windows Credential
// Manager or the Linux Secret Service (D-Bus). On macOS the secret is handed
// to /usr/bin/security on stdin, never as a process argument.
type OSKeyring struct{}

func (OSKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (OSKeyring) Set(service, user, secret string) error   { return keyring.Set(service, user, secret) }

// Tokens finds and stores tokens. The zero value is not usable; see
// DefaultTokens.
type Tokens struct {
	Keyring Keyring
	Getenv  func(string) string
}

// DefaultTokens reads the real environment and OS keychain.
func DefaultTokens() Tokens { return Tokens{Keyring: OSKeyring{}, Getenv: os.Getenv} }

func account(r Repo) string { return Host + "/" + r.String() }

// Lookup returns the token for repo: $GCD_TOKEN first, then the keychain.
func (t Tokens) Lookup(r Repo) (string, error) {
	if tok := strings.TrimSpace(t.Getenv(TokenEnv)); tok != "" {
		return tok, nil
	}
	tok, err := t.Keyring.Get(keyringService, account(r))
	switch {
	case errors.Is(err, ErrKeyringNotFound):
		return "", fmt.Errorf("%w for %s: run `gcd login %s` to save one in the OS keychain, or set %s",
			ErrNoToken, r, r, TokenEnv)
	case err != nil:
		return "", fmt.Errorf("read token for %s from the OS keychain: %w (set %s to bypass the keychain)",
			r, err, TokenEnv)
	}
	return tok, nil
}

// Store saves token for repo in the keychain, replacing any previous one.
func (t Tokens) Store(r Repo, token string) error {
	if err := t.Keyring.Set(keyringService, account(r), token); err != nil {
		return fmt.Errorf("save token for %s in the OS keychain: %w (set %s to bypass the keychain)",
			r, err, TokenEnv)
	}
	return nil
}
