package httpserver

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestCommitUnknownRequiresReconciliationWithoutRetryAdvice(t *testing.T) {
	const request = "9fe472ea-d595-424a-bbef-aea6ee6a3f4f"
	for _, e := range []error{&transaction.Error{Kind: transaction.CommitUnknown, SQLState: "08006", Number: 2013}, errors.New(string(transaction.CommitUnknown))} {
		r := httptest.NewRecorder()
		Problem(r, request, e)
		var body map[string]any
		if json.Unmarshal(r.Body.Bytes(), &body) != nil || r.Code != 409 || r.Header().Get("Retry-After") != "" || r.Header().Get("Content-Type") != "application/problem+json" || len(body) != 5 || body["code"] != string(transaction.CommitUnknown) || body["request_id"] != request {
			t.Fatal("unsafe or retryable commit uncertainty response", r.Code, r.Body.String())
		}
	}
}
