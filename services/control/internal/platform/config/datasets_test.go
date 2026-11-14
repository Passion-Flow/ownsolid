package config_test

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"testing"
)

func TestDatasetPolicyNondefaultLimitsRelationshipsAndPathOwnership(t *testing.T) {
	p := config.DefaultDatasetPolicy()
	p.Limits.MaximumBytes = 8388608
	p.Limits.MaximumRows = 100
	p.Limits.MaximumErrors = 20
	p.Resources.MaximumResultBytes = 16777216
	p.Resources.WallSeconds = 20
	p.Resources.CpuSeconds = 5
	p.LeaseSeconds = 75
	p.Concurrency = 2
	if e := p.Validate(); e != nil {
		t.Fatal("nondefault dataset policy", e)
	}
	cases := []func(*config.DatasetPolicy){func(v *config.DatasetPolicy) { v.Limits.MaximumRows = 1 }, func(v *config.DatasetPolicy) { v.Limits.MaximumErrors = v.Limits.MaximumRows + 1 }, func(v *config.DatasetPolicy) { v.Resources.MemoryBytes = 1 }, func(v *config.DatasetPolicy) { v.Resources.MaximumResultBytes = 4096 }, func(v *config.DatasetPolicy) { v.LeaseSeconds = 30 }, func(v *config.DatasetPolicy) { v.Concurrency = 3 }, func(v *config.DatasetPolicy) { v.ValidationBasisPoints = 0 }}
	for _, change := range cases {
		other := p
		change(&other)
		if other.Validate() == nil {
			t.Fatal("invalid dataset relationship accepted")
		}
	}
	accepted := config.DatasetConfiguration{ArtifactRoot: "/var/lib/projectone/datasets", PythonExecutable: "/usr/local/bin/python3.12", ProcessorFile: "/opt/projectone/data-processing/process.py"}
	if e := accepted.Validate("/var/lib/projectone/models"); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []config.DatasetConfiguration{{ArtifactRoot: "relative"}, {ArtifactRoot: "/var/lib/projectone/datasets"}, {ArtifactRoot: "/", PythonExecutable: accepted.PythonExecutable, ProcessorFile: accepted.ProcessorFile}, {ArtifactRoot: accepted.ArtifactRoot, PythonExecutable: accepted.ArtifactRoot + "/python", ProcessorFile: accepted.ProcessorFile}} {
		if bad.Validate("") == nil {
			t.Fatal("unsafe/incomplete dataset configuration accepted")
		}
	}
	if accepted.Validate(accepted.ArtifactRoot+"/models") == nil {
		t.Fatal("dataset/model ownership namespaces overlapped")
	}
}
