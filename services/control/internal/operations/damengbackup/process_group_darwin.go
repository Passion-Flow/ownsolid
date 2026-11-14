//go:build darwin

package damengbackup

import "syscall"

// Native construction refuses non-Linux hosts; this is only a conservative
// process helper for package compilation on the development Mac.
func processGroupStopped(group int) bool { return syscall.Kill(-group, 0) == syscall.ESRCH }
