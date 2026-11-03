package workflows

import (
	"encoding/json"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"testing"
)

func TestM13CumulativeSchema1ProfileNeverEnablesTools(t *testing.T) {
	profile := "workflows"
	s := &Service{Identity: &identity.Service{Config: config.Config{Server: config.ServerConfiguration{ApplicationProfile: &profile}}}}
	raw, _ := json.Marshal(validToolGraph())
	if _, e := s.parseGraph(raw, config.DefaultWorkflowPolicy(), 100); e == nil {
		t.Fatal("future tool graph admitted by current profile")
	}
	p, e := s.initialGraph()
	if e != nil || p.Graph.SchemaVersion != 1 || p.IsTool() || len(p.Graph.Nodes) != 4 {
		t.Fatal("current default graph", e)
	}
	for _, k := range s.configurationFor(identity.Auth{}, workspaces.Workspace{}).NodeKinds {
		if k == ToolNode {
			t.Fatal("tool public capability")
		}
	}
}
