package httpserver

import (
	"errors"
	"testing"
)

func TestPublicErrorCodeNeverPersistsPrivateDiagnosticText(t *testing.T) {
	for _, raw := range []string{"private-synthetic-password in SQL", "open /synthetic/private/model: denied", "WORKFLOW_FAKE_PRIVATE_SECRET", "provider body synthetic-private-answer"} {
		if PublicErrorCode(errors.New(raw)) != "STORAGE_UNAVAILABLE" {
			t.Fatal("unknown private diagnostic became public")
		}
	}
	if PublicErrorCode(errors.New("KNOWLEDGE_QUERY_TOO_LONG")) != "KNOWLEDGE_QUERY_TOO_LONG" || PublicErrorCode(errors.New("WORKFLOW_RUN_TIMEOUT")) != "WORKFLOW_RUN_TIMEOUT" {
		t.Fatal("owned failure code lost")
	}
}
