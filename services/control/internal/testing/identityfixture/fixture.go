// Package identityfixture supplies isolated synthetic fixtures to tests only.
package identityfixture

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func New(t *testing.T) *identity.Service {
	return NewWithSetup(t, SetupOptions{})
}

// NewWithSetup selects the same synthetic catalog as New, with an explicit
// test-construction budget that never becomes the returned business budget.
func NewWithSetup(t *testing.T, setup SetupOptions) *identity.Service {
	t.Helper()
	return newSelectedWithSetup(t, setup, false)
}

// NewRecoverableWithSetup selects exactly the same full or explicitly selected
// catalog as NewWithSetup, retaining only an already-failed test's SQL schema.
// It does not replace the default full catalog with the M16 account catalog.
func NewRecoverableWithSetup(t *testing.T, setup SetupOptions) *identity.Service {
	t.Helper()
	return newSelectedWithSetup(t, setup, true)
}

func newSelectedWithSetup(t *testing.T, setup SetupOptions, retainFailed bool) *identity.Service {
	t.Helper()
	return newSchemaWithCustody(t, setup, retainFailed, 0, true)
}

// NewAtVersion creates an isolated schema migrated through the given lineage
// version, for verifying the next migration against real earlier data.
func NewAtVersion(t *testing.T, version int) *identity.Service {
	t.Helper()
	if version < 1 || version > database.SchemaVersion {
		t.Fatal("FIXTURE_SCHEMA_VERSION_INVALID")
	}
	return newSchema(t, version, true)
}

// NewAtVersionWithSetup is NewAtVersion with an explicit construction budget.
func NewAtVersionWithSetup(t *testing.T, setup SetupOptions, version int) *identity.Service {
	t.Helper()
	if version < 1 || version > database.SchemaVersion {
		t.Fatal("FIXTURE_SCHEMA_VERSION_INVALID")
	}
	return newSchemaWithSetup(t, setup, version, true)
}

// NewM01 creates an isolated schema holding only the first migration.
func NewM01(t *testing.T) *identity.Service {
	return newSchema(t, 1, false)
}

// NewUnlicensed supplies real public trust and an empty license history.
func NewUnlicensed(t *testing.T) *identity.Service { return newSchema(t, 0, false) }
// newSchema migrates through version, or the complete lineage when version is 0.
func newSchema(t *testing.T, version int, licensed bool) *identity.Service {
	t.Helper()
	return newSchemaWithSetup(t, SetupOptions{}, version, licensed)
}

func newSchemaWithSetup(t *testing.T, options SetupOptions, version int, licensed bool) *identity.Service {
	t.Helper()
	return newSchemaWithCustody(t, options, false, version, licensed)
}

func newSchemaWithCustody(t *testing.T, options SetupOptions, retainFailed bool, version int, licensed bool) *identity.Service {
	t.Helper()
	return newConfigured(t, options, retainFailed, version, licensed, nil)
}

// NewConfigured is New with configure applied to the returned business pool before its
// first connection. Only that pool is affected, so a test can observe what its pool does.
func NewConfigured(t *testing.T, configure func(*pgxpool.Config)) *identity.Service {
	t.Helper()
	return newConfigured(t, SetupOptions{}, false, 0, true, configure)
}

func newConfigured(t *testing.T, options SetupOptions, retainFailed bool, version int, licensed bool, configure func(*pgxpool.Config)) *identity.Service {
	t.Helper()
	setup, e := options.plan()
	if e != nil {
		t.Fatal(e)
	}
	dsn, e := databaseInput(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"), os.Getenv("PROJECT_ONE_TEST_DSN"))
	if e != nil {
		t.Fatal(e)
	}
	if dsn == "" {
		t.Skip("PROJECT_ONE_TEST_DSN is required for real PostgreSQL integration")
	}
	pc, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal("ISOLATED_TEST_DATABASE_CONNECTION_INVALID")
	}
	if !strings.HasSuffix(pc.ConnConfig.Database, "_test") || pc.ConnConfig.Host != "127.0.0.1" {
		t.Fatal("ISOLATED_LOOPBACK_TEST_DATABASE_REQUIRED")
	}
	pc.MaxConns = 8
	pc.ConnConfig.RuntimeParams["statement_timeout"] = fixtureStatementTimeoutMilliseconds
	pc.ConnConfig.RuntimeParams["lock_timeout"] = fixtureLockTimeoutMilliseconds
	pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
	operator, e := pgxpool.NewWithConfig(context.Background(), pc)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(operator.Close)
	schema := "m01_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if file := os.Getenv("PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT"); file != "" {
		schema = nativePostgresScope(t, operator, file, false).Schema
	}
	t.Logf("PROJECT_ONE_OWNED_SCHEMA=%s", schema)
	setupContext, cancelSetup := setup.context()
	defer cancelSetup()
	creator := operator
	var setupPool *pgxpool.Pool
	closeSetup := func() {
		if setupPool != nil {
			setupPool.Close()
			setupPool = nil
		}
	}
	defer closeSetup()
	if setup.explicit {
		setupPool, e = pgxpool.NewWithConfig(setupContext, setup.poolConfiguration(pc, schema))
		if e != nil {
			t.Fatal(e)
		}
		creator = setupPool
	}
	if _, e = creator.Exec(setupContext, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	if configure != nil {
		configure(pc)
	}
	db, e := pgxpool.NewWithConfig(context.Background(), pc)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if retainFailed && t.Failed() {
			// No disposal SQL is allowed after an opted-in failure. Preserve the
			// original metadata, including any uncertain operation or audit rows.
			db.Close()
			operator.Close()
			if db.Stat().TotalConns() != 0 || operator.Stat().TotalConns() != 0 {
				t.Error("OWNED_IDENTITY_FIXTURE_POOL_CLOSE_UNCONFIRMED")
				return
			}
			t.Logf("IDENTITY_FIXTURE_FAILURE_PRESERVED_SCHEMA=%s setup_and_runtime_pools_closed=true", schema)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), fixtureMaintenanceCleanupSeconds*time.Second)
		defer cancel()
		count, disposeError := disposeOwnedPostgresTables(ctx, db, schema)
		db.Close()
		if disposeError != nil {
			t.Error("OWNED_IDENTITY_FIXTURE_TABLE_CLEANUP_FAILED", disposeError)
			return
		}
		if _, e := operator.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("OWNED_IDENTITY_FIXTURE_SCHEMA_CLEANUP_FAILED")
			return
		}
		t.Logf("IDENTITY_FIXTURE_SOURCE_DISPOSAL autocommit_tables=%d exact_owned_schema=true", count)
	})
	if file := os.Getenv("PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT"); file != "" {
		_ = OwnedNativePostgres(t, db, file)
	}
	migrationPool := db
	if setupPool != nil {
		migrationPool = setupPool
	}
	if version > 0 {
		migrateThrough(t, setupContext, migrationPool, version)
	} else if e = database.Migrate(setupContext, migrationPool); e != nil {
		t.Fatal(e)
	}
	// Close waits for every setup connection before any business owner is built.
	// The returned pool was independently configured with the original 5s budget.
	closeSetup()
	cancelSetup()
	c := config.Config{}
	c.Server.PortalOrigin = "https://portal.example.invalid"
	c.Server.AdminOrigin = "https://admin.example.invalid"
	c.SMTP.FromAddress = "platform@example.invalid"
	dir := t.TempDir()
	configureLicense(t, &c, dir, licensed)
	blocklist := filepath.Join(dir, "blocklist")
	os.WriteFile(blocklist, []byte("passwordpassword\n"), 0600)
	pw, e := security.NewPasswords(blocklist)
	if e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	rand.Read(key)
	ring := filepath.Join(dir, "keyring.json")
	b, _ := json.Marshal(map[string]any{"active": "test", "keys": map[string]string{"test": base64.StdEncoding.EncodeToString(key)}})
	os.WriteFile(ring, b, 0600)
	keys, e := mail.LoadKeyring(ring)
	if e != nil {
		t.Fatal(e)
	}
	templates, e := mail.LoadTemplates()
	if e != nil {
		t.Fatal(e)
	}
	outbox := &mail.Outbox{DB: db, Keys: keys, Config: c, Templates: templates}
	s := &identity.Service{DB: db, Config: c, Passwords: pw, Mail: outbox, HMACKey: key}
	license, e := licensing.FromConfig(s)
	if e != nil {
		t.Fatal(e)
	}
	s.BusinessAdmission = license
	return s
}
func Password() string { return "Synthetic passphrase " + uuid.NewString() }
func Bootstrap(t *testing.T, s *identity.Service) string {
	t.Helper()
	p := Password()
	if e := s.Bootstrap(context.Background(), "admin@example.invalid", "Test Admin", p, "Test installation", "Default"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(s.Config.Licensing.TrustFile + ".auto"); e == nil {
		SeedLicense(t, s)
	} else if !os.IsNotExist(e) {
		t.Fatal(e)
	}
	return p
}
func Login(t *testing.T, s *identity.Service, email, p, audience string) identity.Auth {
	t.Helper()
	_, token, _, e := s.Login(context.Background(), email, p, audience, "", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Authenticate(context.Background(), token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func InvitationToken(t *testing.T, s *identity.Service, id string) string {
	t.Helper()
	var key, delivery string
	var nonce, b []byte
	if e := s.DB.QueryRow(context.Background(), "SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1 AND state='queued' ORDER BY created_at DESC LIMIT 1", id).Scan(&delivery, &key, &nonce, &b); e != nil {
		t.Fatal(e)
	}
	raw, e := s.Mail.Keys.Decrypt(key, delivery, "invitation", nonce, b)
	if e != nil {
		t.Fatal(e)
	}
	var p mail.Payload
	if e = json.Unmarshal(raw, &p); e != nil {
		t.Fatal(e)
	}
	return strings.Split(p.Link, "#token=")[1]
}
func ResetToken(t *testing.T, s *identity.Service, email string) string {
	t.Helper()
	var key, delivery string
	var nonce, b []byte
	if e := s.DB.QueryRow(context.Background(), "SELECT id::text,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE recipient_email=$1 AND purpose='password_reset' AND state='queued' ORDER BY created_at DESC LIMIT 1", email).Scan(&delivery, &key, &nonce, &b); e != nil {
		t.Fatal(e)
	}
	raw, e := s.Mail.Keys.Decrypt(key, delivery, "password_reset", nonce, b)
	if e != nil {
		t.Fatal(e)
	}
	var p mail.Payload
	json.Unmarshal(raw, &p)
	return strings.Split(p.Link, "#token=")[1]
}

// migrateThrough applies the first version migrations of the lineage with
// their ledger rows, exactly as Migrate would have recorded them.
func migrateThrough(t *testing.T, ctx context.Context, pool *pgxpool.Pool, version int) {
	t.Helper()
	if _, e := pool.Exec(ctx, "CREATE TABLE schema_migrations(version integer PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP)"); e != nil {
		t.Fatal(e)
	}
	entries, e := migrations.Files.ReadDir("postgresql")
	if e != nil {
		t.Fatal(e)
	}
	applied := 0
	for _, entry := range entries {
		if applied == version {
			break
		}
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		b, e := migrations.Files.ReadFile("postgresql/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		applied++
		h := sha256.Sum256(b)
		if _, e = pool.Exec(ctx, string(b)); e != nil {
			t.Fatal(entry.Name(), e)
		}
		if _, e = pool.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", applied, hex.EncodeToString(h[:])); e != nil {
			t.Fatal(e)
		}
	}
	if applied != version {
		t.Fatal("FIXTURE_SCHEMA_VERSION_INVALID")
	}
}
