package httpserver

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestPrivateClusterPublicStatusesMatchCanonicalContract(t *testing.T) {
	raw, e := os.ReadFile("../../../../../api/openapi/v1/privateclusters/problem-statuses.json")
	if e != nil {
		t.Fatal(e)
	}
	var contract struct {
		Statuses map[string]int `json:"statuses"`
	}
	if json.Unmarshal(raw, &contract) != nil || len(contract.Statuses) != 80 {
		t.Fatal("full private-cluster error contract required")
	}
	for code, want := range contract.Statuses {
		got, status := publicProblem(errors.New(code))
		if got != code || status != want {
			t.Fatal("public status drift", code, got, status, want)
		}
	}
	for _, private := range []string{"PRIVATE_CLUSTER_unregistered", "PRIVATE_CLUSTER_TOKEN_REQUIRED: private path or token", "private SQL diagnostic"} {
		if code, status := publicProblem(errors.New(private)); code != "STORAGE_UNAVAILABLE" || status != 503 {
			t.Fatal("unregistered diagnostic escaped", code, status)
		}
	}
}
