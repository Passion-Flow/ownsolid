package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
)

const enrollmentCommitUnknown = "STORAGE_COMMIT_OUTCOME_UNKNOWN"

var enrollmentRequestID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// EnrollmentUncertain preserves only validated reconciliation context. It does
// not authorize another POST or expose any response detail/private credential.
type EnrollmentUncertain struct{ requestID string }

func (e *EnrollmentUncertain) Error() string {
	return enrollmentCommitUnknown + " request_id=" + e.requestID
}
func (e *EnrollmentUncertain) RequestID() string { return e.requestID }

func enrollmentUnknown(response *http.Response, body []byte) error {
	refused := errors.New("CONTROL_RESPONSE_REJECTED")
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	_, retry := response.Header["Retry-After"]
	if e != nil || media != "application/problem+json" || response.StatusCode != http.StatusConflict ||
		response.Header.Get("Cache-Control") != "no-store" || len(response.Header.Values("X-Request-ID")) != 1 || retry {
		return refused
	}
	// A strict object walk rejects duplicate keys before normal struct decoding.
	decoder := json.NewDecoder(bytes.NewReader(body))
	first, e := decoder.Token()
	if e != nil || first != json.Delim('{') {
		return refused
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, e := decoder.Token()
		name, ok := key.(string)
		if e != nil || !ok || seen[name] {
			return refused
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return refused
		}
	}
	if last, e := decoder.Token(); e != nil || last != json.Delim('}') || decoder.Decode(new(any)) != io.EOF || len(seen) != 5 {
		return refused
	}
	var problem struct {
		Type      string `json:"type"`
		Title     string `json:"title"`
		Status    int    `json:"status"`
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	}
	decoder = json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&problem) != nil || problem.Type != "about:blank" || problem.Title != enrollmentCommitUnknown ||
		problem.Code != enrollmentCommitUnknown || problem.Status != http.StatusConflict ||
		!enrollmentRequestID.MatchString(problem.RequestID) || problem.RequestID == "00000000-0000-0000-0000-000000000000" ||
		response.Header.Get("X-Request-ID") != problem.RequestID {
		return refused
	}
	return &EnrollmentUncertain{requestID: problem.RequestID}
}
