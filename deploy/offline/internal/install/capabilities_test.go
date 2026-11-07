package install

import (
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/pkg/capabilities"
)

func TestWriteCapabilitiesPrintsEveryOption(t *testing.T) {
	var out strings.Builder
	if e := WriteCapabilities(&out); e != nil {
		t.Fatal(e)
	}
	for _, registry := range capabilities.Registries() {
		if !strings.Contains(out.String(), registry.Name+": "+registry.Description) {
			t.Fatal("missing registry", registry.ID)
		}
		for _, option := range registry.Options {
			line := ""
			for _, candidate := range strings.Split(out.String(), "\n") {
				if strings.HasPrefix(candidate, "  "+option.ID+" ") {
					line = candidate
				}
			}
			if line == "" || !strings.Contains(line, capabilityStates[option.State]) ||
				!strings.Contains(line, option.RoadmapItem) || !strings.Contains(line, option.Reason) ||
				!strings.Contains(line, option.Note) {
				t.Fatalf("%s/%s: %q", registry.ID, option.ID, line)
			}
		}
	}
}

func TestEveryStateHasAWord(t *testing.T) {
	for _, state := range []capabilities.State{capabilities.Ready, capabilities.Planned, capabilities.Licensed, capabilities.Unsupported} {
		if capabilityStates[state] == "" {
			t.Fatal(state)
		}
	}
}

func TestUnknownTopologySizeNamesTheDeclaredOnes(t *testing.T) {
	e := validateTopologySize("huge")
	if e == nil || !strings.Contains(e.Error(), "topology.size") || !strings.Contains(e.Error(), "single-node, ha, cell, multi-cell, multi-site") {
		t.Fatal(e)
	}
	if e := validateTopologySize(SingleNode); e != nil {
		t.Fatal(e)
	}
}
