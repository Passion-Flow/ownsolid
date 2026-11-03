package factory

import (
	"os"
	"syscall"
)

// F_NOCACHE is per descriptor. F_GLOBAL_NOCACHE is deliberately never used.
func prepareStreamDescriptor(f *os.File) error {
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_NOCACHE, 1)
	if e != 0 {
		return e
	}
	return nil
}

func discardStreamRange(_ *os.File, _, _ int64) error { return nil }
