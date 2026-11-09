//go:build repository_integration

package nodes_test

import (
	"crypto/sha256"
	"reflect"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

func TestActualObservedInventory(t *testing.T) {
	x := newNodeReadActual(t)
	if x.f.Store.Dialect() != transaction.PostgreSQL {
		t.Skip("observed inventory fixture requires PostgreSQL")
	}
	x.s.Inference = &deployments.Service{Nodes: x.s, Models: &models.Service{DB: x.f.PostgreSQL, Identity: x.owner, Workspaces: x.s.Workspaces}}
	observed := facts()
	observed.DiskAvailableBytes = 0
	enroll := func(profile, name string) (wire.Enrolled, wire.Handshake) {
		t.Helper()
		h := nodeEnrollmentInput(profile)
		h.Facts = observed
		if profile == wire.ReadOnlyProfile {
			h.ExecutionProfile = ""
		}
		token := nodeEnrollmentMint(t, x, x.f.Workspace, name, profile)
		n, err := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: token.Token, Handshake: h}, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		return n, h
	}
	read := func(id string) nodes.ObservedInventory {
		t.Helper()
		out, err := x.s.ObservedInventory(x.ctx, x.portal, x.f.Workspace, id)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	legacy, legacyHandshake := enroll(wire.ReadOnlyProfile, "Inventory legacy node")
	if _, err := x.s.Heartbeat(x.ctx, legacy.NodeID, legacy.Credential, legacyHandshake); err != nil {
		t.Fatal(err)
	}
	out := read(legacy.NodeID)
	if out.NodeID != legacy.NodeID || out.TenantID != x.f.Tenant || out.WorkspaceID != x.f.Workspace || out.SiteID != legacy.SiteID || out.CellID != legacy.CellID || out.GPUs != nil {
		t.Fatal("legacy node scope or unknown GPUs changed", out)
	}
	if out.Host == nil || out.Host.Hostname != observed.Hostname || out.Host.LogicalCPUs != observed.LogicalCPUs || !out.Host.ObservedAt.Equal(observed.ObservedAt) || out.Host.Filesystem.TotalBytes != observed.DiskTotalBytes || out.Host.Filesystem.AvailableBytes == nil || *out.Host.Filesystem.AvailableBytes != 0 {
		t.Fatal("persisted host sample or measured zero changed", out.Host)
	}

	x.exec(t, `UPDATE managed_nodes SET facts=(facts-'disk_available_bytes') || '{"site_id":"forged-site","cell_id":"forged-cell"}'::jsonb WHERE id=$1`, "", legacy.NodeID)
	out = read(legacy.NodeID)
	if out.Host == nil || out.Host.Filesystem.AvailableBytes != nil || out.SiteID != legacy.SiteID || out.CellID != legacy.CellID {
		t.Fatal("absent count was measured or facts replaced authoritative scope", out)
	}
	x.exec(t, `UPDATE managed_nodes SET facts=facts || '{"disk_available_bytes":null}'::jsonb WHERE id=$1`, "", legacy.NodeID)
	if out = read(legacy.NodeID); out.Host == nil || out.Host.Filesystem.AvailableBytes != nil {
		t.Fatal("null available count was measured", out.Host)
	}
	if out = read(x.ids[0]); out.Host != nil || out.GPUs != nil {
		t.Fatal("incomplete historical record yielded a hardware observation", out)
	}

	node, h := enroll(wire.DockerInferenceProfile, "Inventory inference node")
	if out = read(node.NodeID); out.GPUs != nil {
		t.Fatal("node with no probe reported an empty device list")
	}
	h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: observed.ObservedAt}
	heartbeat := func() {
		t.Helper()
		if _, err := x.s.Heartbeat(x.ctx, node.NodeID, node.Credential, h); err != nil {
			t.Fatal(err)
		}
	}
	heartbeat()
	if out = read(node.NodeID); out.GPUs == nil || out.GPUs.Devices == nil || len(out.GPUs.Devices) != 0 || !out.GPUs.ObservedAt.Equal(observed.ObservedAt) {
		t.Fatal("successful empty GPU probe became unknown", out.GPUs)
	}
	h.Inference.ProbeErrorCode = "INFERENCE_NVIDIA_UNAVAILABLE"
	heartbeat()
	if out = read(node.NodeID); out.GPUs != nil {
		t.Fatal("failed GPU probe became a measured empty list", out.GPUs)
	}
	a := wire.NVIDIAGPU{UUID: "GPU-00000000-0000-0000-0000-000000000001", Name: "Synthetic GPU", DriverVersion: "580.65.06", MemoryTotalMiB: 8192, ComputeCapability: 8.9, MIGMode: "Disabled", ObservedAt: observed.ObservedAt}
	b := a
	b.UUID = "GPU-00000000-0000-0000-0000-000000000002"
	h.Inference.GPUs = []wire.NVIDIAGPU{b, a}
	h.Inference.ProbeErrorCode = "INFERENCE_IMAGE_MISSING"
	heartbeat()
	if out = read(node.NodeID); out.GPUs == nil || len(out.GPUs.Devices) != 2 || out.GPUs.Devices[0].UUID != a.UUID || out.GPUs.Devices[1].UUID != b.UUID || out.GPUs.Devices[0].MemoryTotalMiB != a.MemoryTotalMiB {
		t.Fatal("positive GPU observations were lost or not ordered by identity", out.GPUs)
	}
	x.exec(t, `UPDATE inference_nodes SET received_at=clock_timestamp()-interval '1 day' WHERE node_id=$1`, "", node.NodeID)
	if out = read(node.NodeID); out.GPUs != nil || out.Host == nil {
		t.Fatal("stale GPU probe displaced the independent host observation", out)
	}

	assertRefused := func(actor identity.Auth, workspace, id string) {
		t.Helper()
		out, err := x.s.ObservedInventory(x.ctx, actor, workspace, id)
		nodeReadWant(t, err, "RESOURCE_NOT_FOUND")
		if !reflect.DeepEqual(out, nodes.ObservedInventory{}) {
			t.Fatal("refused inventory returned a projection", out)
		}
	}
	tenant, workspace, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	credential := sha256.Sum256([]byte(uuid.NewString()))
	x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign inventory tenant','active')`, "", tenant)
	x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign inventory workspace','active')`, "", workspace, tenant)
	x.exec(t, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES($1,$2,$3,'Foreign node','active',$4,'1.0','{}',clock_timestamp())`, "", foreign, tenant, workspace, credential[:])
	assertRefused(x.admin, "", foreign)
	assertRefused(x.admin, workspace, foreign)
	assertRefused(x.portal, x.f.Workspace, foreign)
	assertRefused(x.portal, "", node.NodeID)
	sibling := uuid.NewString()
	x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Sibling inventory workspace','active')`, "", sibling, x.f.Tenant)
	assertRefused(x.admin, sibling, node.NodeID)
	assertRefused(x.portal, sibling, node.NodeID)
	if out, err := x.s.ObservedInventory(x.ctx, x.admin, "", node.NodeID); err != nil || out.WorkspaceID != x.f.Workspace {
		t.Fatal("admin node scope discovery returned the wrong workspace", out, err)
	}
	x.exec(t, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, "", x.f.User, x.f.Workspace)
	assertRefused(x.portal, x.f.Workspace, node.NodeID)
}
