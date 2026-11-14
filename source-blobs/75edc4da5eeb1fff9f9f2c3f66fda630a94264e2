package httpserver

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestTaskDeliveryPublicStatusesMatchCanonicalOwner(t *testing.T) {
	raw, e := os.ReadFile("../../../../../api/openapi/v1/taskdeliveries/problem-statuses.integration.json")
	if e != nil {
		t.Fatal(e)
	}
	var contract struct {
		Statuses map[string]int `json:"statuses"`
	}
	if json.Unmarshal(raw, &contract) != nil || len(contract.Statuses) == 0 {
		t.Fatal("actual status contract invalid")
	}
	for code, expected := range contract.Statuses {
		actualCode, actualStatus := publicProblem(errors.New(code))
		if actualCode != code || actualStatus != expected {
			t.Fatal("canonical public status mismatch", code, actualCode, actualStatus, expected)
		}
	}
	if code, status := publicProblem(errors.New("private broker/path/credential diagnostic")); code != "STORAGE_UNAVAILABLE" || status != 503 {
		t.Fatal("unregistered private cause escaped", code, status)
	}
}
