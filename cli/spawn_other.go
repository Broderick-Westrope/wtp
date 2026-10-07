//go:build !unix

package cli

import "syscall"

func detachedAttr() *syscall.SysProcAttr {
	return nil
}
