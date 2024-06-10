//go:build !windows
// +build !windows

package cache

import "golang.org/x/sys/unix"

var umask = unix.Umask
