package artifactstores

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"testing"
)

func TestUnadmittedObjectServiceNeverProjectsOrStartsWork(t *testing.T) {
	s := &Service{}
	if out, e := s.Create(context.Background(), identity.Auth{}, CreateRequest{}, "", ""); e == nil || out.ID != "" {
		t.Fatal("unadmitted creation projected", e)
	}
	if out, e := s.ProcessNext(context.Background()); e == nil || out {
		t.Fatal("unadmitted worker started", e)
	}
	if e := s.VerifyStored(context.Background()); e == nil {
		t.Fatal("unadmitted readiness passed")
	}
	if e := s.Run(context.Background(), nil); e == nil {
		t.Fatal("unadmitted loop started")
	}
}

func TestArtifactWritesUseExistingWorkspaceRoleContract(t *testing.T) {
	var a identity.Auth
	a.Me.Audience = "portal"
	for _, tc := range []struct {
		role  string
		write bool
	}{{"owner", true}, {"member", true}, {"viewer", false}, {"editor", false}} {
		role := tc.role
		if writeRole(a, workspaces.Workspace{Role: &role}) != tc.write {
			t.Fatal("existing Workspace role changed", tc.role)
		}
	}
	a.Me.Audience = "admin"
	a.Me.User.IsPlatformAdmin = true
	if !writeRole(a, workspaces.Workspace{}) {
		t.Fatal("current Admin refused")
	}
}
