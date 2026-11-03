//go:build repository_integration

package http_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
)

func enrollmentHTTPAudits(t *testing.T, x pairedNodeHTTP) int {
	t.Helper()
	var count int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM audit_events WHERE action='node.enrolled' AND audience='system'", "SELECT count(*) FROM audit_events WHERE action='node.enrolled' AND audience='system'")).Scan(&count); e != nil {
		t.Fatal(e)
	}
	return count
}

func enrollmentHTTPRefusal(t *testing.T, x pairedNodeHTTP, raw []byte, path, media, audience string, cookies bool, status int, code string) {
	t.Helper()
	before, audits := nodeHTTPStoredDigest(t, x), enrollmentHTTPAudits(t, x)
	r, e := http.NewRequestWithContext(x.ctx, "POST", x.admin.s.URL+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	r.Host = audience + ".example.invalid"
	r.Header.Set("Content-Type", media)
	if cookies {
		for _, value := range x.admin.cookies {
			r.AddCookie(value)
		}
	}
	response, e := x.admin.s.Client().Do(r)
	if e != nil {
		t.Fatal("actual trusted HTTPS enrollment refusal transport", e)
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 65536))
	var problem map[string]any
	if e != nil || len(body) == 65536 || response.StatusCode != status || response.Header.Get("Content-Type") != "application/problem+json" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" || json.Unmarshal(body, &problem) != nil || len(problem) != 5 || problem["code"] != code || problem["request_id"] != response.Header.Get("X-Request-ID") {
		t.Fatal("actual enrollment refusal canonical Problem mismatch", response.StatusCode, code)
	}
	if e = x.admin.schemas["Problem"].Validate(problem); e != nil {
		t.Fatal(e)
	}
	if status == 429 {
		seconds, e := strconv.Atoi(response.Header.Get("Retry-After"))
		if e != nil || seconds < 1 || seconds > x.owner.Config.Policy().Rates.AgentEnrollIPWindowSeconds {
			t.Fatal("actual persisted enrollment rate retry interval invalid")
		}
	} else if response.Header.Get("Retry-After") != "" {
		t.Fatal("actual enrollment refusal unexpectedly advises retry")
	}
	nodeHTTPSNoSecrets(t, problem)
	if nodeHTTPStoredDigest(t, x) != before || enrollmentHTTPAudits(t, x) != audits {
		t.Fatal("actual enrollment refusal changed node/token/task/system audit state")
	}
}

func TestM20PairedNodeEnrollmentHTTPAuthority(t *testing.T) {
	for _, kind := range []string{"strict_json_and_entrance", "token_state", "profile_refused", "inactive_workspace", "inactive_tenant", "enrollment_rate"} {
		t.Run(kind, func(t *testing.T) {
			x := newPairedNodeHTTP(t)
			pending := nodeJoinHTTPMint(t, x, wire.ReadOnlyProfile)
			input := enrollmentHTTPInput(pending["token"].(string))
			encode := func(value any) []byte {
				raw, e := json.Marshal(value)
				if e != nil {
					t.Fatal(e)
				}
				return raw
			}
			refuse := func(value any, status int, code string) {
				enrollmentHTTPRefusal(t, x, encode(value), "/agent/v1/enroll", "application/json", "admin", false, status, code)
			}
			switch kind {
			case "strict_json_and_entrance":
				raw := encode(input)
				for _, field := range []string{"tenant_id", "workspace_id"} {
					var body map[string]any
					if e := json.Unmarshal(raw, &body); e != nil {
						t.Fatal(e)
					}
					body[field] = "untrusted-client-override"
					refuse(body, 400, "VALIDATION_FAILED")
				}
				duplicate := append([]byte(`{"join_token":"synthetic-duplicate",`), raw[1:]...)
				for _, body := range [][]byte{duplicate, append(append([]byte(nil), raw...), []byte(" {}")...), []byte("{")} {
					enrollmentHTTPRefusal(t, x, body, "/agent/v1/enroll", "application/json", "admin", false, 400, "VALIDATION_FAILED")
				}
				enrollmentHTTPRefusal(t, x, raw, "/agent/v1/enroll?workspace_id=untrusted", "application/json", "admin", false, 400, "VALIDATION_FAILED")
				enrollmentHTTPRefusal(t, x, raw, "/agent/v1/enroll", "text/plain", "admin", false, 415, "UNSUPPORTED_MEDIA_TYPE")
				enrollmentHTTPRefusal(t, x, raw, "/agent/v1/enroll", "application/json", "portal", false, 404, "RESOURCE_NOT_FOUND")
				input.JoinToken = ""
				enrollmentHTTPRefusal(t, x, encode(input), "/agent/v1/enroll", "application/json", "admin", true, 400, "TOKEN_UNUSABLE")
			case "token_state":
				nodeJoinHTTPRevoke(x, pending["id"].(string))
				refuse(input, 400, "TOKEN_UNUSABLE")
				expired := nodeJoinHTTPMint(t, x, wire.ReadOnlyProfile)
				x.exec(t, "UPDATE node_join_tokens SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE node_join_tokens SET expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", expired["id"])
				input.JoinToken = expired["token"].(string)
				refuse(input, 400, "TOKEN_UNUSABLE")
				input.JoinToken = security.Token()
				refuse(input, 400, "TOKEN_UNUSABLE")
			case "profile_refused":
				input.ExecutionProfile = wire.SystemdComponentsProfile
				input.ProtocolVersion = wire.ProfileVersion(input.ExecutionProfile)
				input.Capabilities = wire.Capabilities(input.ExecutionProfile)
				refuse(input, 403, "AGENT_PROFILE_REJECTED")
				input = enrollmentHTTPInput(input.JoinToken)
				input.ProtocolVersion = "synthetic-unsupported"
				refuse(input, 400, "AGENT_PROTOCOL_UNSUPPORTED")
			case "inactive_workspace":
				x.exec(t, "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1", "UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?", x.f.Workspace)
				refuse(input, 403, "ACCESS_DENIED")
			case "inactive_tenant":
				x.exec(t, "UPDATE tenants SET status='disabled' WHERE id=$1", "UPDATE tenants SET status='disabled' WHERE id=?", x.f.Tenant)
				refuse(input, 403, "ACCESS_DENIED")
			case "enrollment_rate":
				policy := x.owner.Config.Policy()
				policy.Rates.AgentEnrollIPCount = 1
				x.owner.Config.Runtime = &policy
				invalid := input
				invalid.JoinToken = security.Token()
				refuse(invalid, 400, "TOKEN_UNUSABLE")
				refuse(input, 429, "RATE_LIMITED")
			}
		})
	}
}

func TestM20PairedNodeEnrollmentHTTPLicenseContinuation(t *testing.T) {
	x := newPairedNodeHTTP(t)
	pending := nodeJoinHTTPMint(t, x, wire.ReadOnlyProfile)
	trust, e := x.owner.Config.LicenseTrust()
	if e != nil {
		t.Fatal(e)
	}
	// Only the trusted test clock is injected. Actual signed history, SQL locks,
	// current user/session authority and admission implementation remain real.
	licenses, e := licensing.New(x.owner, trust, x.owner.Config.LicensePolicy(), func() time.Time { return time.Now().UTC().Add(3 * time.Hour) })
	if e != nil {
		t.Fatal(e)
	}
	x.owner.BusinessAdmission, x.handler.Licensing.Service = licenses, licenses
	state := x.admin.request("GET", "/api/v1/admin/license", nil, 200, "", nil)
	if state["state"] != "expired" || state["new_business_allowed"] != false {
		t.Fatal("actual signed License did not become expired under explicit fixture clock")
	}
	problem := x.admin.mutate("POST", nodeJoinHTTPPath, nodeJoinHTTPBody(x.f.Workspace, wire.ReadOnlyProfile), 403, "Problem")
	if problem["code"] != "LICENSE_NEW_BUSINESS_UNAVAILABLE" {
		t.Fatal("expired actual License admitted a new token")
	}
	server, log := enrollmentHTTPServer(t, x)
	result := enrollmentHTTPProbe(t, x, server, enrollmentHTTPInput(pending["token"].(string)), "acknowledged")
	if result.Enrolled == nil || len(log.values()) != 1 {
		t.Fatal("preissued valid token did not finish through actual client after License expiry")
	}
	var nodeID string
	var consumed bool
	if e = x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT node_id::text,consumed_at IS NOT NULL FROM node_join_tokens WHERE id=$1", "SELECT node_id,consumed_at IS NOT NULL FROM node_join_tokens WHERE id=?", pending["id"])).Scan(&nodeID, &consumed); e != nil || !consumed || nodeID != result.Enrolled.NodeID {
		t.Fatal("actual License continuation token consumption/linkage failed")
	}
	var hash []byte
	if e = x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT credential_hash FROM managed_nodes WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3", "SELECT credential_hash FROM managed_nodes WHERE id=? AND tenant_id=? AND workspace_id=?", nodeID, x.f.Tenant, x.f.Workspace)).Scan(&hash); e != nil || !bytes.Equal(hash, security.Digest("node-credential:v1:"+result.Enrolled.Credential)) || enrollmentHTTPAudits(t, x) != 1 {
		t.Fatal("actual License continuation credential/scope/system audit mismatch")
	}
}
