//go:build windows

package gitstore

import "syscall"

// ownProcessGroup starts git in a new process group, which does not receive
// the console's Ctrl+C. gcd decides what to do: `gcd watch` lets the running
// sync finish, other commands cancel their context, which kills git.
func ownProcessGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
