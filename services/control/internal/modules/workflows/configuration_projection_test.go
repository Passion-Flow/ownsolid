package workflows

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

// Process-state projection controls only. These do not admit a run or replace
// actual selected repository/encoder/gateway integration controls.
func TestWorkflowConfigurationProjectionHasNoDatabaseMutexEdge(t *testing.T) {
	s := &Service{Identity: &identity.Service{}}
	s.Identity.Config.Workflows.Enabled = true
	role := "viewer"
	w := workspaces.Workspace{Role: &role}
	a := identity.Auth{Me: identity.Me{Audience: "portal"}}
	if s.configurationFor(a, w).CanExecute {
		t.Fatal("zero lifecycle enabled execution")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.lifecycleMu.Lock()
	s.started = true
	s.lifecycleContext = ctx
	s.publishConfigurationLocked()
	// Keep the actual lifecycle mutex held: a configuration read must not take it.
	if !s.configurationFor(a, w).CanExecute {
		t.Fatal("published current lifecycle unavailable")
	}
	s.maintenanceError = "WORKFLOW_MAINTENANCE_FAILED"
	s.publishConfigurationLocked()
	if s.configurationFor(a, w).CanExecute {
		t.Fatal("maintenance failure remained executable")
	}
	s.maintenanceError = ""
	s.publishConfigurationLocked()
	if !s.configurationFor(a, w).CanExecute {
		t.Fatal("recovered maintenance unavailable")
	}
	cancel()
	if s.configurationFor(a, w).CanExecute {
		t.Fatal("cancelled parent stayed available without a publisher")
	}
	next, stop := context.WithCancel(context.Background())
	defer stop()
	s.lifecycleContext = next
	s.publishConfigurationLocked()
	s.Identity.Config.Workflows.Enabled = false
	if s.configurationFor(a, w).CanExecute {
		t.Fatal("explicit false enabled execution")
	}
	s.Identity.Config.Workflows.Enabled = true
	s.closing = true
	s.publishConfigurationLocked()
	if s.configurationFor(a, w).CanExecute {
		t.Fatal("closing lifecycle remained executable")
	}
	s.lifecycleMu.Unlock()
}
