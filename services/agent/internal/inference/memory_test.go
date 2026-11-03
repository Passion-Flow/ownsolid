package inference

import "testing"

func TestAvailableMemoryGateIsBoundedAndRejectsMissingOrAmbiguousValues(t *testing.T) {
	if e := validateAvailableHostMemory("MemTotal: 16777216 kB\nMemAvailable: 9437184 kB\n"); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"MemFree: 16777216 kB", "MemAvailable: 9437183 kB", "MemAvailable: -1 kB", "MemAvailable: 18446744073709551616 kB", "MemAvailable: 9437184 bytes", "MemAvailable: 9437184 kB\nMemAvailable: 9437184 kB"} {
		if validateAvailableHostMemory(value) == nil {
			t.Fatal("unsafe available memory gate accepted", value)
		}
	}
}
