package samlprofile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSAMLIdentityExactMaximumAndNormalization(t *testing.T) {
	f := newIdentityFixture(t, true)
	maximum := strings.Repeat("\U0001f680", 256)
	proof := f.proof(t, maximum, false)
	first := f.accept(t, proof)
	if first.ID == "" || !first.Created || first.Subject != proof.Subject || first.ContextID != f.scope {
		t.Fatal("maximum signed identity did not round-trip exactly")
	}
	implied := f.accept(t, f.proof(t, maximum, true))
	if implied.Created || implied.ID != first.ID || implied.Subject != first.Subject {
		t.Fatal("effective qualifier equivalence changed persistent identity")
	}
	if f.scalar(t, `SELECT pg_column_size(name_id_value) FROM saml_persistent_identities WHERE tenant_id=$1 AND id=$2`, f.tenant, first.ID) != 1028 {
		t.Fatal("maximum NameID was not stored inline without compression")
	}
	ids := map[string]bool{first.ID: true}
	for _, item := range []struct{ name, value string }{{"upper", "Opaque:Case"}, {"lower", "opaque:Case"}, {"precomposed", "\u00e9"}, {"decomposed", "e\u0301"}, {"trailing_space", "Opaque:Case "}, {"maximum_trailing_space", strings.Repeat("a", 255) + " "}} {
		t.Run(item.name, func(t *testing.T) {
			p := f.proof(t, item.value, false)
			stored := f.accept(t, p)
			if stored.Subject != p.Subject || !stored.Created || ids[stored.ID] {
				t.Fatal("case, space or normalization changed exact identity")
			}
			ids[stored.ID] = true
			repeated := f.accept(t, f.proof(t, item.value, true))
			if repeated.ID != stored.ID || repeated.Created {
				t.Fatal("repeated exact identity changed stable row")
			}
		})
	}
	if f.scalar(t, `SELECT count(*) FROM saml_persistent_identities`) != len(ids) {
		t.Fatal("distinct exact identities were merged or duplicated")
	}
}

func TestSAMLIdentityDatabaseConstraintsAndScope(t *testing.T) {
	f := newIdentityFixture(t, false)
	p := f.proof(t, "Opaque:Scope", false)
	stored := f.accept(t, p)
	for _, item := range []struct{ name, value string }{{"empty", ""}, {"characters", strings.Repeat("a", 257)}, {"four_byte_characters", strings.Repeat("\U0001f680", 257)}, {"trailing_overlength_space", strings.Repeat("a", 256) + " "}} {
		t.Run(item.name, func(t *testing.T) {
			f.refuseSQL(t, "23514", `INSERT INTO saml_persistent_identities(tenant_id,context_id,name_id_value) VALUES($1,$2,$3::text)`, f.tenant, f.scope, item.value)
		})
	}
	for _, field := range []string{"issuer", "sp_entity"} {
		t.Run("long_"+field, func(t *testing.T) {
			issuer, sp := f.crypto.cfg.Issuer, f.crypto.cfg.SP
			if field == "issuer" {
				issuer = "urn:" + strings.Repeat("a", 1021)
			} else {
				sp = "urn:" + strings.Repeat("a", 1021)
			}
			f.refuseSQL(t, "23514", `INSERT INTO saml_identity_contexts(tenant_id,connector_id,issuer,sp_entity,name_id_format,metadata_digest) VALUES($1,gen_random_uuid(),$2::text,$3::text,$4,$5)`, f.tenant, issuer, sp, p.Subject.Format, metadataDigest(f.crypto.cfg))
		})
	}
	for _, field := range []string{"issuer", "sp_entity", "name_id_format"} {
		t.Run("immutable_"+field, func(t *testing.T) {
			f.refuseSQL(t, "23514", `UPDATE saml_identity_contexts SET `+field+`='urn:changed' WHERE tenant_id=$1 AND connector_id=$2`, f.tenant, f.connector)
		})
	}
	f.refuseSQL(t, "23514", `UPDATE saml_identity_contexts SET enabled=false WHERE tenant_id=$1 AND connector_id=$2`, f.tenant, f.connector)
	for _, field := range []string{"tenant_id", "connector_id", "id"} {
		t.Run("immutable_"+field, func(t *testing.T) {
			f.refuseSQL(t, "23514", `UPDATE saml_identity_contexts SET `+field+`=gen_random_uuid() WHERE tenant_id=$1 AND connector_id=$2`, f.tenant, f.connector)
		})
	}
	f.refuseSQL(t, "23514", `UPDATE saml_persistent_identities SET name_id_value='changed' WHERE tenant_id=$1 AND id=$2`, f.tenant, stored.ID)
	var foreignTenant, foreignConnector, foreignScope string
	if f.pool.QueryRow(f.ctx, `SELECT gen_random_uuid()::text,gen_random_uuid()::text`).Scan(&foreignTenant, &foreignConnector) != nil {
		t.Fatal("foreign fixture identifiers unavailable")
	}
	if f.pool.QueryRow(f.ctx, `INSERT INTO saml_identity_contexts(tenant_id,connector_id,issuer,sp_entity,name_id_format,metadata_digest) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text`, foreignTenant, foreignConnector, p.Subject.Issuer, p.Subject.SPNameQualifier, p.Subject.Format, metadataDigest(f.crypto.cfg)).Scan(&foreignScope) != nil {
		t.Fatal("foreign context fixture unavailable")
	}
	f.refuseSQL(t, "23503", `INSERT INTO saml_persistent_identities(tenant_id,context_id,name_id_value) VALUES($1,$2,$3)`, f.tenant, foreignScope, p.Subject.Value)
	tx := f.begin(t)
	defer rollbackIdentity(tx)
	result, err := f.store.resolve(f.ctx, tx, foreignConnector, p)
	if !errors.Is(err, identityScopeRefused) || result.ID != "" {
		t.Fatal("foreign tenant connector resolved identity")
	}
	rollbackIdentity(tx)
	other := newIdentityFixture(t, false)
	foreignProof := other.proof(t, p.Subject.Value, false)
	// The foreign fixture has a different server-selected context, even when the
	// same upstream value appears in both tenants. No row ID crosses the boundary.
	tx = f.begin(t)
	crossed, crossErr := f.store.resolve(f.ctx, tx, f.connector, foreignProof)
	rollbackIdentity(tx)
	if !errors.Is(crossErr, identityScopeRefused) || crossed.ID != "" {
		t.Fatal("valid foreign-tenant proof with shared entity names was accepted")
	}
	aTx := f.begin(t)
	authority, aErr := f.store.loadSource(f.ctx, aTx, f.connector)
	rollbackIdentity(aTx)
	if aErr != nil {
		t.Fatal("own verifier snapshot unavailable")
	}
	if _, aErr = newIdentityVerifier(authority, other.crypto.cfg); !errors.Is(aErr, identityScopeRefused) {
		t.Fatal("foreign signing metadata with shared entity names was admitted")
	}
	f.refuseSQL(t, "23514", `UPDATE saml_identity_contexts SET metadata_digest=$3 WHERE tenant_id=$1 AND connector_id=$2`, f.tenant, f.connector, metadataDigest(other.crypto.cfg))
	foreign := other.accept(t, foreignProof)
	if foreign.ID == stored.ID || f.scalar(t, `SELECT count(*) FROM saml_persistent_identities WHERE tenant_id=$1 AND id=$2`, f.tenant, foreign.ID) != 0 {
		t.Fatal("foreign identity row crossed tenant scope")
	}
}

func TestSAMLIdentityAuthorityRollbackAndProofScope(t *testing.T) {
	f := newIdentityFixture(t, false)
	p := f.proof(t, "Opaque:Rollback", false)
	tx := f.begin(t)
	pending, err := f.store.resolve(f.ctx, tx, f.connector, p)
	if err != nil || pending.ID == "" {
		rollbackIdentity(tx)
		t.Fatal("borrowed identity transaction failed")
	}
	rollbackIdentity(tx)
	if f.scalar(t, `SELECT count(*) FROM saml_persistent_identities`) != 0 {
		t.Fatal("identity resolution committed outside its caller transaction")
	}
	f.exec(t, `UPDATE saml_identity_contexts SET enabled=false,revision=revision+1 WHERE tenant_id=$1 AND connector_id=$2`, f.tenant, f.connector)
	tx = f.begin(t)
	result, err := f.store.resolve(f.ctx, tx, f.connector, p)
	rollbackIdentity(tx)
	if !errors.Is(err, identityScopeRefused) || result.ID != "" {
		t.Fatal("disabled context resolved external identity")
	}
	f.exec(t, `UPDATE saml_identity_contexts SET enabled=true,revision=revision+1 WHERE tenant_id=$1 AND connector_id=$2`, f.tenant, f.connector)
	tx = f.begin(t)
	stale, staleErr := f.store.resolve(f.ctx, tx, f.connector, p)
	rollbackIdentity(tx)
	if !errors.Is(staleErr, identityScopeRefused) || stale.ID != "" {
		t.Fatal("repaired authority revived an old proof")
	}
	p = f.proof(t, "Opaque:Rollback", false)
	accepted := f.accept(t, p)
	f.refuseSQL(t, "23514", `UPDATE saml_identity_contexts SET revision=revision-1 WHERE tenant_id=$1 AND connector_id=$2`, f.tenant, f.connector)
	if accepted.Revision != 3 || accepted.ID == pending.ID {
		t.Fatal("explicit repair did not use current context and rollback outcome")
	}
	for _, name := range []string{"issuer", "sp", "qualifier", "format", "expired"} {
		t.Run(name, func(t *testing.T) {
			candidate := p
			switch name {
			case "issuer":
				candidate.Subject.Issuer = "urn:foreign"
			case "sp":
				candidate.Subject.SPNameQualifier = "urn:foreign"
			case "qualifier":
				candidate.Subject.NameQualifier = "urn:foreign"
			case "format":
				candidate.Subject.Format = "urn:foreign"
			case "expired":
				candidate.ExpiresAt = time.Now().Add(-time.Second)
			}
			tx := f.begin(t)
			result, err := f.store.resolve(f.ctx, tx, f.connector, candidate)
			rollbackIdentity(tx)
			if !errors.Is(err, identityScopeRefused) || result.ID != "" {
				t.Fatal("foreign or expired proof resolved current identity")
			}
		})
	}
	if f.scalar(t, `SELECT count(*) FROM saml_persistent_identities`) != 1 {
		t.Fatal("proof refusals changed retained identity")
	}
}

type identityResult struct {
	identity storedIdentity
	err      error
}

func (f *identityFixture) waitIdentityBlock(t *testing.T, pid, minimum int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.scalar(t, `SELECT count(*) FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))`, pid) >= minimum {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("actual identity uniqueness wait not observed")
}
func TestSAMLIdentityConcurrentResolutionAndCancellation(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			f := newIdentityFixture(t, false)
			p := f.proof(t, "Opaque:Concurrent", false)
			owner := f.begin(t)
			defer rollbackIdentity(owner)
			var pid int
			if owner.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid) != nil {
				t.Fatal("owned uniqueness backend unavailable")
			}
			initial, err := f.store.resolve(f.ctx, owner, f.connector, p)
			if err != nil {
				t.Fatal("owned uncommitted identity insert failed")
			}
			ctx, cancel := context.WithCancel(f.ctx)
			defer cancel()
			outcome := make(chan identityResult, 1)
			go func() {
				tx, err := f.pool.Begin(ctx)
				if err != nil {
					outcome <- identityResult{err: identityStorageFailed}
					return
				}
				defer rollbackIdentity(tx)
				id, err := f.store.resolve(ctx, tx, f.connector, p)
				if err == nil {
					err = tx.Commit(ctx)
				}
				outcome <- identityResult{identity: id, err: err}
			}()
			f.waitIdentityBlock(t, pid, 1)
			if mode == "commit" {
				if owner.Commit(f.ctx) != nil {
					t.Fatal("owned uniqueness commit failed")
				}
			} else if mode == "rollback" {
				rollbackIdentity(owner)
			} else {
				cancel()
			}
			var result identityResult
			select {
			case result = <-outcome:
			case <-time.After(5 * time.Second):
				t.Fatal("bounded identity resolution did not return")
			}
			if mode == "cancellation" {
				if result.err == nil || result.identity.ID != "" {
					t.Fatal("canceled identity wait returned an identity")
				}
				deadline := time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) {
					if f.scalar(t, `SELECT count(*) FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))`, pid) == 0 {
						break
					}
					time.Sleep(5 * time.Millisecond)
				}
				if f.scalar(t, `SELECT count(*) FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))`, pid) != 0 {
					t.Fatal("canceled identity SQL worker retained its wait")
				}
				rollbackIdentity(owner)
				if f.scalar(t, `SELECT count(*) FROM saml_persistent_identities`) != 0 {
					t.Fatal("canceled/rolled-back resolution persisted identity")
				}
				fresh := f.accept(t, p)
				if !fresh.Created {
					t.Fatal("explicit identity resolution after cancellation failed")
				}
			} else {
				if result.err != nil || result.identity.ID == "" || result.identity.Subject != p.Subject {
					t.Fatal("waiting exact identity resolution failed")
				}
				if mode == "commit" && (result.identity.ID != initial.ID || result.identity.Created) {
					t.Fatal("concurrent resolution duplicated committed identity")
				}
				if mode == "rollback" && (result.identity.ID == initial.ID || !result.identity.Created) {
					t.Fatal("rolled-back identity was revived")
				}
				if f.scalar(t, `SELECT count(*) FROM saml_persistent_identities`) != 1 {
					t.Fatal("concurrent resolution retained multiple identities")
				}
			}
		})
	}
}

func TestSAMLIdentityMaximumIndexLookup(t *testing.T) {
	f := newIdentityFixture(t, true)
	p := f.proof(t, strings.Repeat("\U0001f680", 256), false)
	target := f.accept(t, p)
	f.exec(t, `INSERT INTO saml_persistent_identities(tenant_id,context_id,name_id_value) SELECT $1,$2,'fixture-'||g::text FROM generate_series(1,2000) g`, f.tenant, f.scope)
	f.exec(t, `ANALYZE saml_persistent_identities`)
	var raw []byte
	if f.pool.QueryRow(f.ctx, `EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) SELECT id FROM saml_persistent_identities WHERE tenant_id=$1 AND context_id=$2 AND name_id_value=$3::text`, f.tenant, f.scope, p.Subject.Value).Scan(&raw) != nil {
		t.Fatal("real maximum-key index plan unavailable")
	}
	var plan []struct{ Plan map[string]any }
	if json.Unmarshal(raw, &plan) != nil || len(plan) != 1 {
		t.Fatal("real maximum-key index plan invalid")
	}
	kind, _ := plan[0].Plan["Node Type"].(string)
	if kind != "Index Scan" && kind != "Index Only Scan" {
		t.Fatal("exact maximum-key lookup did not use a real index")
	}
	if plan[0].Plan["Actual Rows"] != float64(1) || f.scalar(t, `SELECT count(*) FROM saml_persistent_identities WHERE tenant_id=$1 AND id=$2 AND name_id_value=$3::text`, f.tenant, target.ID, p.Subject.Value) != 1 {
		t.Fatal("maximum-key lookup lost exact stored value")
	}
}
