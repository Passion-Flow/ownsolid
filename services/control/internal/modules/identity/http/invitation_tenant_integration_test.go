package http_test

import (
	"net/http"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestInvitationTenantIsolation(t *testing.T) {
	s, _, portal, admin, password := setup(t)
	const adminEmail = "admin@example.invalid"
	admin.mutate(http.MethodPost, "/api/v1/auth/login", map[string]any{
		"email": adminEmail, "password": password,
	}, http.StatusOK, "LoginResponse")
	const invitations = "/api/v1/admin/invitations"
	const deliveries = "/api/v1/admin/mail-deliveries"
	local := admin.mutate(http.MethodPost, invitations, map[string]any{
		"email": "local-invite@example.invalid", "locale": "en",
	}, http.StatusCreated, "InvitationResponse")
	localID := local["id"].(string)
	localDelivery := local["delivery"].(map[string]any)["id"].(string)
	localToken := fixture.InvitationToken(t, s, localID)

	tenant, workspace, invitation, delivery := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	token := security.Token()
	const foreignEmail = "foreign-invite@example.invalid"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Other tenant','active')", tenant)
	exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other workspace','active')", workspace, tenant)
	exec(`INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at)
		SELECT $1,$2,$3,$4,'en','pending',$5,primary_admin_user_id,now(),now(),now()+interval '1 hour'
		FROM installation_state WHERE id=1`, invitation, tenant, workspace, foreignEmail, security.Digest(token))
	exec(`INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,invitation_id,invitation_generation,state,
			next_attempt_at,message_id,created_at,updated_at,completed_at)
		VALUES($1,'invitation',$2,'en',$3,1,'sent',now(),$4,now(),now(),now())`,
		delivery, foreignEmail, invitation, "<"+delivery+"@example.invalid>")

	for _, list := range []struct{ path, schema, localID string }{
		{invitations, "InvitationListResponse", localID},
		{deliveries, "MailListResponse", localDelivery},
	} {
		t.Run(list.schema, func(t *testing.T) {
			actor := *admin
			actor.t = t
			response := actor.request(http.MethodGet, list.path+"?limit=1", nil, http.StatusOK, list.schema, nil)
			items := response["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["id"] != list.localID || response["next_cursor"] != nil {
				t.Fatal("list did not contain exactly the installation tenant's record")
			}
			problem := actor.request(http.MethodGet, list.path+"?tenant_id="+tenant, nil, http.StatusBadRequest, "Problem", nil)
			if problem["code"] != "VALIDATION_FAILED" {
				t.Fatal("unexpected tenant selector response", problem["code"])
			}
		})
	}

	assertCode := func(c *client, path string, body map[string]any, status int, code string) {
		t.Helper()
		problem := c.mutate(http.MethodPost, path, body, status, "Problem")
		if problem["code"] != code {
			t.Fatalf("%s: got %v, want %s", path, problem["code"], code)
		}
	}
	assertCode(admin, invitations, map[string]any{"email": foreignEmail, "locale": "en", "tenant_id": tenant}, http.StatusBadRequest, "VALIDATION_FAILED")
	assertCode(admin, invitations+"/"+invitation+"/resends", map[string]any{"version": 1}, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	assertCode(admin, invitations+"/"+invitation+"/revocations", map[string]any{"version": 1, "reason": "Synthetic fixture"}, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	assertCode(admin, deliveries+"/"+delivery+"/resends", map[string]any{}, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	assertCode(portal, "/api/v1/auth/invitation-validations", map[string]any{"token": token}, http.StatusBadRequest, "TOKEN_UNUSABLE")
	assertCode(portal, "/api/v1/auth/register", map[string]any{
		"token": token, "display_name": "Other tenant", "password": fixture.Password(),
	}, http.StatusBadRequest, "TOKEN_UNUSABLE")
	portal.mutate(http.MethodPost, "/api/v1/auth/invitation-validations", map[string]any{"token": localToken}, http.StatusOK, "InvitationValidationResponse")

	var unchanged bool
	if err := s.DB.QueryRow(t.Context(), `SELECT status='pending' AND version=1 AND token_hash=$2
		AND (SELECT count(*) FROM mail_deliveries WHERE invitation_id=$1)=1
		AND NOT EXISTS(SELECT 1 FROM users WHERE email_canonical=$3)
		FROM invitations WHERE id=$1`, invitation, security.Digest(token), foreignEmail).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("denied requests changed the other tenant's records", err)
	}
}
