package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInferenceToolCandidateKeepsV1LeaseClosed(t *testing.T) {
	const claim = `{
	  "lease": {
	    "task_id":"00000000-0000-0000-0000-000000000001",
	    "sequence":1,
	    "operation":"inference.deploy.v1",
	    "lease_token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	    "lease_expires_at":"2026-11-09T00:00:00Z",
	    "attempt":1,
	    "inference": {
	      "action":"deploy",
	      "deployment_id":"00000000-0000-0000-0000-000000000002",
	      "model_id":"00000000-0000-0000-0000-000000000003",
	      "catalog_digest":"4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc",
	      "gpu_uuid":"GPU-00000000-0000-0000-0000-000000000004",
	      "revision":1
	    }
	  }
	}`
	for _, transport := range []struct {
		name   string
		decode func(string, *Claim) error
	}{
		{"HTTPS claim payload", func(body string, out *Claim) error {
			decoder := json.NewDecoder(strings.NewReader(body))
			decoder.DisallowUnknownFields()
			return decoder.Decode(out)
		}},
		{"gRPC claim reply", func(body string, out *Claim) error {
			var msg ControlMessage
			err := (SessionCodec{}).Unmarshal([]byte(`{"reply_to":1,"claim":`+body+`}`), &msg)
			if err == nil {
				*out = *msg.Claim
			}
			return err
		}},
	} {
		t.Run(transport.name, func(t *testing.T) {
			var decoded Claim
			if err := transport.decode(claim, &decoded); err != nil {
				t.Fatalf("legacy claim could not be decoded: %v", err)
			}
			lease := decoded.Lease
			if lease == nil || lease.Inference == nil || lease.Inference.Validate(lease.Operation) != nil || !OperationAllowed(DockerInferenceProfile, lease.Operation) || ProfileVersion(DockerInferenceProfile) != "1.6" {
				t.Fatal("legacy inference deploy lease or profile changed")
			}
			for _, injection := range []string{`"tool_call_parser":"hermes"`, `"serving_recipe":"gw103"`, `"argv":["--enable-auto-tool-choice"]`} {
				body := strings.Replace(claim, `"revision":1`, `"revision":1,`+injection, 1)
				if err := transport.decode(body, &Claim{}); err == nil {
					t.Errorf("inference lease accepted injected field %s", injection)
				}
			}
		})
	}
}
