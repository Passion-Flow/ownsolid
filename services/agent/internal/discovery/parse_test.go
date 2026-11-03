package discovery

import (
	"runtime"
	"strings"
	"testing"
)

func TestParsersRejectMissingAndMalformedSources(t *testing.T) {
	for _, input := range []string{"ID=ubuntu\nVERSION_ID=\"24.04\"\n", "ID='ubuntu'\nVERSION_ID='24.04'\n"} {
		id, v, e := parseOS([]byte(input))
		if e != nil || id != "ubuntu" || v != "24.04" {
			t.Fatal("valid OS not parsed", e)
		}
	}
	for _, input := range []string{"ID=ubuntu", "ID=ubuntu\nID=debian\nVERSION_ID=24.04", "ID=ubuntu\nVERSION_ID=\"unterminated", strings.Repeat("x", 65537)} {
		if _, _, e := parseOS([]byte(input)); e == nil {
			t.Fatal("invalid OS accepted")
		}
	}
	if n, e := parseMemory([]byte("MemFree: 1024 kB\nMemTotal: 16384 kB\n")); e != nil || n != 16777216 {
		t.Fatal("memory unit error", e)
	}
	for _, input := range []string{"MemFree: 1 kB", "MemTotal: 0 kB", "MemTotal: -1 kB", "MemTotal: 8 GB", "MemTotal: 999999999999999999 kB"} {
		if _, e := parseMemory([]byte(input)); e == nil {
			t.Fatal("invalid memory accepted")
		}
	}
}
func TestRealCollectorOrExplicitUnsupportedHost(t *testing.T) {
	f, e := Collect("/")
	if runtime.GOOS != "linux" {
		if e == nil || e.Error() != "AGENT_OS_UNSUPPORTED" {
			t.Fatal("unsupported host silently substituted", e)
		}
		return
	}
	if e != nil || f.Architecture != runtime.GOARCH || f.MemoryTotalBytes <= 0 || f.DiskTotalBytes <= 0 {
		t.Fatal("real Linux discovery failed", e)
	}
	if _, e = Collect("/nonexistent-projectone-test-path"); e == nil {
		t.Fatal("missing disk path accepted")
	}
}
