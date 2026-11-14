//go:build linux && (amd64 || arm64)

package factory

import (
	"os"
	"syscall"
)

func prepareStreamDescriptor(_ *os.File) error { return nil }

func discardStreamRange(f *os.File, offset, length int64) error {
	// Linux's fadvise64 ABI uses native64-bit offset/length on both declared
	// targets. Positive page-aligned processed ranges never mean whole-file.
	const posixFadvDontneed = 4
	_, _, e := syscall.Syscall6(syscall.SYS_FADVISE64, f.Fd(), uintptr(offset), uintptr(length), posixFadvDontneed, 0, 0)
	if e != 0 {
		return e
	}
	return nil
}
