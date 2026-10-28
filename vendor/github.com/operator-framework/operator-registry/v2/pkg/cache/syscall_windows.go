//go:build windows
// +build windows

package cache

var umask = func(i int) int { return 0 }
