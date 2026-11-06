package postgresbackup

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func TestMVPSourceMetadataPreservesOriginalOmittedBytes(t *testing.T) {
	m := mvpBindingManifestFixture()
	legacy := `{"application_profile":"mvp-postgresql-cpu-v1","schema_version":45,"system_identifier":"7702686790272344104","database_oid":16384,"backend_pid":400,"runtime_principal":"synthetic_runtime","principal_oid":16385,"principal_verifier_sha256":"` + strings.Repeat("a", 64) + `","external_artifacts":[{"owner":"artifactstores","records":0,"content_sha256":"` + strings.Repeat("b", 64) + `","payload_authority":"original-owner-external-payload-and-reconciliation-required"},{"owner":"imageregistry","records":0,"content_sha256":"` + strings.Repeat("b", 64) + `","payload_authority":"original-owner-external-payload-and-reconciliation-required"}],"registry_configured":false}` + "\n"
	raw, e := canonical(m.MVPSource)
	if e != nil || !bytes.Equal(raw, []byte(legacy)) {
		t.Fatal("old omitted source metadata bytes changed", e)
	}
	var decoded MVPSourceBinding
	if e = datawire.DecodeStrict(raw, &decoded); e != nil || decoded.SourceMetadata != nil {
		t.Fatal("old record gained observed facts", e)
	}
	again, e := canonical(decoded)
	if e != nil || !bytes.Equal(raw, again) {
		t.Fatal("old source identity digest changed", e)
	}
	m.MVPSource = &decoded
	if e = validMVPSourceBinding(m); e != nil {
		t.Fatal("original nil metadata read refused", e)
	}
	metadata := MVPSourceMetadata{ServerVersionNumber: 180006, VectorVersion: "0.8.7"}
	m.MVPSource.SourceMetadata = &metadata
	raw, e = canonical(m.MVPSource)
	if e != nil || !bytes.Contains(raw, []byte(`"source_metadata":{"server_version_number":180006,"vector_version":"0.8.7"}`)) || validMVPSourceBinding(m) != nil {
		t.Fatal("new explicit observed facts rejected", e)
	}
}

func TestMVPSourceMetadataRefusesMalformedOrInferredFacts(t *testing.T) {
	m := mvpBindingManifestFixture()
	m.MVPSource.SourceMetadata = &MVPSourceMetadata{ServerVersionNumber: 180006, VectorVersion: "0.8.7"}
	raw, _ := json.Marshal(m.MVPSource)
	for _, name := range []string{"null", "unknown", "missing", "alias", "type", "empty-vector", "old-server", "old-vector", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			json.Unmarshal(raw, &fields)
			var metadata map[string]json.RawMessage
			json.Unmarshal(fields["source_metadata"], &metadata)
			switch name {
			case "unknown":
				metadata["unknown"] = json.RawMessage("true")
			case "missing":
				delete(metadata, "server_version_number")
			case "alias":
				metadata["ServerVersionNumber"] = metadata["server_version_number"]
				delete(metadata, "server_version_number")
			case "type":
				metadata["server_version_number"] = json.RawMessage(`"180006"`)
			case "empty-vector":
				metadata["vector_version"] = json.RawMessage(`""`)
			case "old-server":
				metadata["server_version_number"] = json.RawMessage("180005")
			case "old-vector":
				metadata["vector_version"] = json.RawMessage(`"0.8.6"`)
			}
			fields["source_metadata"], _ = json.Marshal(metadata)
			if name == "null" {
				fields["source_metadata"] = json.RawMessage("null")
			}
			if name == "duplicate" {
				fields["source_metadata"] = json.RawMessage(`{"server_version_number":180006,"server_version_number":180005,"vector_version":"0.8.7"}`)
			}
			changed, _ := json.Marshal(fields)
			var binding MVPSourceBinding
			if e := datawire.DecodeStrict(changed, &binding); e == nil {
				candidate := m
				candidate.MVPSource = &binding
				if validMVPSourceBinding(candidate) == nil {
					t.Fatal("malformed or false facts accepted")
				}
			}
		})
	}
}
