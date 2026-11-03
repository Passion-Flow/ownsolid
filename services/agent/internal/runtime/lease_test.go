package runtime

import (
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"strings"
	"testing"
)

func TestLifecycleLeaseDispatchIsDisjointAndCannotUpgradeOlderProfiles(t *testing.T) {
	prep := runtimePreparationLease()
	train := runtimeTrainingLease()
	for _, profile := range []string{wire.ReadOnlyProfile, wire.SystemdComponentsProfile, wire.SystemdMaintenanceProfile, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile} {
		if e := validateExecutionLease(profile, prep); (e == nil) != (profile == wire.DockerModelLifecycleProfile) {
			t.Fatal("preparation authority", profile, e)
		}
		if e := validateExecutionLease(profile, train); (e == nil) != wire.IsTrainingProfile(profile) {
			t.Fatal("training authority", profile, e)
		}
	}
	mixed := prep
	mixed.Training = train.Training
	if validateExecutionLease(wire.DockerModelLifecycleProfile, mixed) == nil {
		t.Fatal("mixed executor union")
	}
	missing := prep
	missing.Preparation = nil
	if validateExecutionLease(wire.DockerModelLifecycleProfile, missing) == nil {
		t.Fatal("missing typed command")
	}
	collect := wire.Lease{TaskID: prep.TaskID, Operation: wire.CollectFacts, LeaseToken: strings.Repeat("A", 43), Attempt: 1}
	if e := validateExecutionLease(wire.DockerModelLifecycleProfile, collect); e != nil {
		t.Fatal(e)
	}
	collect.Preparation = prep.Preparation
	if validateExecutionLease(wire.DockerModelLifecycleProfile, collect) == nil {
		t.Fatal("discovery smuggled preparation")
	}
}
