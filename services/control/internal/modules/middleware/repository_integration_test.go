//go:build repository_integration

package middleware

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware/testschema"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// These are the exact unnumbered owning M26 candidate statements, not toy tables.
// The fixture applies them only after the real baseline in a fresh owned scope;
// this does not activate a production migration or qualify complete schema DDL.
//
//go:embed schema/postgresql/*.sql schema/mysql/*.sql
var candidateSchema embed.FS

type paired struct {
	f                    repositoryfixture.Fixture
	ctx                  context.Context
	s                    *Service
	accounts             *identity.Service
	admin, owner, viewer identity.Auth
	configuration        Configuration
	clock                *time.Time
}

func (p paired) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := p.f.Store.Exec(p.ctx, p.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual selected fixture SQL", e)
	}
}

func pairedSetup(t *testing.T) paired {
	return pairedSetupWithConfiguration(t, nil)
}

func pairedSetupWithConfiguration(t *testing.T, configured *Configuration) paired {
	t.Helper()
	var f repositoryfixture.Fixture
	budget := 90 * time.Second
	if configured == nil {
		f = repositoryfixture.New(t)
	} else {
		f = repositoryfixture.NewRecoverableUnbootstrapped(t)
		f.Tenant = configured.Profiles[0].ExpectedTenantID
		budget = time.Duration(2*configured.Policy.OperationSeconds+10*configured.Policy.RequestSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	t.Cleanup(cancel)
	p := paired{f: f, ctx: ctx}
	if configured != nil {
		// A fresh disposable control-plane fixture adopts the real synthetic
		// source tenant identity; the native source itself is never reseeded.
		p.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Native fixture tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Native fixture tenant','active')", f.Tenant)
		p.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Native fixture Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Native fixture Workspace','active')", f.Workspace, f.Tenant)
		p.exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,'native-synthetic@example.invalid','native-synthetic@example.invalid','Native actor','en','active',true)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,'native-synthetic@example.invalid','native-synthetic@example.invalid','Native actor','en','active',true)", f.User)
		p.exec(t, "UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1", "UPDATE installation_state SET bootstrap_complete=true,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=UTC_TIMESTAMP(6) WHERE id=1", f.Tenant, f.Workspace, f.User)
	}
	testschema.Prepare(t, ctx, f, candidateSchema)

	accounts := &identity.Service{Storage: f.Store}
	p.accounts = accounts
	p.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	issue := func(user, audience string) identity.Auth {
		var session identity.IssuedSession
		if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			session, e = accounts.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		a, e := accounts.Authenticate(ctx, session.Token, audience)
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	p.admin = issue(f.User, "admin")
	p.owner = issue(f.User, "portal")
	viewer := uuid.NewString()
	email := "synthetic-" + viewer + "@example.invalid"
	p.exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'Synthetic viewer','en','active')", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'Synthetic viewer','en','active')", viewer, email, email)
	p.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'viewer')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'viewer')", viewer, f.Tenant, f.Workspace)
	p.viewer = issue(viewer, "portal")
	key, trust, e := license.GenerateKey("synthetic-m26-isolated-license")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second).Add(5 * time.Second)
	p.clock = &now
	licenses, e := licensing.New(accounts, trust, license.DefaultPolicy(), func() time.Time { return *p.clock })
	if e != nil {
		t.Fatal(e)
	}
	accounts.BusinessAdmission = licenses
	state, e := licenses.StatusAdmin(ctx, p.admin)
	if e != nil {
		t.Fatal(e)
	}
	stamp := func(offset time.Duration) string { return now.Add(offset).Format(time.RFC3339) }
	claims := license.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic M26 control-plane fixture", DeploymentID: state.DeploymentID, IssuedAt: stamp(-time.Second), NotBefore: stamp(-time.Second), ExpiresAt: stamp(time.Hour), GraceUntil: stamp(2 * time.Hour), Scope: license.WholePlatformScope}
	artifact, e := license.Issue(claims, key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = licenses.Import(ctx, p.admin, licensing.ImportInput{Artifact: string(artifact), Reason: "Synthetic signed M26 fixture"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if configured != nil {
		p.configuration = *configured
		p.s, e = New(accounts, &workspaces.Service{Identity: accounts}, *configured)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private external TMPDIR required")
	}
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Explicit synthetic M26 parser CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, certificate, certificate, &private.PublicKey, private)
	if e != nil {
		t.Fatal(e)
	}
	password := make([]byte, 32)
	if _, e = rand.Read(password); e != nil {
		t.Fatal(e)
	}
	passwordFile, caFile := filepath.Join(root, "password"), filepath.Join(root, "ca.pem")
	if os.WriteFile(passwordFile, []byte(hex.EncodeToString(password)), 0600) != nil || os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600) != nil {
		t.Fatal("private fixture input")
	}
	c := Configuration{FormatVersion: 1, Policy: DefaultPolicy(), Profiles: []Profile{{Key: "synthetic_readonly", Kind: CustomerExternal, Host: "127.0.0.1", Port: 1, Database: "synthetic", User: "readonly", PasswordFile: passwordFile, CAFile: caFile, ExpectedMajor: 18}}}
	c.Policy.PageSize = 2
	// Port1 is explicit non-serving synthetic profile metadata. These core cases
	// never claim endpoint TLS/probe or native artifact qualification.
	p.configuration = c
	p.s, e = New(accounts, &workspaces.Service{Identity: accounts}, c)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func (p paired) register(t *testing.T) (Instance, string) {
	t.Helper()
	key := uuid.NewString()
	v, e := p.s.Register(p.ctx, p.owner, p.f.Workspace, p.configuration.Profiles[0].Key, "Synthetic maintenance \u4e16\u754c", key, uuid.NewString())
	if e != nil {
		t.Fatal("actual registration", e)
	}
	return v, key
}
func wantCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("expected %s,got %v", code, e)
	}
}
func (p paired) count(t *testing.T, pg, my string, args ...any) int {
	t.Helper()
	var n int
	if e := p.f.Store.QueryRow(p.ctx, p.f.Statement(pg, my, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestActualPairedMiddlewareRegistration(t *testing.T) {
	for _, name := range []string{"registration_persists_exact_intent_and_audit", "changed_intent_conflicts", "profile_drift_explicit_revision_fences_old_job", "portal_viewer_cannot_mutate", "removed_membership_current_denial", "admin_revocation_current_denial", "cross_workspace_instance_hidden", "signed_license_expiry_blocks_new_job", "external_backup_refused", "false_isolated_zero_job", "queued_authority_loss_terminal_zero_output", "expired_running_unknown_never_replayed", "terminal_intent_history_immutable", "bounded_history_and_original_actor_intent", "reopened_actual_selected_integrity", "borrowed_restore_atomic_fence"} {
		t.Run(name, func(t *testing.T) {
			p := pairedSetup(t)
			v, key := p.register(t)
			health := func() OperationInput {
				return OperationInput{Kind: "health", Reason: "Explicit synthetic control-plane case", IdempotencyKey: uuid.NewString(), Version: v.Version}
			}
			switch name {
			case "registration_persists_exact_intent_and_audit":
				again, e := p.s.Register(p.ctx, p.owner, p.f.Workspace, v.ProfileKey, v.Name, key, uuid.NewString())
				if e != nil || again.ID != v.ID {
					t.Fatal("idempotent actual registration", e)
				}
				type result struct {
					value Instance
					err   error
				}
				results := make(chan result, 2)
				start := make(chan struct{})
				var joined sync.WaitGroup
				for range 2 {
					joined.Add(1)
					go func() {
						defer joined.Done()
						<-start
						value, err := p.s.Register(p.ctx, p.owner, p.f.Workspace, v.ProfileKey, v.Name, key, uuid.NewString())
						results <- result{value, err}
					}()
				}
				close(start)
				joined.Wait()
				for range 2 {
					value := <-results
					if value.err != nil || value.value.ID != v.ID {
						t.Fatal("actual concurrent original replay changed", value.err)
					}
				}
				got, e := p.s.RegistrationIntent(p.ctx, p.owner, p.f.Workspace, key)
				if e != nil || got.ID != v.ID {
					t.Fatal("actual original intent", e)
				}
				if n := p.count(t, "SELECT count(*) FROM audit_events WHERE action='middleware.registered' AND target_id=$1", "SELECT count(*) FROM audit_events WHERE action='middleware.registered' AND target_id=?", v.ID); n != 1 {
					t.Fatal("non-atomic or repeated audit", n)
				}
			case "changed_intent_conflicts":
				out, e := p.s.Register(p.ctx, p.owner, p.f.Workspace, v.ProfileKey, "Changed intent", key, uuid.NewString())
				wantCode(t, e, "IDEMPOTENCY_CONFLICT")
				if out.ID != "" {
					t.Fatal("conflict projected output")
				}
			case "profile_drift_explicit_revision_fences_old_job":
				job, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, health(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				if os.WriteFile(p.configuration.Profiles[0].PasswordFile, []byte("explicit_changed_synthetic_password"), 0600) != nil {
					t.Fatal("fixture drift")
				}
				_, e = p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, health(), uuid.NewString())
				wantCode(t, e, "MIDDLEWARE_INPUT_CHANGED")
				updated, e := p.s.Update(p.ctx, p.owner, p.f.Workspace, v.ID, v.Name, "active", v.Version, true, "Acknowledge actual synthetic input drift", uuid.NewString())
				if e != nil || updated.Version != v.Version+1 {
					t.Fatal(e)
				}
				if e = p.s.Pass(p.ctx); e != nil {
					t.Fatal(e)
				}
				old, e := p.s.Operation(p.ctx, p.owner, p.f.Workspace, job.ID)
				if e != nil || old.State != "failed" || old.Result != nil {
					t.Fatal("stale accepted job executed", e)
				}
			case "portal_viewer_cannot_mutate":
				_, e := p.s.Enqueue(p.ctx, p.viewer, p.f.Workspace, v.ID, health(), uuid.NewString())
				wantCode(t, e, "ACCESS_DENIED")
				if _, e = p.s.Get(p.ctx, p.viewer, p.f.Workspace, v.ID); e != nil {
					t.Fatal("actual viewer read", e)
				}
			case "removed_membership_current_denial":
				p.exec(t, "DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2", "DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?", p.f.User, p.f.Workspace)
				_, e := p.s.Get(p.ctx, p.owner, p.f.Workspace, v.ID)
				wantCode(t, e, "RESOURCE_NOT_FOUND")
			case "admin_revocation_current_denial":
				p.exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", p.f.User)
				_, e := p.s.Get(p.ctx, p.admin, p.f.Workspace, v.ID)
				wantCode(t, e, "AUTH_SESSION_EXPIRED")
			case "cross_workspace_instance_hidden":
				other := uuid.NewString()
				p.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other synthetic scope','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other synthetic scope','active')", other, p.f.Tenant)
				_, e := p.s.Get(p.ctx, p.admin, other, v.ID)
				wantCode(t, e, "RESOURCE_NOT_FOUND")
				foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
				p.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign synthetic tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Foreign synthetic tenant','active')", foreignTenant)
				p.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign tenant scope','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign tenant scope','active')", foreignWorkspace, foreignTenant)
				_, e = p.s.Get(p.ctx, p.admin, foreignWorkspace, v.ID)
				wantCode(t, e, "RESOURCE_NOT_FOUND")
			case "signed_license_expiry_blocks_new_job":
				*p.clock = p.clock.Add(3 * time.Hour)
				_, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, health(), uuid.NewString())
				wantCode(t, e, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
			case "external_backup_refused":
				input := health()
				input.Kind = "backup"
				_, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, input, uuid.NewString())
				wantCode(t, e, "MIDDLEWARE_EXTERNAL_MUTATION_REFUSED")
			case "false_isolated_zero_job":
				input := health()
				input.Kind = "isolated_restore"
				input.BackupID = uuid.NewString()
				input.TargetKey = "isolated"
				input.ConfirmIsolated = false
				_, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, input, uuid.NewString())
				wantCode(t, e, "MIDDLEWARE_ISOLATED_CONFIRMATION_REQUIRED")
				if n := p.count(t, "SELECT count(*) FROM middleware_operations", "SELECT count(*) FROM middleware_operations"); n != 0 {
					t.Fatal("false confirmation wrote a job")
				}
			case "queued_authority_loss_terminal_zero_output":
				job, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, health(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				p.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='logout' WHERE id=?", p.owner.SessionID)
				if e = p.s.Pass(p.ctx); e != nil {
					t.Fatal(e)
				}
				out, e := p.s.Operation(p.ctx, p.admin, p.f.Workspace, job.ID)
				if e != nil || out.State != "failed" || out.Result != nil {
					t.Fatal("lost actor obtained execution output", e)
				}
			case "expired_running_unknown_never_replayed":
				job, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, health(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				p.exec(t, "UPDATE middleware_operations SET state='running',attempt_id=$1,lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$2", "UPDATE middleware_operations SET state='running',attempt_id=?,lease_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", uuid.NewString(), job.ID)
				for range 2 {
					if e = p.s.Pass(p.ctx); e != nil {
						t.Fatal(e)
					}
				}
				out, e := p.s.Operation(p.ctx, p.owner, p.f.Workspace, job.ID)
				if e != nil || out.State != "unknown" || out.Result != nil {
					t.Fatal("expired task replayed or projected", e)
				}
				if n := p.count(t, "SELECT count(*) FROM audit_events WHERE action='middleware.operation_uncertain' AND target_id=$1", "SELECT count(*) FROM audit_events WHERE action='middleware.operation_uncertain' AND target_id=?", job.ID); n != 1 {
					t.Fatal("uncertainty audit repeated", n)
				}
			case "terminal_intent_history_immutable":
				job, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, health(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				_, e = p.f.Store.Exec(p.ctx, p.f.Statement("UPDATE middleware_operations SET input_json='{}' WHERE id=$1", "UPDATE middleware_operations SET input_json='{}' WHERE id=?", job.ID))
				if e == nil {
					t.Fatal("actual intent trigger did not reject update")
				}
				if e = p.s.terminal(p.ctx, &claimed{Operation: Operation{ID: job.ID, TenantID: p.f.Tenant, WorkspaceID: p.f.Workspace}, requestedBy: p.f.User, request: uuid.NewString()}, "failed", "MIDDLEWARE_SYNTHETIC_REJECTION", nil, false); e != nil {
					t.Fatal(e)
				}
				_, e = p.f.Store.Exec(p.ctx, p.f.Statement("DELETE FROM middleware_operations WHERE id=$1", "DELETE FROM middleware_operations WHERE id=?", job.ID))
				if e == nil {
					t.Fatal("actual history trigger did not reject delete")
				}
			case "bounded_history_and_original_actor_intent":
				var first Operation
				var input OperationInput
				for n := 0; n < 3; n++ {
					input = health()
					var e error
					first, e = p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, input, uuid.NewString())
					if e != nil {
						t.Fatal(e)
					}
				}
				page, e := p.s.History(p.ctx, p.owner, p.f.Workspace, v.ID, "")
				if e != nil || len(page.Items) != 2 || page.NextCursor == nil {
					t.Fatal("unbounded history", e)
				}
				out, e := p.s.OperationIntent(p.ctx, p.owner, p.f.Workspace, input.IdempotencyKey)
				if e != nil || out.ID != first.ID {
					t.Fatal("original job readback", e)
				}
				_, e = p.s.OperationIntent(p.ctx, p.viewer, p.f.Workspace, input.IdempotencyKey)
				wantCode(t, e, "RESOURCE_NOT_FOUND")
			case "reopened_actual_selected_integrity":
				reopened, e := New(p.accounts, &workspaces.Service{Identity: p.accounts}, p.configuration)
				if e != nil {
					t.Fatal(e)
				}
				if e = reopened.VerifyStored(p.ctx); e != nil {
					t.Fatal("actual restarted selected owner", e)
				}
				out, e := reopened.Get(p.ctx, p.owner, p.f.Workspace, v.ID)
				if e != nil || out.ID != v.ID {
					t.Fatal(e)
				}
				raw, _ := json.Marshal(out)
				if bytes.Contains(raw, []byte(v.Fingerprint)) || strings.Contains(string(raw), "password") {
					t.Fatal("private inputs projected")
				}
			case "borrowed_restore_atomic_fence":
				queued, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, health(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				running, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, v.ID, health(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				p.exec(t, "UPDATE middleware_operations SET state='running',attempt_id=$1,lease_expires_at=clock_timestamp()+interval '1 minute' WHERE id=$2", "UPDATE middleware_operations SET state='running',attempt_id=?,lease_expires_at=TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6)) WHERE id=?", uuid.NewString(), running.ID)
				rollback := errors.New("EXPLICIT_SYNTHETIC_ROLLBACK")
				e = p.f.Store.Within(p.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					if e := InvalidateAuthoritiesWithin(ctx, q, "Synthetic original restore reason"); e != nil {
						return e
					}
					return rollback
				})
				if e == nil {
					t.Fatal("rollback unexpectedly committed")
				}
				old, e := p.s.Operation(p.ctx, p.owner, p.f.Workspace, queued.ID)
				if e != nil || old.State != "queued" {
					t.Fatal("rollback lost queued authority", e)
				}
				for range 2 {
					if e = p.f.Store.Within(p.ctx, false, func(ctx context.Context, q transaction.Executor) error {
						return InvalidateAuthoritiesWithin(ctx, q, "Synthetic original restore reason")
					}); e != nil {
						t.Fatal(e)
					}
				}
				for _, check := range []struct{ id, state string }{{queued.ID, "failed"}, {running.ID, "unknown"}} {
					out, e := p.s.Operation(p.ctx, p.owner, p.f.Workspace, check.id)
					if e != nil || out.State != check.state || out.Result != nil {
						t.Fatal("restored authority survived or projected", e)
					}
				}
				if n := p.count(t, "SELECT count(*) FROM audit_events WHERE action='middleware.restore_invalidated'", "SELECT count(*) FROM audit_events WHERE action='middleware.restore_invalidated'"); n != 1 {
					t.Fatal("restore audit rollback/repeat failed", n)
				}
			}
		})
	}
}
