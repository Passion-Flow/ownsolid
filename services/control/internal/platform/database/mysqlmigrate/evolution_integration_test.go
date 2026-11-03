//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

// The old prefix is an identified test construction,not an operator schema bypass.
func evolutionBase(catalog Catalog) Catalog {
	out := catalog
	out.steps = catalog.steps[:15]
	return out
}
func evolutionActual(t *testing.T) (*mysqlstore.Store, Catalog) {
	t.Helper()
	store, catalog, _ := actualMigration(t)
	migrationRun(t, store, evolutionBase(catalog), Apply)
	return store, catalog
}
func evolutionBusinessDigest(t *testing.T, store *mysqlstore.Store) string {
	t.Helper()
	values := []any{}
	for _, query := range []string{
		"SELECT JSON_ARRAY(id,bootstrap_complete,tenant_id,default_workspace_id,primary_admin_user_id,bootstrapped_at) FROM installation_state ORDER BY id",
		"SELECT JSON_ARRAY(id,name,status,created_at) FROM tenants ORDER BY id",
		"SELECT JSON_ARRAY(id,tenant_id,name,status,version,created_at,updated_at) FROM workspaces ORDER BY id",
		"SELECT JSON_ARRAY(id,email_canonical,email_display,display_name,locale,status,is_platform_admin,email_verified_at,auth_epoch,version,created_at,updated_at) FROM users ORDER BY id",
		"SELECT JSON_ARRAY(user_id,password_phc,version,changed_at) FROM password_credentials ORDER BY user_id",
		"SELECT JSON_ARRAY(user_id,tenant_id,workspace_id,role,version,created_at,updated_at) FROM workspace_memberships ORDER BY user_id,workspace_id",
		"SELECT JSON_ARRAY(id,tenant_id,workspace_id,email_canonical,locale,role,status,HEX(token_hash),token_generation,version,created_by,accepted_user_id,created_at,updated_at,expires_at,accepted_at,revoked_at) FROM invitations ORDER BY id",
		"SELECT JSON_ARRAY(id,purpose,recipient_email,locale,invitation_id,invitation_generation,reset_token_id,user_id,state,payload_key_id,HEX(payload_nonce),HEX(payload_ciphertext),payload_schema_version,attempt_count,next_attempt_at,lease_token,locked_until,error_category,message_id,created_at,updated_at,completed_at) FROM mail_deliveries ORDER BY id",
		"SELECT JSON_ARRAY(id,occurred_at,actor_user_id,subject_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id,HEX(identifier_digest),reason,metadata) FROM audit_events ORDER BY id",
	} {
		rows, e := store.Pool().QueryContext(context.Background(), query)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		for rows.Next() {
			var raw []byte
			var value any
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				t.Fatal("actual canonical business row scan")
			}
			if json.Unmarshal(raw, &value) != nil {
				rows.Close()
				t.Fatal("actual canonical JSON row")
			}
			values = append(values, value)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
	}
	raw, e := json.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	return hash(string(raw))
}
func seedEvolutionBusiness(t *testing.T, store *mysqlstore.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	directory := t.TempDir()
	blocklist := filepath.Join(directory, "synthetic-blocklist")
	if e := os.WriteFile(blocklist, []byte("passwordpasswordpassword\n"), 0600); e != nil {
		t.Fatal(e)
	}
	passwords, e := security.NewPasswords(blocklist)
	if e != nil {
		t.Fatal(e)
	}
	repository, e := transaction.FromMySQL(store)
	if e != nil {
		t.Fatal(e)
	}
	owner := &identity.Service{Storage: repository, Passwords: passwords}
	if e = owner.Bootstrap(ctx, "operator@example.invalid", "Migration operator \u4e16\u754c", "Synthetic migration phrase "+uuid.NewString(), "Migration tenant \u4e16\u754c", "Migration Workspace \u4e16\u754c"); e != nil {
		t.Fatal("actual prerequisite bootstrap", e)
	}
	var tenant, workspace, user string
	if e = store.Pool().QueryRowContext(ctx, "SELECT tenant_id,default_workspace_id,primary_admin_user_id FROM installation_state WHERE id=1").Scan(&tenant, &workspace, &user); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	invitation := uuid.NewString()
	token := "Synthetic invitation " + uuid.NewString()
	digest := sha256.Sum256([]byte(token))
	migrationExec(t, store, `INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES(?,?,?,'synthetic@example.invalid','en','pending',?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),TIMESTAMPADD(HOUR,1,UTC_TIMESTAMP(6)))`, invitation, tenant, workspace, digest[:], user)
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal("synthetic encryption key")
	}
	keyring := filepath.Join(directory, "synthetic-keyring.json")
	raw, _ := json.Marshal(mail.Keyring{Active: "synthetic", Keys: map[string]string{"synthetic": base64.StdEncoding.EncodeToString(key)}})
	if e = os.WriteFile(keyring, raw, 0600); e != nil {
		t.Fatal(e)
	}
	ring, e := mail.LoadKeyring(keyring)
	if e != nil {
		t.Fatal(e)
	}
	outbox := &mail.Outbox{Storage: repository, Keys: ring}
	outbox.Config.Server.PortalOrigin = "https://portal.example.invalid"
	outbox.Config.SMTP.FromAddress = "platform@example.invalid"
	if e = repository.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		_, e := outbox.EnqueueWithin(ctx, tx, mail.Input{Purpose: "invitation", Recipient: "synthetic@example.invalid", Locale: "en", InvitationID: invitation, Generation: 1, Token: token, Audience: "portal"})
		return e
	}); e != nil {
		t.Fatal("actual prerequisite encrypted outbox", e)
	}
}

func TestActualMySQLEvolutionUpgradeAndIntegrity(t *testing.T) {
	store, catalog := evolutionActual(t)
	seedEvolutionBusiness(t, store)
	before := evolutionBusinessDigest(t, store)
	if report, e := Run(context.Background(), store, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeSchemaNotReady || report.AppliedSteps != 15 || report.DirtyStep != 0 {
		t.Fatal("old valid prefix incorrectly required future definitions", report, e)
	}
	migrationRun(t, store, evolutionBase(catalog), Readiness)
	migrationRun(t, store, catalog, Apply)
	migrationRun(t, store, catalog, Readiness)
	if after := evolutionBusinessDigest(t, store); after != before {
		t.Fatal("actual evolution changed business values")
	}
	t.Log("MYSQL_EVOLUTION_BUSINESS_SHA256=" + before)
	t.Run("current_locale_shapes_preserve_existing_data", func(t *testing.T) {
		tx, e := store.Pool().BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		for _, table := range []string{"users", "invitations", "mail_deliveries"} {
			for _, locale := range []string{"en", "zh-CN", "zh-Hant-TW", "pt-BR", "sr-Latn-RS", "abc-abcdefgh-ABCDEFGH-12345678"} {
				if _, e = tx.ExecContext(context.Background(), "UPDATE `"+table+"` SET locale=?", locale); e != nil {
					t.Fatal("valid locale shape refused", mysqlstore.SafeError(e))
				}
				var actual string
				if e = tx.QueryRowContext(context.Background(), "SELECT locale FROM `"+table+"`").Scan(&actual); e != nil || actual != locale {
					t.Fatal("locale readback", mysqlstore.SafeError(e))
				}
			}
			for _, locale := range []string{"", "EN", "e", "en_XX", "en--US", " en", "en ", "\u4e2d\u6587", "en-aa-bb-cc-dd", strings.Repeat("a", 36)} {
				if _, e = tx.ExecContext(context.Background(), "UPDATE `"+table+"` SET locale=?", locale); e == nil {
					t.Fatal("invalid locale shape committed", table)
				}
			}
		}
	})
	t.Run("ASCII_UUID_and_lease_CHECKs_remain_case_sensitive", func(t *testing.T) {
		tx, e := store.Pool().BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		_, e = tx.ExecContext(context.Background(), `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES('AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA','uppercase@example.invalid','uppercase@example.invalid','Synthetic uppercase UUID','en','active',0)`)
		var typed *mysqlstore.Error
		if !errors.As(mysqlstore.SafeError(e), &typed) || typed.Number != 3819 {
			t.Fatal("uppercase UUID CHECK lost", mysqlstore.SafeError(e))
		}
		_, e = tx.ExecContext(context.Background(), `UPDATE mail_deliveries SET state='sending',lease_token='AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA',locked_until=TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6))`)
		if !errors.As(mysqlstore.SafeError(e), &typed) || typed.Number != 3819 {
			t.Fatal("uppercase lease CHECK lost", mysqlstore.SafeError(e))
		}
		if _, e = tx.ExecContext(context.Background(), `UPDATE mail_deliveries SET state='sending',lease_token='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',locked_until=TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6))`); e != nil {
			t.Fatal("valid lease semantics changed", mysqlstore.SafeError(e))
		}
	})
	if after := evolutionBusinessDigest(t, store); after != before {
		t.Fatal("negative-case transaction rollback changed business rows")
	}
}

func TestActualMySQLEvolutionFreshAndIdempotent(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	migrationRun(t, store, catalog, Apply)
	events := 0
	report, e := Run(context.Background(), store, catalog, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !report.Ready || events != 0 {
		t.Fatal("actual evolved fresh rerun changed state", report, events, e)
	}
}

func TestActualMySQLEvolutionBeforeDDLReconciliation(t *testing.T) {
	store, catalog := evolutionActual(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report, e := Run(ctx, store, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == 16 && event.Stage == Applying {
			cancel()
		}
	})
	if e == nil || report.DirtyStep != 16 || !report.LockReleased {
		t.Fatal("actual before-ALTER interruption not retained", report, e)
	}
	if _, e = Run(context.Background(), store, catalog, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty ALTER automatically replayed", e)
	}
	migrationRun(t, store, catalog, Reconcile)
	migrationRun(t, store, catalog, Readiness)
}

func TestActualMySQLEvolutionAfterDDLReconciliation(t *testing.T) {
	store, catalog := evolutionActual(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report, e := Run(ctx, store, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == 16 && event.Stage == Verified {
			cancel()
		}
	})
	if e == nil || report.DirtyStep != 16 || !report.LockReleased {
		t.Fatal("actual after-ALTER interruption not retained", report, e)
	}
	var old int
	if e = store.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM information_schema.table_constraints WHERE table_schema=DATABASE() AND table_name='users' AND constraint_name='users_chk_1'").Scan(&old); e != nil || old != 0 {
		t.Fatal("ALTER did not actually remove old CHECK")
	}
	// Replaying the original ALTER would now fail DROP CHECK users_chk_1.
	migrationRun(t, store, catalog, Reconcile)
	migrationRun(t, store, catalog, Readiness)
}

func TestActualMySQLEvolutionDirtyDriftRefusal(t *testing.T) {
	store, catalog := evolutionActual(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report, e := Run(ctx, store, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == 16 && event.Stage == Verified {
			cancel()
		}
	})
	if e == nil || report.DirtyStep != 16 || !report.LockReleased {
		t.Fatal("actual dirty target setup failed", report, e)
	}
	migrationExec(t, store, "ALTER TABLE users ADD COLUMN synthetic_drift INT NULL")
	if _, e := Run(context.Background(), store, catalog, testOptions(), Reconcile, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("third-state definition adopted", e)
	}
	var count int
	if e := store.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations WHERE step=16 AND state='applying'").Scan(&count); e != nil || count != 1 {
		t.Fatal("drift refusal altered journal")
	}
}

func TestActualMySQLEvolutionMissingTableRefusal(t *testing.T) {
	store, catalog := evolutionActual(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report, e := Run(ctx, store, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == 18 && event.Stage == Applying {
			cancel()
		}
	})
	if e == nil || report.DirtyStep != 18 || !report.LockReleased {
		t.Fatal("actual missing predecessor setup failed", report, e)
	}
	// This is an exclusively owned empty disposable test database,not user data.
	migrationExec(t, store, "DROP TABLE mail_deliveries")
	if _, e := Run(context.Background(), store, catalog, testOptions(), Reconcile, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("missing ALTER predecessor recreated", e)
	}
}

func TestActualMySQLEvolutionUnjournaledTargetRefusal(t *testing.T) {
	store, catalog := evolutionActual(t)
	migrationExec(t, store, catalog.steps[15].sql)
	if _, e := Run(context.Background(), store, catalog, testOptions(), Apply, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("unjournaled ALTER adopted", e)
	}
	var count int
	if e := store.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count); e != nil || count != 15 {
		t.Fatal("refused adoption still wrote a journal")
	}
}

func TestActualMySQLEvolutionReadback(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	migrationRun(t, store, catalog, Readiness)
	digest := evolutionBusinessDigest(t, store)
	var complete bool
	if e := store.Pool().QueryRowContext(context.Background(), "SELECT bootstrap_complete FROM installation_state WHERE id=1").Scan(&complete); e != nil || !complete {
		t.Fatal("restored installed business state missing", mysqlstore.SafeError(e))
	}
	t.Log("MYSQL_EVOLUTION_BUSINESS_SHA256=" + digest)
}
