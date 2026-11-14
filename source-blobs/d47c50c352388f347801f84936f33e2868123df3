//go:build linux

package damengbackup

import (
	"os"
	"strconv"
	"strings"
)

// A descendant may inherit the vendor output pipe after its parent has exited.
// Verify this exact owned process group has no runnable/sleeping members before
// removing credentials. Zombies have exited and hold no file descriptors.
func processGroupStopped(group int) bool {
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return false
	}
	for _, entry := range entries {
		if _, e = strconv.Atoi(entry.Name()); e != nil {
			continue
		}
		raw, e := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return false
		}
		end := strings.LastIndex(string(raw), ")")
		if end < 0 {
			return false
		}
		fields := strings.Fields(string(raw)[end+1:])
		if len(fields) < 3 {
			return false
		}
		pgid, e := strconv.Atoi(fields[2])
		if e != nil {
			return false
		}
		if pgid == group && fields[0] != "Z" && fields[0] != "X" {
			return false
		}
	}
	return true
}
