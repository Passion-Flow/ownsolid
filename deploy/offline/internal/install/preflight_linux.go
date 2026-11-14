package install

import "syscall"

func clockSynchronized() bool {
	var info syscall.Timex
	state, err := syscall.Adjtimex(&info)
	// TIME_ERROR is returned when the kernel clock is unsynchronized.
	const timeError = 5
	return err == nil && state < timeError
}
