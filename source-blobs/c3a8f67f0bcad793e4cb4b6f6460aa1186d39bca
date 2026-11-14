package main

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
)

func TestOriginalHTTPCompositionBindsEnabledRecoveryOwners(t *testing.T) {
	// Real construction and pool identity, with every database dial forbidden.
	// This is startup graph validation, not SQL recovery qualification.
	owner := &identity.Service{DB: releaseViewPool(t)}
	h := identityhttp.New(owner)
	if h.Nodes == nil || h.Nodes.Service == nil {
		t.Fatal("original composition omitted nodes")
	}
	n := h.Nodes.Service
	if e := n.CheckRecoveryOwners(); e != nil {
		t.Fatal("complete original graph rejected", e)
	}
	training := n.Training
	n.Training = nil
	if e := n.CheckRecoveryOwners(); e == nil {
		t.Fatal("enabled recovery owner omitted")
	}
	n.Training = training
	if e := n.CheckRecoveryOwners(); e != nil {
		t.Fatal("restored current owner rejected", e)
	}
}
