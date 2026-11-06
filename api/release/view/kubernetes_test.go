package view

import (
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"strings"
	"testing"
)

func kubernetesSample() Snapshot {
	s := sample()
	s.Format = KubernetesFormat
	s.Profile = "mvp-postgresql-cpu-v1-linux-amd64"
	s.Events[0].Action = "installation_terminal"
	s.Events[0].Outcome = "serving_verified"
	s.Kubernetes = &KubernetesJournalBinding{IndexFormat: KubernetesIndexFormat, ApplicationProfile: wire.MVPPostgreSQLCPUApplicationProfile, SchemaVersion: 44, PreparedPlanSHA256: strings.Repeat("1", 64), LastSource: KubernetesSourceReference{Kind: "installation", EventKind: "terminal", OperationID: s.Events[0].OperationID, PlanSHA256: strings.Repeat("2", 64), Sequence: 1, SHA256: strings.Repeat("3", 64)}, InstalledRelease: &KubernetesInstalledRelease{ReleaseID: s.ReleaseID, Profile: s.Profile, ArchiveSHA256: s.ArchiveSHA256, ManifestSHA256: s.ManifestSHA256, SourceCommit: strings.Repeat("4", 40), SchemaVersion: 44, ActivationSequence: 1}}
	return s
}
func TestKubernetesViewBindsActualOrderedSourceAndServingActivation(t *testing.T) {
	s := kubernetesSample()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Snapshot){func(s *Snapshot) { s.Format = Format }, func(s *Snapshot) { s.Kubernetes.ApplicationProfile = "releases" }, func(s *Snapshot) { s.Kubernetes.SchemaVersion = 22 }, func(s *Snapshot) { s.Kubernetes.LastSource.Kind = "merged_timestamps" }, func(s *Snapshot) { s.Kubernetes.LastSource.OperationID = strings.Repeat("9", 32) }, func(s *Snapshot) { s.Kubernetes.LastSource.PlanSHA256 = "unknown" }, func(s *Snapshot) { s.Kubernetes.LastSource.Sequence = 0 }, func(s *Snapshot) { s.Kubernetes.InstalledRelease.ActivationSequence = 2 }, func(s *Snapshot) { s.Kubernetes.InstalledRelease.SchemaVersion = 22 }, func(s *Snapshot) { s.Events[0].Outcome = "recorded" }, func(s *Snapshot) { s.InstalledRelease = &InstalledRelease{} }} {
		copy := kubernetesSample()
		change(&copy)
		if copy.Validate() == nil {
			t.Fatal("foreign/ambiguous/uncompleted Kubernetes source accepted")
		}
	}
	legacy := sample()
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "kubernetes") || legacy.Validate() != nil {
		t.Fatal("legacy omitted bytes or validation changed")
	}
}

func TestKubernetesDecodePreservesLegacyClosedFieldsAndRejectsNullBindings(t *testing.T) {
	legacy := sample()
	raw, _ := json.Marshal(legacy)
	malformed := append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"kubernetes":null}`)...)
	var decoded Snapshot
	if wire.StrictJSON(malformed, &decoded) == nil {
		t.Fatal("legacy unknown additive null accepted")
	}
	current := kubernetesSample()
	raw, _ = json.Marshal(current)
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	fields["installed_release"] = json.RawMessage("null")
	malformed, _ = json.Marshal(fields)
	if wire.StrictJSON(malformed, &decoded) == nil {
		t.Fatal("Kubernetes top-level legacy null accepted")
	}
	delete(fields, "installed_release")
	fields["kubernetes"] = json.RawMessage("null")
	malformed, _ = json.Marshal(fields)
	if wire.StrictJSON(malformed, &decoded) == nil {
		t.Fatal("null Kubernetes binding accepted")
	}
	raw, _ = json.Marshal(current.Kubernetes)
	var nested map[string]json.RawMessage
	json.Unmarshal(raw, &nested)
	nested["installed_release"] = json.RawMessage("null")
	fields["kubernetes"], _ = json.Marshal(nested)
	malformed, _ = json.Marshal(fields)
	if wire.StrictJSON(malformed, &decoded) == nil {
		t.Fatal("null recorded installed release accepted")
	}
	raw, _ = json.Marshal(current)
	if wire.StrictJSON(raw, &decoded) != nil || decoded.Validate() != nil {
		t.Fatal("valid additive frame refused")
	}
	policy := Configuration{Directory: "/run/view", ScopeID: strings.Repeat("a", 32), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: 0, PageSize: 1, OperationSeconds: 1}
	raw, _ = json.Marshal(policy)
	if _, e := ParseConfiguration(raw); e != nil {
		t.Fatal(e)
	}
	malformed = []byte(strings.Replace(string(raw), `"publisher_uid":0`, `"publisher_uid":null`, 1))
	if _, e := ParseConfiguration(malformed); e == nil {
		t.Fatal("null publisher mapped to root UID")
	}
}
