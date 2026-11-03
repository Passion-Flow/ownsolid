//go:build repository_integration && aws_selected_integration

package cloudinventory

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"testing"
)

// Existing fixture admission requires an explicit isolated real PostgreSQL/MySQL
// target, applies its actual catalog, and retains its ordinary cleanup policy.
// Missing AWS migrations/guards fail these checks; candidate DDL is not activated.
func TestAWSActualSelectedRepositorySQL(t *testing.T) {
	f := repositoryfixture.New(t)
	assertActualAWSRepositorySQL(t, f.Store, f.Tenant, f.Workspace, f.User)
}
