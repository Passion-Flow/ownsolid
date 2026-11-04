package deployments_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

// Machine/GPU/model observations and result proofs are explicitly synthetic.
// TLS, License, authorization, CSRF, typed commands/results, SQL and audits are real.
func TestM19ExpiredLicenseDeploymentRecoveryHTTPAndAgentCompletion(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	owner := c.s.Nodes.Identity
	handler := transport.New(owner)
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/deployments/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	machine := httpfixture.New(t, server, "admin", schemas)
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	}
	machine.Headers = map[string]string{"Authorization": "Bearer " + c.node.Credential}
	base := "/agent/v1/sites/default/cells/default/nodes/" + c.node.NodeID
	claim := func() wire.Lease {
		t.Helper()
		value := machine.Call("POST", base+"/tasks/claims", map[string]any{}, 200, "ClaimResponse")
		raw, _ := json.Marshal(value["lease"])
		var lease wire.Lease
		if json.Unmarshal(raw, &lease) != nil || lease.Inference == nil {
			t.Fatal("actual typed recovery lease")
		}
		return lease
	}
	finish := func(lease wire.Lease, state string) {
		t.Helper()
		machine.Call("POST", base+"/tasks/"+lease.TaskID+"/renewals", wire.LeaseRenewal{LeaseToken: lease.LeaseToken}, 200, "RenewedLeaseResponse")
		machine.Call("POST", base+"/tasks/"+lease.TaskID+"/results", result(lease, state), 200, "CompletedResponse")
	}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	created := admin.Write("POST", "/api/v1/admin/deployments", c.input(), 202, "DeploymentResponse")
	id := created["id"].(string)
	revision := int64(created["revision"].(float64))
	path := "/api/v1/admin/deployments/" + id
	trust, e := owner.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	expired, e := licensing.New(owner, trust, owner.Config.LicensePolicy(), func() time.Time { return time.Now().UTC().Add(26 * time.Hour) })
	if e != nil {
		t.Fatal(e)
	}
	owner.BusinessAdmission = expired
	// An already accepted deployment may still be claimed and completed after expiry.
	initial := claim()
	finish(initial, "running")
	for _, attempt := range []struct {
		path  string
		input any
	}{{"/api/v1/admin/deployments", c.input()}, {path + "/actions", map[string]any{"action": "deploy", "revision": revision, "reason": "Synthetic denied redeploy"}}} {
		admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		problem := admin.Write("POST", attempt.path, attempt.input, 403, "Problem")
		if problem["code"] != "LICENSE_NEW_BUSINESS_UNAVAILABLE" {
			t.Fatal("unexpected new deployment denial")
		}
	}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	admin.Write("POST", path+"/actions", map[string]any{"action": "stop", "revision": revision + 1, "reason": "Synthetic stale recovery"}, 409, "Problem")
	admin.CSRF = ""
	admin.Call("POST", path+"/actions", map[string]any{"action": "stop", "revision": revision, "reason": "No CSRF"}, 403, "Problem")
	admin.Write("POST", path+"/actions", map[string]any{"action": "stop", "revision": revision, "reason": "Strict recovery fields", "host": "unapproved"}, 400, "Problem")
	portal.Call("GET", "/api/v1/workspaces/"+uuid.NewString()+"/deployments/"+id, nil, 404, "Problem")
	for _, action := range []string{"observe", "verify", "stop", "remove"} {
		admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
		changed := admin.Write("POST", path+"/actions", map[string]any{"action": action, "revision": revision, "reason": "Synthetic expired deployment recovery"}, 202, "DeploymentResponse")
		revision++
		if changed["revision"] != float64(revision) {
			t.Fatal("recovery revision not persisted")
		}
		if action == "stop" && changed["resource_state"] != "held" {
			t.Fatal("stop intent prematurely released capacity")
		}
		lease := claim()
		if lease.Inference.Action != action || lease.Inference.DeploymentID != id || lease.Inference.Revision != revision {
			t.Fatal("recovery body/command interpretation diverged")
		}
		state := "running"
		if action == "stop" {
			state = "stopped"
		}
		if action == "remove" {
			state = "not_installed"
		}
		finish(lease, state)
		current := admin.Call("GET", path, nil, 200, "DeploymentResponse")
		if (action == "stop" || action == "remove") && current["resource_state"] != "released" {
			t.Fatal("confirmed recovery retained incorrect capacity")
		}
	}
	current := admin.Call("GET", path, nil, 200, "DeploymentResponse")
	if current["removed_at"] == nil {
		t.Fatal("confirmed removal not durable")
	}
	var tasks, audits int
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM node_tasks WHERE node_id=$1", c.node.NodeID).Scan(&tasks); e != nil || tasks != 5 {
		t.Fatal("denied work inserted extra task", e, tasks)
	}
	if e = owner.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events a JOIN node_tasks n ON a.target_id=n.id WHERE n.node_id=$1 AND a.action='node.task_succeeded'", c.node.NodeID).Scan(&audits); e != nil || audits != 5 {
		t.Fatal("exact completion audits missing", e, audits)
	}
}
