package distribution

import (
	"context"
	"database/sql"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// MaximumRotationOverlapSeconds bounds how long a rotated key keeps working beside its
// successor: a week covers a deploy cycle, and a leaked old secret still dies soon.
const MaximumRotationOverlapSeconds = 7 * 24 * 60 * 60

// minimumKeyLifetimeSeconds is the shortest lifetime issue accepts, so a successor never gets less.
const minimumKeyLifetimeSeconds = 300

// rotationFingerprint binds a rotation's idempotency key to the key it replaces and its body.
type rotationFingerprint struct {
	KeyID          string `json:"key_id"`
	OverlapSeconds int    `json:"overlap_seconds"`
	Reason         string `json:"reason"`
}

// predecessor is what a successor copies from the key it replaces.
type predecessor struct {
	aliasID        string
	label          string
	owner          string
	epoch          int64
	authority      string
	lifetime       int
	successorID    *string
	successorKey   *string
	successorPrint []byte
}

// RotateKey issues a successor to an active key with the same workspace, alias, label, owner and
// authority, and stops the old key after the overlap, or at once when the overlap is zero. The
// new secret is returned once; a repeated request returns the successor without it. A key has at
// most one successor (docs/research/gateway-keys.md).
func (s *Service) RotateKey(ctx context.Context, a identity.Auth, workspace, id string, input RotateRequest, key, request string) (MintedKey, error) {
	if !wire.ValidID(id) || !wire.ValidID(workspace) || !wire.ValidID(key) || !reason(input.Reason) || input.OverlapSeconds < 0 || input.OverlapSeconds > MaximumRotationOverlapSeconds {
		return MintedKey{}, fail("VALIDATION_FAILED")
	}
	if _, e := s.Workspaces.Get(ctx, a, workspace, false); e != nil {
		return MintedKey{}, e
	}
	owner, e := s.keyOwner(ctx, a, workspace, id)
	if e != nil {
		return MintedKey{}, e
	}
	expected := fingerprint(a.Me.User.ID, rotationFingerprint{KeyID: id, OverlapSeconds: input.OverlapSeconds, Reason: input.Reason})
	var out MintedKey
	e = s.transaction(ctx, a, workspace, false, []string{owner}, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		// Only the PostgreSQL chain records a successor (CMP-201, CMP-202).
		if tx.Dialect() != transaction.PostgreSQL {
			return fail("API_KEY_ROTATION_UNAVAILABLE")
		}
		current, e := scanKey(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+keyFields+keyJoins+" WHERE k.tenant_id=$1 AND k.workspace_id=$2 AND k.id=$3 AND ($4 OR k.created_by=$5) FOR UPDATE OF k", w.TenantID, w.ID, id, admin(a), a.Me.User.ID)))
		if e != nil {
			return e
		}
		old, e := readPredecessor(ctx, tx, id)
		if e != nil {
			return e
		}
		if old.successorID != nil {
			if *old.successorKey != key {
				return fail("API_KEY_ALREADY_ROTATED")
			}
			if !same(old.successorPrint, expected) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out.Key, e = scanKey(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+keyFields+keyJoins+" WHERE k.id=$1", *old.successorID)))
			return e
		}
		if current.State != "active" {
			return fail("API_KEY_NOT_ACTIVE")
		}
		lifetime := min(max(old.lifetime, minimumKeyLifetimeSeconds), s.Identity.Config.Policy().Distribution.KeyMaxTTLSeconds)
		successor := uuid.NewString()
		token := security.Token()
		raw := apiKeyPrefix + "." + successor + "." + token
		if _, e = distributionExecOne(ctx, tx, transaction.SQL(transaction.PostgreSQL, "INSERT INTO api_keys(id,tenant_id,workspace_id,alias_id,label,secret_hash,created_by,requested_epoch,idempotency_key,fingerprint,expires_at,membership_authority,rotated_from) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,clock_timestamp()+make_interval(secs=>$11),$12,$13)", successor, w.TenantID, w.ID, old.aliasID, old.label, s.digest(ordinaryKeyPurpose, successor, token), old.owner, old.epoch, key, expected, lifetime, old.authority, id)); e != nil {
			return rotationConflict(e)
		}
		stop := transaction.SQL(transaction.PostgreSQL, "UPDATE api_keys SET expires_at=LEAST(expires_at,clock_timestamp()+make_interval(secs=>$2)) WHERE id=$1", id, input.OverlapSeconds)
		if input.OverlapSeconds == 0 {
			stop = transaction.SQL(transaction.PostgreSQL, "UPDATE api_keys SET revoked_at=clock_timestamp(),revoke_reason=$2 WHERE id=$1", id, input.Reason)
		}
		if _, e = distributionExecOne(ctx, tx, stop); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "gateway.api_key_rotated", "api_key", id, input.Reason, request); e != nil {
			return e
		}
		if e = s.audit(ctx, tx, a, w, "gateway.api_key_created", "api_key", successor, input.Reason, request); e != nil {
			return e
		}
		out.Key, e = scanKey(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+keyFields+keyJoins+" WHERE k.id=$1", successor)))
		if e != nil {
			return e
		}
		out.Secret = &raw
		out.SecretAvailable = true
		out.Created = true
		return nil
	})
	if e != nil {
		return MintedKey{}, e
	}
	return out, nil
}

// keyOwner finds who owns a key the caller may act on, so the transaction can lock that person
// before the key; a key the caller cannot see is not found.
func (s *Service) keyOwner(ctx context.Context, a identity.Auth, workspace, id string) (string, error) {
	var owner string
	e := s.storageTransaction(ctx, func(ctx context.Context, q transaction.Executor) error {
		return q.QueryRow(ctx, distributionNativeSQL(q, `SELECT CREATED_BY FROM API_KEYS WHERE ID=? AND WORKSPACE_ID=? AND (?=1 OR CREATED_BY=?)`, "SELECT created_by::text FROM api_keys WHERE id=$1 AND workspace_id=$2 AND ($3 OR created_by=$4)", "SELECT created_by FROM api_keys WHERE id=? AND workspace_id=? AND (? OR created_by=?)", id, workspace, admin(a), a.Me.User.ID)).Scan(&owner)
	})
	if errors.Is(e, sql.ErrNoRows) || e != nil && e.Error() == string(transaction.NoRows) {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	return owner, e
}

func readPredecessor(ctx context.Context, tx transaction.Executor, id string) (predecessor, error) {
	var out predecessor
	e := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT k.alias_id::text,k.label,k.created_by::text,k.requested_epoch,k.membership_authority::text,ceil(extract(epoch FROM k.expires_at-k.created_at))::integer,s.id::text,s.idempotency_key::text,s.fingerprint FROM api_keys k LEFT JOIN api_keys s ON s.rotated_from=k.id WHERE k.id=$1", id)).Scan(&out.aliasID, &out.label, &out.owner, &out.epoch, &out.authority, &out.lifetime, &out.successorID, &out.successorKey, &out.successorPrint)
	return out, e
}

// rotationConflict reports an idempotency key the owner already used for another key. Rotations of
// one key serialize on its owner's row and the key's own, so the successor index is only a backstop.
func rotationConflict(e error) error {
	var stored *transaction.Error
	if errors.As(e, &stored) && stored.Kind == transaction.UniqueConflict {
		return fail("IDEMPOTENCY_CONFLICT")
	}
	return e
}
