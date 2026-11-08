//go:build !linux

package install

func clockSynchronized() bool { return false }
