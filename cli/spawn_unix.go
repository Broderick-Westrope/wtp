//go:build unix

package cli

import "syscall"

// detachedAttr starts the child in its own session so it survives the
// invoking shell and never receives the terminal's signals.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
