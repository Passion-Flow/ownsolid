package httpserver

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestMongoDBSourceChangedRequiresReviewWithoutRetryAdvice(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Retry-After", "stale")
	Problem(w, "synthetic-mongodb-change", errors.New("MONGODB_SOURCE_CHANGED"))
	var body map[string]any
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || w.Code != 409 ||
		w.Header().Get("Retry-After") != "" || w.Header().Get("Content-Type") != "application/problem+json" ||
		len(body) != 5 || body["code"] != "MONGODB_SOURCE_CHANGED" || body["request_id"] != "synthetic-mongodb-change" {
		t.Fatal("source change lost its public conflict/review boundary", w.Code, w.Body.String())
	}
}
