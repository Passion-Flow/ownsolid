//go:build repository_integration && object_storage_integration

package artifactstores

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

func (g *objectStory) workerAuthority(t *testing.T, leaf string, source []byte) {
	t.Helper()
	o, _, _ := g.create(t, source)
	want := "AUTH_SESSION_EXPIRED"
	switch leaf {
	case "prepared_worker_origin_session_revoked":
		g.exec(t, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_worker_revoke' WHERE id=$1", "UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_worker_revoke' WHERE id=?", `UPDATE "SESSIONS" SET "REVOKED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)),"REVOKE_REASON"='synthetic_worker_revoke' WHERE "ID"=?`, g.portal.SessionID)
	case "prepared_worker_original_digest_changed":
		// Explicit SQL fault models digest replacement, not an actual login journey.
		hash := sha256.Sum256([]byte("explicit synthetic replacement session digest"))
		g.exec(t, "UPDATE sessions SET token_hash=$2 WHERE id=$1", "UPDATE sessions SET token_hash=? WHERE id=?", `UPDATE "SESSIONS" SET "TOKEN_HASH"=? WHERE "ID"=?`, workerDigestFaultArgs(g, hash[:])...)
	case "prepared_legacy_authority_requires_explicit_reconcile":
		legacy, err := json.Marshal(map[string]any{"Actor": g.portal.Me.User.ID, "Session": g.portal.SessionID, "Audience": g.portal.Me.Audience, "Epoch": g.portal.Epoch})
		if err != nil {
			t.Fatal(err)
		}
		g.exec(t, "UPDATE artifact_objects SET operation_authority=$2::jsonb WHERE id=$1", "UPDATE artifact_objects SET operation_authority=CAST(? AS JSON) WHERE id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_AUTHORITY"=? WHERE "ID"=?`, workerLegacyFaultArgs(g, o.ID, legacy)...)
		want = "OBJECT_AUTHORITY_INVALID"
	default:
		t.Fatal("unrecognized frozen worker authority selector")
	}
	worked, err := g.s.ProcessNext(g.ctx)
	if err == nil || err.Error() != want || worked {
		t.Fatal("unproven worker authority executed", err, worked)
	}
	var phase, oldOperation, code string
	if err = g.f.Store.QueryRow(g.ctx, statement(g.f.Store, "SELECT operation_phase,operation_id::text,error_code FROM artifact_objects WHERE id=$1", "SELECT operation_phase,operation_id,error_code FROM artifact_objects WHERE id=?", `SELECT "OPERATION_PHASE","OPERATION_ID","ERROR_CODE" FROM "ARTIFACT_OBJECTS" WHERE "ID"=?`, o.ID)).Scan(&phase, &oldOperation, &code); err != nil || phase != "prepared" || code != want {
		t.Fatal("failed original preparation was not retained", err)
	}
	uploads, err := g.s.Transport.FindUploads(g.ctx, objectstore.Ref{Tenant: g.f.Tenant, Workspace: g.f.Workspace, Object: o.ID})
	if err != nil || len(uploads) != 0 {
		t.Fatal("refused worker reached remote create", err)
	}
	if worked, err = g.s.ProcessNext(g.ctx); err != nil || worked {
		t.Fatal("failed prepared operation retried automatically", err)
	}
	if leaf != "prepared_legacy_authority_requires_explicit_reconcile" {
		return
	}
	if _, err = g.s.Reconcile(g.ctx, g.portal, g.f.Workspace, o.ID, "Explicitly reauthorize legacy unsent preparation", uuid.NewString()); err != nil {
		t.Fatal("current actor could not explicitly recover unsent legacy intent", err)
	}
	var nextOperation string
	var raw []byte
	if err = g.f.Store.QueryRow(g.ctx, statement(g.f.Store, "SELECT operation_id::text,operation_authority FROM artifact_objects WHERE id=$1", "SELECT operation_id,operation_authority FROM artifact_objects WHERE id=?", `SELECT "OPERATION_ID","OPERATION_AUTHORITY" FROM "ARTIFACT_OBJECTS" WHERE "ID"=?`, o.ID)).Scan(&nextOperation, &raw); err != nil || nextOperation == oldOperation {
		t.Fatal("recovery reused original operation identity", err)
	}
	a, err := actorFrom(raw)
	if err != nil || a.SessionID != g.portal.SessionID || !bytes.Equal(a.TokenHash, g.portal.TokenHash) {
		t.Fatal("explicit recovery did not bind exact current authority", err)
	}
	g.work(t)
	current, err := g.s.Get(g.ctx, g.portal, g.f.Workspace, o.ID, "")
	if err != nil || current.State != "uploading" {
		t.Fatal("explicitly reauthorized preparation did not execute", err)
	}
}

func workerDigestFaultArgs(g *objectStory, hash []byte) []any {
	if positionalArguments(g.f.Store) {
		return []any{hash, g.portal.SessionID}
	}
	return []any{g.portal.SessionID, hash}
}

func workerLegacyFaultArgs(g *objectStory, object string, raw []byte) []any {
	if positionalArguments(g.f.Store) {
		return []any{jsonParameter(g.f.Store, raw), object}
	}
	return []any{object, raw}
}
