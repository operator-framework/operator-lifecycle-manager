//go:build windows
// +build windows

package registry

var umask = func(i int) int { return 0 }
