package v1

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTransferEnvelopesPreserveMetadataAndRejectAmbiguousJSON(t *testing.T) {
	metadata := []byte("{\n  \"schema_version\": 1\n}")
	raw := []byte(`{"lease_token":"` + strings.Repeat("a", 43) + `","kind":"checkpoint","metadata":` + string(metadata) + `}`)
	u, e := DecodeUploadSubmission(raw)
	if e != nil || !bytes.Equal(u.Metadata, metadata) {
		t.Fatal("original receipt byte boundary lost", e)
	}
	for _, bad := range [][]byte{append([]byte(`{"kind":"artifact",`), raw[1:]...), bytes.Replace(raw, []byte(`"metadata":{`), []byte(`"Metadata":{`), 1), bytes.Replace(raw, metadata, []byte(`null`), 1), bytes.Replace(raw, metadata, []byte(`{"duplicate":1,"duplicate":2}`), 1), bytes.Repeat([]byte("x"), MaximumUploadSubmissionBytes+1)} {
		if _, e := DecodeUploadSubmission(bad); e == nil {
			t.Fatal("ambiguous/oversized upload envelope accepted")
		}
	}
}

func TestStopReceiptExactAttemptImageAndAbsenceProof(t *testing.T) {
	c := validCommand()
	digest, e := c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	r := StopReceipt{SchemaVersion: 1, ReceiptID: c.Scope.JobID, JournalID: c.AttemptID, NodeID: c.ModelID, TaskID: c.DatasetID, Scope: c.Scope, AttemptID: c.AttemptID, AttemptNumber: c.AttemptNumber, CommandSHA256: digest, ImageID: *c.Recipe.Identity.ImageID, GPUUUID: c.GPUUUID, ContainerName: ContainerName(c.AttemptID), Proof: "never_created", OwnedContainerAbsent: true, CompletedAt: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
	if e = r.ValidateFor(c, r.NodeID, r.TaskID); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(StopSubmission{LeaseToken: strings.Repeat("a", 43), Receipt: r, Outcome: json.RawMessage("null")})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = DecodeStopSubmission(raw); e != nil {
		t.Fatal("exact nullable stop envelope", e)
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"container_id":null,`), nil, 1), bytes.Replace(raw, []byte(`"outcome":null`), []byte(`"outcome":null,"outcome":null`), 1), bytes.Replace(raw, []byte(`"owned_container_absent":true`), []byte(`"owned_container_absent":null`), 1)} {
		if _, e := DecodeStopSubmission(bad); e == nil {
			t.Fatal("ambiguous/missing/null stop field accepted")
		}
	}
	for _, change := range []func(*StopReceipt){func(v *StopReceipt) { v.ImageID = "sha256:" + strings.Repeat("b", 64) }, func(v *StopReceipt) { v.AttemptNumber++ }, func(v *StopReceipt) { v.ContainerName = "unrelated" }, func(v *StopReceipt) { v.Proof = "owned_container_removed" }} {
		v := r
		change(&v)
		if v.ValidateFor(c, r.NodeID, r.TaskID) == nil {
			t.Fatal("foreign stop proof accepted")
		}
	}
}
