package distribution_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"strings"
	"testing"
)

type access struct {
	alias   distribution.Alias
	gateway distribution.Provisioned
	key     distribution.MintedKey
	owner   identity.Auth
}

func (c scenario) access(t *testing.T) access {
	t.Helper()
	ctx := context.Background()
	deployment := c.readyDeployment(t)
	gateway, e := c.s.Provision(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	alias, e := c.s.Publish(ctx, c.a, distribution.PublishRequest{WorkspaceID: c.a.Me.Workspace.ID, GatewayID: gateway.Gateway.ID, Name: "public-alias", DeploymentID: deployment.ID, DeploymentRevision: deployment.Revision, Reason: "Synthetic publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	owner := c.user(t, "api-owner@example.invalid")
	key, e := c.s.MintKey(ctx, owner, keyInput(alias), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return access{alias: alias, gateway: gateway, key: key, owner: owner}
}
func (a access) input() wire.AdmissionRequest {
	return wire.AdmissionRequest{RequestID: uuid.NewString(), APIKey: *a.key.Secret, Model: a.alias.Name, MaximumOutputTokens: 16}
}
func success(id string) wire.Outcome {
	return wire.Outcome{RequestID: id, Status: "succeeded", Usage: &wire.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}, ElapsedMilliseconds: 1, OutputStarted: true}
}

func TestFreshMachineAuthorizationDurableAdmissionAndCanonicalIdempotentReceipt(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	access := c.access(t)
	models, e := c.s.AuthorizeModels(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, *access.key.Secret)
	if e != nil || len(models.Models) != 1 || models.GatewayID != access.gateway.Gateway.ID || models.NodeID != c.node.NodeID || models.WorkspaceID != access.alias.WorkspaceID || models.Models[0].Name != access.alias.Name {
		t.Fatal("fresh machine scope", e)
	}
	input := access.input()
	admitted, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString())
	if e != nil || admitted.RequestID != input.RequestID || admitted.Binding.ID != access.alias.ID {
		t.Fatal("durable admission", e)
	}
	_, e = c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString())
	check(t, e, "GATEWAY_REQUEST_EXISTS")
	outcome := success(input.RequestID)
	expected, _ := outcome.Digest()
	receipt, e := c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, outcome, uuid.NewString())
	if e != nil || receipt.CanonicalSHA256 != expected || receipt.RequestID != input.RequestID {
		t.Fatal("durable canonical receipt", e)
	}
	again, e := c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, outcome, uuid.NewString())
	if e != nil || again != receipt {
		t.Fatal("exact metadata replay", e)
	}
	changed := outcome
	changed.OutputStarted = false
	_, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, changed, uuid.NewString())
	check(t, e, "GATEWAY_OUTCOME_CONFLICT")
	var state string
	var held bool
	var reports int
	if e = c.s.DB.QueryRow(ctx, "SELECT state,capacity_held FROM gateway_requests WHERE id=$1", input.RequestID).Scan(&state, &held); e != nil || state != "reported" || held {
		t.Fatal("terminal state/capacity", e)
	}
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='gateway.request_reported'", input.RequestID).Scan(&reports); e != nil || reports != 1 {
		t.Fatal("receipt replay duplicated audit", e)
	}
	_, e = c.s.AuthorizeModels(ctx, access.gateway.Gateway.ID, *access.key.Secret, *access.key.Secret)
	check(t, e, "GATEWAY_UNAUTHORIZED")
	_, e = c.s.AuthorizeModels(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, *access.gateway.Secret)
	check(t, e, "API_KEY_INVALID")
	bad := "gw1." + access.gateway.Gateway.ID + "." + security.Token()
	_, e = c.s.AuthorizeModels(ctx, access.gateway.Gateway.ID, bad, *access.key.Secret)
	check(t, e, "GATEWAY_UNAUTHORIZED")
}

func TestRevocationFencesNewWorkWhileExistingAdmissionCanReport(t *testing.T) {
	for _, kind := range []string{"key", "alias", "gateway", "membership", "account"} {
		t.Run(kind, func(t *testing.T) {
			c := setup(t)
			ctx := context.Background()
			access := c.access(t)
			input := access.input()
			if _, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			expected := "API_KEY_INVALID"
			switch kind {
			case "key":
				if _, e := c.s.RevokeKey(ctx, access.owner, access.alias.WorkspaceID, access.key.Key.ID, "Synthetic revoke", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			case "alias":
				expected = "MODEL_ALIAS_UNAVAILABLE"
				if _, e := c.s.DisableAlias(ctx, c.a, access.alias.WorkspaceID, access.alias.ID, access.alias.Revision, "Synthetic disable", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			case "gateway":
				expected = "GATEWAY_UNAUTHORIZED"
				if _, e := c.s.RevokeGateway(ctx, c.a, access.alias.WorkspaceID, access.gateway.Gateway.ID, "Synthetic revoke", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			case "membership":
				var version int64
				if e := c.s.DB.QueryRow(ctx, "SELECT version FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", access.alias.WorkspaceID, access.owner.Me.User.ID).Scan(&version); e != nil {
					t.Fatal(e)
				}
				if _, e := c.s.Workspaces.ChangeMember(ctx, c.a, access.alias.WorkspaceID, access.owner.Me.User.ID, "", version, "Synthetic remove", uuid.NewString(), true); e != nil {
					t.Fatal(e)
				}
				if _, e := c.s.AuthorizeModels(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, *access.key.Secret); e == nil || e.Error() != "FORBIDDEN" {
					t.Fatal("removed membership admitted", e)
				}
				if _, e := c.s.Workspaces.AddMember(ctx, c.a, access.alias.WorkspaceID, "api-owner@example.invalid", "member", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				listed, e := c.s.Keys(ctx, c.a, access.alias.WorkspaceID)
				if e != nil || len(listed) != 1 || listed[0].State != "invalidated" {
					t.Fatal("membership re-add revived old secret", e)
				}
			case "account":
				disabled, e := c.s.Identity.SetStatus(ctx, c.a, access.owner.Me.User.ID, "disabled", access.owner.Me.User.Version, "Synthetic disable", uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = c.s.Identity.SetStatus(ctx, c.a, disabled.ID, "active", disabled.Version, "Synthetic re-enable", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			}
			_, e := c.s.AuthorizeModels(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, *access.key.Secret)
			check(t, e, expected)
			_, e = c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, access.input(), uuid.NewString())
			check(t, e, expected)
			if _, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
				t.Fatal("prior metadata receipt incorrectly depends on current user authority", e)
			}
		})
	}
}

func TestInvalidKeyCannotPoisonValidRateBucket(t *testing.T) {
	c := setup(t)
	policy := config.DefaultRuntimePolicy()
	policy.Distribution.RequestsPerWindow = 1
	c.s.Identity.Config.Runtime = &policy
	access := c.access(t)
	ctx := context.Background()
	bad := access.input()
	pieces := strings.Split(bad.APIKey, ".")
	bad.APIKey = pieces[0] + "." + pieces[1] + "." + security.Token()
	for i := 0; i < 3; i++ {
		bad.RequestID = uuid.NewString()
		_, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, bad, uuid.NewString())
		check(t, e, "API_KEY_INVALID")
	}
	input := access.input()
	if _, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal("invalid key poisoned valid admission", e)
	}
	if _, e := c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, access.input(), uuid.NewString())
	check(t, e, "RATE_LIMITED")
}

func TestOverdueAndIncompleteUsageRetainCapacityWithoutFabricatedTotals(t *testing.T) {
	c := setup(t)
	policy := config.DefaultRuntimePolicy()
	policy.Distribution.MaxConcurrentRequests = 1
	c.s.Identity.Config.Runtime = &policy
	access := c.access(t)
	ctx := context.Background()
	input := access.input()
	if _, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e := c.s.DB.Exec(ctx, "UPDATE gateway_requests SET admitted_at=clock_timestamp()-interval '2 hours',deadline_at=clock_timestamp()-interval '1 hour' WHERE id=$1", input.RequestID); e != nil {
		t.Fatal(e)
	}
	_, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, access.input(), uuid.NewString())
	check(t, e, "RATE_LIMITED")
	code := "gateway_interrupted"
	outcome := wire.Outcome{RequestID: input.RequestID, Status: "incomplete", ErrorCode: &code}
	if _, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, outcome, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var held, unknown bool
	if e = c.s.DB.QueryRow(ctx, "SELECT capacity_held,outcome->'usage'='null'::jsonb FROM gateway_requests WHERE id=$1", input.RequestID).Scan(&held, &unknown); e != nil || !held || !unknown {
		t.Fatal("unknown outcome freed capacity or invented usage", e)
	}
	_, e = c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, access.input(), uuid.NewString())
	check(t, e, "RATE_LIMITED")
}

func TestReceiptAuditFailureRollsBackAndRejectsExcessUsage(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	access := c.access(t)
	input := access.input()
	if _, e := c.s.Admit(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	bad := success(input.RequestID)
	bad.Usage.CompletionTokens = 17
	bad.Usage.TotalTokens = 24
	_, e := c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, bad, uuid.NewString())
	check(t, e, "GATEWAY_USAGE_INVALID")
	if _, e = c.s.DB.Exec(ctx, `CREATE FUNCTION synthetic_receipt_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='gateway.request_reported' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER synthetic_receipt_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION synthetic_receipt_audit_failure();`); e != nil {
		t.Fatal(e)
	}
	_, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(input.RequestID), uuid.NewString())
	if e == nil {
		t.Fatal("failed audit acknowledged usage")
	}
	var held, missing bool
	if e = c.s.DB.QueryRow(ctx, "SELECT capacity_held,outcome IS NULL FROM gateway_requests WHERE id=$1", input.RequestID).Scan(&held, &missing); e != nil || !held || !missing {
		t.Fatal("failed audit partially committed receipt", e)
	}
	if _, e = c.s.DB.Exec(ctx, "DROP TRIGGER synthetic_receipt_audit_failure ON audit_events; DROP FUNCTION synthetic_receipt_audit_failure()"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Report(ctx, access.gateway.Gateway.ID, *access.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
		t.Fatal("metadata-only retry failed after repair", e)
	}
}
