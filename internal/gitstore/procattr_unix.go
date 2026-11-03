//go:build !windows

package gitstore

import "syscall"

// ownProcessGroup starts git in its own process group, so the SIGINT a
// terminal sends on Ctrl+C reaches gcd only. gcd decides what to do: `gcd
// watch` lets the running sync finish, other commands cancel their context,
// which kills git.
func ownProcessGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
