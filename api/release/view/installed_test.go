package view

import (
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"strings"
	"testing"
)

func installedSample() Snapshot {
	s := sample()
	s.Events[0].Action = "upgrade_activate"
	s.Events[0].Outcome = "completed"
	s.InstalledRelease = &InstalledRelease{ReleaseID: "current", Profile: wire.MVPPostgreSQLCPUProfile, ArchiveSHA256: strings.Repeat("e", 64), ManifestSHA256: strings.Repeat("f", 64), SourceCommit: strings.Repeat("a", 40), SchemaVersion: wire.MVPPostgreSQLCPUV1SchemaVersion, ActivationSequence: 1}
	return s
}
func TestInstalledReleaseKeepsOriginalBindingAndRequiresCompletedActivation(t *testing.T) {
	s := installedSample()
	if s.Validate() != nil {
		t.Fatal("valid completed installed record refused")
	}
	for _, change := range []func(*Snapshot){func(v *Snapshot) { v.Events[0].Outcome = "started" }, func(v *Snapshot) { v.Events[0].Action = "process_start" }, func(v *Snapshot) { v.InstalledRelease.ActivationSequence = 2 }, func(v *Snapshot) { v.InstalledRelease.ReleaseID = v.ReleaseID }, func(v *Snapshot) { v.InstalledRelease.ManifestSHA256 = v.ManifestSHA256 }, func(v *Snapshot) { v.InstalledRelease.SchemaVersion = 23 }, func(v *Snapshot) { v.InstalledRelease.Profile = "platform" }, func(v *Snapshot) { v.InstalledRelease.SourceCommit = "missing" }} {
		v := installedSample()
		change(&v)
		if v.Validate() == nil {
			t.Fatal("uncompleted/contradictory installed release admitted")
		}
	}
	raw, _ := json.Marshal(sample())
	if strings.Contains(string(raw), "installed_release") {
		t.Fatal("historical encoding changed")
	}
	s = installedSample()
	s.FirstSequence = 2
	s.Sequence = 2
	s.Events[0].Sequence = 2
	s.Events[0].Action = "process_stop"
	if s.Validate() != nil {
		t.Fatal("valid activation older than bounded suffix refused")
	}
}
func TestProjectionPolicyHasOneStrictLowercaseRequiredShape(t *testing.T) {
	c := Configuration{Directory: "/private/projection", ScopeID: strings.Repeat("a", 32), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: 0, PageSize: 7, OperationSeconds: 2}
	raw, _ := json.Marshal(c)
	if got, e := ParseConfiguration(raw); e != nil || got != c {
		t.Fatal("shared policy", e)
	}
	for _, bad := range []string{strings.Replace(string(raw), "publisher_uid", "Publisher_uid", 1), strings.Replace(string(raw), `"publisher_uid":0,`, "", 1), strings.Replace(string(raw), `"publisher_uid":0`, `"publisher_uid":null`, 1), strings.Replace(string(raw), `"page_size":7`, `"page_size":101`, 1), strings.Replace(string(raw), `"operation_seconds":2`, `"operation_seconds":0`, 1), strings.Replace(string(raw), `"page_size":7`, `"page_size":7,"page_size":7`, 1)} {
		if _, e := ParseConfiguration([]byte(bad)); e == nil {
			t.Fatal("ambiguous or invalid shared policy admitted")
		}
	}
}
