package samlprofile

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed storage.sql
var identitySchema string

type identityFixture struct {
	ctx                      context.Context
	pool                     *pgxpool.Pool
	store                    identityStore
	crypto                   *fixture
	tenant, connector, scope string
}

func rollbackIdentity(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func newIdentityFixture(t *testing.T, longContext bool) *identityFixture {
	t.Helper()
	path := os.Getenv("IAM_SAML_STORAGE_DSN_FILE")
	if path == "" {
		t.Skip("explicit private SAML PostgreSQL fixture required")
	}
	raw, err := readPrivateFile(path, 16384)
	if err != nil {
		t.Fatal("private SAML database reference refused")
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	clear(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port < 1024 || cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("owned loopback test database required")
	}
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.MaxConns = 6
	cfg.MaxConnIdleTime = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["application_name"] = "iam104-private-identity-storage"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		cancel()
		t.Fatal("private SAML database pool unavailable")
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		admin.Close()
		cancel()
		t.Fatal("owned schema identifier unavailable")
	}
	schema := "iam104_" + hex.EncodeToString(nonce[:])
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		cancel()
		t.Fatal("owned SAML schema unavailable")
	}
	admin.Close()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		cancel()
		t.Fatal("owned SAML scoped pool unavailable")
	}
	f := &identityFixture{ctx: ctx, pool: pool}
	t.Cleanup(func() {
		pool.Close()
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		cleanConfig := cfg.Copy()
		cleanConfig.ConnConfig.RuntimeParams["search_path"] = "public"
		clean, err := pgxpool.NewWithConfig(cleanup, cleanConfig)
		if err == nil {
			_, err = clean.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`)
			clean.Close()
		}
		if err != nil {
			t.Error("owned SAML schema cleanup failed")
		}
		cancel()
	})
	if _, err = pool.Exec(ctx, identitySchema); err != nil {
		t.Fatal("SAML identity storage schema refused")
	}
	f.crypto = newFixture(t)
	if longContext {
		f.crypto.cfg.Issuer = "urn:" + strings.Repeat("\U0001f680", 1020)
		f.crypto.cfg.SP = "urn:" + strings.Repeat("\U0001f681", 1020)
		f.crypto.cfg.Metadata = f.crypto.metadata(t, f.crypto.cert)
	}
	if pool.QueryRow(ctx, `SELECT gen_random_uuid()::text,gen_random_uuid()::text`).Scan(&f.tenant, &f.connector) != nil {
		t.Fatal("owned SAML scope identifiers unavailable")
	}
	f.store = identityStore{tenant: f.tenant}
	if pool.QueryRow(ctx, `INSERT INTO saml_identity_contexts(tenant_id,connector_id,issuer,sp_entity,name_id_format,metadata_digest) VALUES($1,$2,$3::text,$4::text,$5,$6) RETURNING id::text`, f.tenant, f.connector, f.crypto.cfg.Issuer, f.crypto.cfg.SP, "urn:oasis:names:tc:SAML:2.0:nameid-format:persistent", metadataDigest(f.crypto.cfg)).Scan(&f.scope) != nil {
		t.Fatal("owned SAML identity context unavailable")
	}
	return f
}

func (f *identityFixture) proof(t *testing.T, value string, omitQualifiers bool) identityProof {
	t.Helper()
	a := f.crypto.assertion()
	name := a.FindElement("./Subject/NameID")
	if name == nil {
		t.Fatal("signed fixture NameID missing")
	}
	name.SetText(value)
	if omitQualifiers {
		name.RemoveAttr("NameQualifier")
		name.RemoveAttr("SPNameQualifier")
	}
	sourceTx := f.begin(t)
	authority, err := f.store.loadSource(f.ctx, sourceTx, f.connector)
	if err != nil {
		rollbackIdentity(sourceTx)
		t.Fatal("current SAML verification authority unavailable")
	}
	if sourceTx.Commit(f.ctx) != nil {
		t.Fatal("current SAML authority snapshot failed")
	}
	v, err := newIdentityVerifier(authority, f.crypto.cfg)
	if err != nil {
		t.Fatal("approved metadata does not match current authority")
	}
	p, err := v.verify(encoded(t, f.crypto.response(f.crypto.sign(t, a, rsaSHA256))), "_request_private")
	if err != nil {
		t.Fatal("actual signed NameID proof refused")
	}
	return p
}
func (f *identityFixture) begin(t *testing.T) pgx.Tx {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal("owned identity transaction unavailable")
	}
	return tx
}
func (f *identityFixture) accept(t *testing.T, p identityProof) storedIdentity {
	t.Helper()
	tx := f.begin(t)
	defer rollbackIdentity(tx)
	result, err := f.store.resolve(f.ctx, tx, f.connector, p)
	if err != nil {
		t.Fatal("verified identity resolution failed")
	}
	if tx.Commit(f.ctx) != nil {
		t.Fatal("verified identity commit failed")
	}
	return result
}
func (f *identityFixture) scalar(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if f.pool.QueryRow(f.ctx, sql, args...).Scan(&n) != nil {
		t.Fatal("owned identity readback failed")
	}
	return n
}
func (f *identityFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		t.Fatal("owned identity fixture mutation failed")
	}
}
func (f *identityFixture) refuseSQL(t *testing.T, code, sql string, args ...any) {
	t.Helper()
	_, err := f.pool.Exec(f.ctx, sql, args...)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != code {
		t.Fatal("database did not enforce exact identity constraint")
	}
}

func metadataDigest(c configuration) []byte { value := sha256.Sum256(c.Metadata); return value[:] }
