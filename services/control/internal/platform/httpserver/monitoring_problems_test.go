package httpserver

import (
	"errors"
	"testing"
)

func TestMonitoringPublicFailureEnvelopes(t *testing.T) {
	for code, want := range map[string]int{"CONFIG_MONITORING_INVALID": 503, "MONITORING_CONFIGURATION_INVALID": 503, "MONITORING_CONFIGURATION_REQUIRED": 503, "MONITORING_OWNER_REQUIRED": 503, "MONITORING_BUSY": 429} {
		actual, status := publicProblem(errors.New(code))
		if actual != code || status != want {
			t.Fatal(code, actual, status)
		}
	}
	if code, status := publicProblem(errors.New("MONITORING_private credential/path")); code != "STORAGE_UNAVAILABLE" || status != 503 {
		t.Fatal("private diagnostic escaped", code, status)
	}
}
