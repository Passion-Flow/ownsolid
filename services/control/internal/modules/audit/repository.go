package audit

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// WriteWithin uses the caller's actual business transaction. It never opens an
// independent connection or commits an event before its associated mutation.
func WriteWithin(ctx context.Context, tx transaction.Executor, actor, subject, audience, action, outcome, targetType, targetID, reason, requestID string) error {
	if tx == nil {
		return errors.New("AUDIT_TRANSACTION_REQUIRED")
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	var text string
	switch tx.Dialect() {
	case transaction.PostgreSQL:
		text = `INSERT INTO audit_events(id,occurred_at,actor_user_id,subject_user_id,audience,action,outcome,target_type,target_id,reason,request_id,tenant_id,workspace_id) VALUES($1,clock_timestamp(),NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5,$6,NULLIF($7,''),NULLIF($8,'')::uuid,NULLIF($9,''),$10,(SELECT tenant_id FROM installation_state WHERE id=1),(SELECT default_workspace_id FROM installation_state WHERE id=1))`
	case transaction.MySQL:
		text = `INSERT INTO audit_events(id,occurred_at,actor_user_id,subject_user_id,audience,action,outcome,target_type,target_id,reason,request_id,tenant_id,workspace_id) VALUES(?,UTC_TIMESTAMP(6),NULLIF(?,''),NULLIF(?,''),?,?,?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?,(SELECT tenant_id FROM installation_state WHERE id=1),(SELECT default_workspace_id FROM installation_state WHERE id=1))`
	case transaction.Dameng:
		text = `INSERT INTO AUDIT_EVENTS(ID,OCCURRED_AT,ACTOR_USER_ID,SUBJECT_USER_ID,AUDIENCE,ACTION,OUTCOME,TARGET_TYPE,TARGET_ID,REASON,REQUEST_ID,TENANT_ID,WORKSPACE_ID) VALUES(?,CURRENT_TIMESTAMP(6),NULLIF(?,''),NULLIF(?,''),?,?,?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?,(SELECT TENANT_ID FROM INSTALLATION_STATE WHERE ID=1),(SELECT DEFAULT_WORKSPACE_ID FROM INSTALLATION_STATE WHERE ID=1))`
	default:
		return errors.New("AUDIT_DIALECT_UNSUPPORTED")
	}
	n, e := tx.Exec(ctx, transaction.SQL(tx.Dialect(), text, uuid.NewString(), actor, subject, audience, action, outcome, targetType, targetID, reason, requestID))
	if e == nil && n != 1 {
		return errors.New("AUDIT_WRITE_UNCONFIRMED")
	}
	return e
}

func WriteAnonymousWithin(ctx context.Context, tx transaction.Executor, audience, action, outcome, requestID string, digest []byte) error {
	if tx == nil {
		return errors.New("AUDIT_TRANSACTION_REQUIRED")
	}
	if requestID == "" {
		requestID = uuid.NewString()
	}
	var text string
	switch tx.Dialect() {
	case transaction.PostgreSQL:
		text = `INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id,identifier_digest,tenant_id,workspace_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,(SELECT tenant_id FROM installation_state WHERE id=1),(SELECT default_workspace_id FROM installation_state WHERE id=1))`
	case transaction.MySQL:
		text = `INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id,identifier_digest,tenant_id,workspace_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,?,(SELECT tenant_id FROM installation_state WHERE id=1),(SELECT default_workspace_id FROM installation_state WHERE id=1))`
	case transaction.Dameng:
		text = `INSERT INTO AUDIT_EVENTS(ID,OCCURRED_AT,AUDIENCE,ACTION,OUTCOME,REQUEST_ID,IDENTIFIER_DIGEST,TENANT_ID,WORKSPACE_ID) VALUES(?,CURRENT_TIMESTAMP(6),?,?,?,?,?,(SELECT TENANT_ID FROM INSTALLATION_STATE WHERE ID=1),(SELECT DEFAULT_WORKSPACE_ID FROM INSTALLATION_STATE WHERE ID=1))`
	default:
		return errors.New("AUDIT_DIALECT_UNSUPPORTED")
	}
	n, e := tx.Exec(ctx, transaction.SQL(tx.Dialect(), text, uuid.NewString(), audience, action, outcome, requestID, digest))
	if e == nil && n != 1 {
		return errors.New("AUDIT_WRITE_UNCONFIRMED")
	}
	return e
}
