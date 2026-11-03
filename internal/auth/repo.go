// Package auth handles GitHub authentication for gcd: parsing owner/repo,
// finding the fine-grained PAT (GCD_TOKEN, then the OS keychain), storing it,
// and checking a repo and token through the GitHub REST API before a Disk is
// bound to it.
package auth

import (
	"fmt"
	"regexp"
	"strings"
)

// Host is the GitHub host gcd talks to for git over HTTPS.
const Host = "github.com"

// Repo is a GitHub repository, owner/name.
type Repo struct {
	Owner string
	Name  string
}

var (
	ownerRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	nameRE  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// ParseRepo accepts "owner/repo" or a https://github.com/owner/repo[.git]
// URL.
func ParseRepo(s string) (Repo, error) {
	in := s
	if rest, ok := strings.CutPrefix(s, "https://"+Host+"/"); ok {
		s = strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git")
	}
	owner, name, ok := strings.Cut(s, "/")
	if !ok || !ownerRE.MatchString(owner) || !nameRE.MatchString(name) || name == "." || name == ".." {
		return Repo{}, fmt.Errorf("%q is not a GitHub repo; want owner/repo", in)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// String returns "owner/repo".
func (r Repo) String() string { return r.Owner + "/" + r.Name }
