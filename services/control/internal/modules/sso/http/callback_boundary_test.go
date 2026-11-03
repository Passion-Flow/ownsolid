package http

import (
	"errors"
	"testing"
)

func TestCallbackRedirectAllowsOnlyStableUncertainOutcome(t *testing.T) {
	for _, code := range []string{"STORAGE_COMMIT_OUTCOME_UNKNOWN", "STORAGE_UNAVAILABLE", "SSO_FLOW_UNUSABLE", "SSO_PASSWORD_CHANGED"} {
		if resultError(errors.New(code)) != code {
			t.Fatal("stable callback recovery code discarded")
		}
	}
	for _, diagnostic := range []string{"provider secret diagnostic", "https://outside.example.invalid", "SSO_FLOW_UNUSABLE&result=authenticated", ""} {
		if resultError(errors.New(diagnostic)) != "SSO_FLOW_UNUSABLE" {
			t.Fatal("untrusted callback diagnostic entered redirect")
		}
	}
}
