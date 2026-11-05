package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// idempotentWithin runs a write once per actor, operation and Idempotency-Key. A repeat
// of the same request returns the saved response in result; the same key with another
// request fails with IDEMPOTENCY_CONFLICT. It runs inside the write's transaction, so the
// record commits with the write or not at all. status is the HTTP status the write answers.
func (s *Service) idempotentWithin(ctx context.Context, tx transaction.Executor, actor, operation, key string, request []byte, status int, result any, run func() error) error {
	hash := sha256.Sum256(request)
	if _, e := tx.Exec(ctx, identitySQL(tx, "DELETE FROM idempotency_records WHERE actor_user_id=$1 AND operation_id=$2 AND idempotency_key=$3 AND expires_at<=clock_timestamp()", "DELETE FROM idempotency_records WHERE actor_user_id=? AND operation_id=? AND idempotency_key=? AND expires_at<=UTC_TIMESTAMP(6)", `DELETE FROM IDEMPOTENCY_RECORDS WHERE ACTOR_USER_ID=? AND OPERATION_ID=? AND IDEMPOTENCY_KEY=? AND EXPIRES_AT<=CURRENT_TIMESTAMP(6)`, actor, operation, key)); e != nil {
		return e
	}
	var saved, body []byte
	e := tx.QueryRow(ctx, identitySQL(tx, "SELECT request_hash,response_body FROM idempotency_records WHERE actor_user_id=$1 AND operation_id=$2 AND idempotency_key=$3", "SELECT request_hash,response_body FROM idempotency_records WHERE actor_user_id=? AND operation_id=? AND idempotency_key=?", `SELECT REQUEST_HASH,RESPONSE_BODY FROM IDEMPOTENCY_RECORDS WHERE ACTOR_USER_ID=? AND OPERATION_ID=? AND IDEMPOTENCY_KEY=?`, actor, operation, key)).Scan(&saved, &body)
	if e == nil {
		if !bytes.Equal(saved, hash[:]) {
			return failure("IDEMPOTENCY_CONFLICT")
		}
		return json.Unmarshal(body, result)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e = run(); e != nil {
		return e
	}
	if body, e = json.Marshal(result); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, identitySQL(tx, `INSERT INTO idempotency_records(actor_user_id,operation_id,idempotency_key,request_hash,response_status,response_body,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp(),clock_timestamp()+$7::bigint*interval '1 second')`, `INSERT INTO idempotency_records(actor_user_id,operation_id,idempotency_key,request_hash,response_status,response_body,created_at,expires_at) VALUES(?,?,?,?,?,?,UTC_TIMESTAMP(6),TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)))`, `INSERT INTO IDEMPOTENCY_RECORDS(ACTOR_USER_ID,OPERATION_ID,IDEMPOTENCY_KEY,REQUEST_HASH,RESPONSE_STATUS,RESPONSE_BODY,CREATED_AT,EXPIRES_AT) VALUES(?,?,?,?,?,?,CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6)+NUMTODSINTERVAL(?,'SECOND'))`, actor, operation, key, hash[:], status, identityJSON(tx, body), s.Config.Policy().Identity.IdempotencySeconds))
	return e
}
