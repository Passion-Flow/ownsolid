package engine

import (
	"errors"
	"fmt"
	"testing"
)

func TestEngineDiagnosticNeverPublishesCallerOrTransportText(t *testing.T) {
	const code = "OFFLINE_ENGINE_CONFIGURATION_INVENTORY_MISMATCH"
	for _, err := range []error{failure(code), fmt.Errorf("private wrapper: %w", failure(code))} {
		if DiagnosticCode(err) != code {
			t.Fatal("adapter code lost")
		}
	}
	for _, err := range []error{nil, errors.New(code), errors.New("SYNTHETIC_SECRET_VALUE"), &diagnosticFailure{code: code + ":SYNTHETIC_SECRET_VALUE"}} {
		if DiagnosticCode(err) != "OFFLINE_ENGINE_UNCLASSIFIED_FAILURE" {
			t.Fatal("untrusted diagnostic accepted")
		}
	}
}
