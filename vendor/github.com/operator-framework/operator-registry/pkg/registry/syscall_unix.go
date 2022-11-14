//go:build !windows
// +build !windows

package registry

import "golang.org/x/sys/unix"

var umask = unix.Umask
