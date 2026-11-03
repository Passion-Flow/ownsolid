package mail

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (o *Outbox) storage() (transaction.Store, error) {
	if o.Storage != nil {
		if o.DB != nil {
			return nil, errors.New("MAIL_STORAGE_CONFIGURATION_CONFLICT")
		}
		return o.Storage, nil
	}
	if o.DB == nil {
		return nil, errors.New("MAIL_STORAGE_REQUIRED")
	}
	return transaction.FromPostgreSQL(o.DB)
}

func statement(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}

func (o *Outbox) Enqueue(ctx context.Context, tx pgx.Tx, i Input) (Summary, error) {
	return o.EnqueueWithin(ctx, transaction.PostgreSQLTransaction(tx), i)
}
func (o *Outbox) EnqueueWithin(ctx context.Context, tx transaction.Executor, i Input) (Summary, error) {
	if tx == nil || o.Keys == nil {
		return Summary{}, errors.New("MAIL_ENQUEUE_CONFIGURATION_REQUIRED")
	}
	id := uuid.NewString()
	origin := o.Config.Server.PortalOrigin
	if i.Audience == "admin" {
		origin = o.Config.Server.AdminOrigin
	}
	route := "/reset-password"
	if i.Purpose == "invitation" {
		route = "/register"
	}
	link := ""
	if i.Token != "" {
		link = origin + route + "#token=" + i.Token
	}
	raw, e := json.Marshal(Payload{link})
	if e != nil {
		return Summary{}, errors.New("MAIL_PAYLOAD_INVALID")
	}
	key, nonce, cipher, e := o.Keys.Encrypt(id, i.Purpose, raw)
	if e != nil {
		return Summary{}, e
	}
	var generation any
	if i.InvitationID != "" {
		generation = i.Generation
	}
	n, e := tx.Exec(ctx, statement(tx,
		`INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,invitation_id,invitation_generation,reset_token_id,user_id,state,payload_key_id,payload_nonce,payload_ciphertext,next_attempt_at,message_id,created_at,updated_at) VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,NULLIF($7,'')::uuid,NULLIF($8,'')::uuid,'queued',$9,$10,$11,clock_timestamp(),$12,clock_timestamp(),clock_timestamp())`,
		`INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,invitation_id,invitation_generation,reset_token_id,user_id,state,payload_key_id,payload_nonce,payload_ciphertext,next_attempt_at,message_id,created_at,updated_at) VALUES(?,?,?,?,NULLIF(?,''),?,NULLIF(?,''),NULLIF(?,''),'queued',?,?,?,UTC_TIMESTAMP(6),?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, `INSERT INTO MAIL_DELIVERIES(ID,PURPOSE,RECIPIENT_EMAIL,LOCALE,INVITATION_ID,INVITATION_GENERATION,RESET_TOKEN_ID,USER_ID,STATE,PAYLOAD_KEY_ID,PAYLOAD_NONCE,PAYLOAD_CIPHERTEXT,NEXT_ATTEMPT_AT,MESSAGE_ID,CREATED_AT,UPDATED_AT) VALUES(?,?,?,?,NULLIF(?,''),?,NULLIF(?,''),NULLIF(?,''),'queued',?,?,?,CURRENT_TIMESTAMP(6),?,CURRENT_TIMESTAMP(6),CURRENT_TIMESTAMP(6))`,
		id, i.Purpose, i.Recipient, i.Locale, i.InvitationID, generation, i.ResetID, i.UserID, key, nonce, cipher, o.messageID(id)))
	if e != nil {
		return Summary{}, e
	}
	if n != 1 {
		return Summary{}, errors.New("MAIL_WRITE_UNCONFIRMED")
	}
	return Summary{ID: id, State: "queued"}, nil
}

func Latest(ctx context.Context, tx pgx.Tx, id string) (*Summary, error) {
	return LatestWithin(ctx, transaction.PostgreSQLTransaction(tx), id)
}
func LatestWithin(ctx context.Context, q transaction.Executor, id string) (*Summary, error) {
	var d Summary
	e := q.QueryRow(ctx, statement(q, "SELECT id::text,state,attempt_count,error_category FROM mail_deliveries WHERE invitation_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1", "SELECT id,state,attempt_count,error_category FROM mail_deliveries WHERE invitation_id=? ORDER BY created_at DESC,id DESC LIMIT 1", `SELECT ID,STATE,ATTEMPT_COUNT,ERROR_CATEGORY FROM MAIL_DELIVERIES WHERE INVITATION_ID=? ORDER BY CREATED_AT DESC,ID DESC LIMIT 1`, id)).Scan(&d.ID, &d.State, &d.AttemptCount, &d.ErrorCategory)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return &d, e
}

type cancellation int

const (
	cancelInvitation cancellation = iota
	cancelUserReset
)

func cancelWithin(ctx context.Context, tx transaction.Executor, kind cancellation, id string) error {
	var pg, my, dm string
	switch kind {
	case cancelInvitation:
		pg = "SELECT id::text FROM mail_deliveries WHERE invitation_id=$1 AND state IN ('queued','sending') ORDER BY id FOR UPDATE"
		my = "SELECT id FROM mail_deliveries WHERE invitation_id=? AND state IN ('queued','sending') ORDER BY id FOR UPDATE"
		dm = "SELECT ID FROM MAIL_DELIVERIES WHERE INVITATION_ID=? AND STATE IN ('queued','sending') ORDER BY ID FOR UPDATE"
	case cancelUserReset:
		pg = "SELECT id::text FROM mail_deliveries WHERE purpose='password_reset' AND user_id=$1 AND state IN ('queued','sending') ORDER BY id FOR UPDATE"
		my = "SELECT id FROM mail_deliveries WHERE purpose='password_reset' AND user_id=? AND state IN ('queued','sending') ORDER BY id FOR UPDATE"
		dm = "SELECT ID FROM MAIL_DELIVERIES WHERE PURPOSE='password_reset' AND USER_ID=? AND STATE IN ('queued','sending') ORDER BY ID FOR UPDATE"
	default:
		return errors.New("MAIL_CANCELLATION_KIND_INVALID")
	}
	rows, e := tx.Query(ctx, statement(tx, pg, my, dm, id))
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		n, e := tx.Exec(ctx, statement(tx,
			`UPDATE mail_deliveries SET state='cancelled',payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp(),error_category='token_obsolete' WHERE id=$1 AND state IN ('queued','sending')`,
			`UPDATE mail_deliveries SET state='cancelled',payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6),error_category='token_obsolete' WHERE id=? AND state IN ('queued','sending')`, `UPDATE MAIL_DELIVERIES SET STATE='cancelled',PAYLOAD_KEY_ID=NULL,PAYLOAD_NONCE=NULL,PAYLOAD_CIPHERTEXT=NULL,LEASE_TOKEN=NULL,LOCKED_UNTIL=NULL,COMPLETED_AT=CURRENT_TIMESTAMP(6),UPDATED_AT=CURRENT_TIMESTAMP(6),ERROR_CATEGORY='token_obsolete' WHERE ID=? AND STATE IN ('queued','sending')`, id))
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("MAIL_LOCKED_STATE_CHANGED")
		}
		if e = audit.WriteWithin(ctx, tx, "", "", "system", "mail.cancelled", "success", "mail_delivery", id, "", ""); e != nil {
			return e
		}
	}
	return nil
}
func CancelInvitation(ctx context.Context, tx pgx.Tx, id string) error {
	return CancelInvitationWithin(ctx, transaction.PostgreSQLTransaction(tx), id)
}
func CancelUserReset(ctx context.Context, tx pgx.Tx, id string) error {
	return CancelUserResetWithin(ctx, transaction.PostgreSQLTransaction(tx), id)
}
func CancelInvitationWithin(ctx context.Context, tx transaction.Executor, id string) error {
	return cancelWithin(ctx, tx, cancelInvitation, id)
}
func CancelUserResetWithin(ctx context.Context, tx transaction.Executor, id string) error {
	return cancelWithin(ctx, tx, cancelUserReset, id)
}

func (o *Outbox) recoverExhausted(ctx context.Context, tx transaction.Executor) error {
	var id string
	e := tx.QueryRow(ctx, statement(tx,
		`SELECT id::text FROM mail_deliveries WHERE attempt_count>=$1 AND (state='queued' OR (state='sending' AND locked_until<=clock_timestamp())) ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`,
		`SELECT id FROM mail_deliveries WHERE attempt_count>=? AND (state='queued' OR (state='sending' AND locked_until<=UTC_TIMESTAMP(6))) ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, `SELECT ID FROM MAIL_DELIVERIES WHERE ATTEMPT_COUNT>=? AND (STATE='queued' OR (STATE='sending' AND LOCKED_UNTIL<=CURRENT_TIMESTAMP(6))) ORDER BY CREATED_AT,ID FOR UPDATE 1 SKIP LOCKED`, o.Config.Policy().Mail.MaxAttempts)).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	n, e := tx.Exec(ctx, statement(tx,
		`UPDATE mail_deliveries SET state='failed',error_category=CASE WHEN state='sending' THEN 'smtp_unknown_outcome' ELSE COALESCE(error_category,'smtp_unknown_outcome') END,payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`,
		`UPDATE mail_deliveries SET state='failed',error_category=CASE WHEN state='sending' THEN 'smtp_unknown_outcome' ELSE COALESCE(error_category,'smtp_unknown_outcome') END,payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=?`, `UPDATE MAIL_DELIVERIES SET STATE='failed',ERROR_CATEGORY=CASE WHEN STATE='sending' THEN 'smtp_unknown_outcome' ELSE COALESCE(ERROR_CATEGORY,'smtp_unknown_outcome') END,PAYLOAD_KEY_ID=NULL,PAYLOAD_NONCE=NULL,PAYLOAD_CIPHERTEXT=NULL,LEASE_TOKEN=NULL,LOCKED_UNTIL=NULL,COMPLETED_AT=CURRENT_TIMESTAMP(6),UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, id))
	if e != nil {
		return e
	}
	if n != 1 {
		return errors.New("MAIL_LOCKED_STATE_CHANGED")
	}
	return audit.WriteWithin(ctx, tx, "", "", "system", "mail.failed", "failure", "mail_delivery", id, "", "")
}
func (o *Outbox) claim(ctx context.Context, store transaction.Store) (claimed, bool, error) {
	var c claimed
	found := false
	e := store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if e := o.recoverExhausted(ctx, tx); e != nil {
			return e
		}
		e := tx.QueryRow(ctx, statement(tx,
			`SELECT id::text,purpose,recipient_email,locale,payload_key_id,payload_nonce,payload_ciphertext,attempt_count,message_id FROM mail_deliveries WHERE attempt_count<$1 AND ((state='queued' AND next_attempt_at<=clock_timestamp()) OR (state='sending' AND locked_until<=clock_timestamp())) ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`,
			`SELECT id,purpose,recipient_email,locale,payload_key_id,payload_nonce,payload_ciphertext,attempt_count,message_id FROM mail_deliveries WHERE attempt_count<? AND ((state='queued' AND next_attempt_at<=UTC_TIMESTAMP(6)) OR (state='sending' AND locked_until<=UTC_TIMESTAMP(6))) ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, `SELECT ID,PURPOSE,RECIPIENT_EMAIL,LOCALE,PAYLOAD_KEY_ID,PAYLOAD_NONCE,PAYLOAD_CIPHERTEXT,ATTEMPT_COUNT,MESSAGE_ID FROM MAIL_DELIVERIES WHERE ATTEMPT_COUNT<? AND ((STATE='queued' AND NEXT_ATTEMPT_AT<=CURRENT_TIMESTAMP(6)) OR (STATE='sending' AND LOCKED_UNTIL<=CURRENT_TIMESTAMP(6))) ORDER BY CREATED_AT,ID FOR UPDATE 1 SKIP LOCKED`, o.Config.Policy().Mail.MaxAttempts)).Scan(&c.ID, &c.Purpose, &c.Recipient, &c.Locale, &c.Key, &c.Nonce, &c.Cipher, &c.Attempts, &c.MessageID)
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		c.Lease = uuid.NewString()
		n, e := tx.Exec(ctx, statement(tx,
			`UPDATE mail_deliveries SET state='sending',lease_token=$1,locked_until=clock_timestamp()+make_interval(secs=>$2),attempt_count=attempt_count+1,updated_at=clock_timestamp() WHERE id=$3`,
			`UPDATE mail_deliveries SET state='sending',lease_token=?,locked_until=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),attempt_count=attempt_count+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, `UPDATE MAIL_DELIVERIES SET STATE='sending',LEASE_TOKEN=?,LOCKED_UNTIL=CURRENT_TIMESTAMP(6)+NUMTODSINTERVAL(?,'SECOND'),ATTEMPT_COUNT=ATTEMPT_COUNT+1,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE ID=?`, c.Lease, o.Config.Policy().Mail.LeaseSeconds, c.ID))
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("MAIL_LOCKED_STATE_CHANGED")
		}
		c.Attempts++
		found = true
		return nil
	})
	if e != nil {
		return claimed{}, false, e
	}
	return c, found, nil
}
func (o *Outbox) eligible(ctx context.Context, store transaction.Store, c claimed) (bool, error) {
	var ok bool
	e := store.QueryRow(ctx, statement(store,
		`SELECT CASE d.purpose WHEN 'invitation' THEN EXISTS(SELECT 1 FROM invitations i JOIN workspaces w ON w.id=i.workspace_id JOIN tenants t ON t.id=i.tenant_id WHERE i.id=d.invitation_id AND i.status='pending' AND i.expires_at>clock_timestamp() AND i.token_generation=d.invitation_generation AND w.status='active' AND t.status='active') WHEN 'password_reset' THEN EXISTS(SELECT 1 FROM password_reset_tokens r JOIN users u ON u.id=r.user_id WHERE r.id=d.reset_token_id AND r.token_hash IS NOT NULL AND r.expires_at>clock_timestamp() AND u.status='active') ELSE true END FROM mail_deliveries d WHERE d.id=$1 AND d.state='sending' AND d.lease_token=$2 AND d.locked_until>clock_timestamp()`,
		`SELECT CASE d.purpose WHEN 'invitation' THEN EXISTS(SELECT 1 FROM invitations i JOIN workspaces w ON w.id=i.workspace_id JOIN tenants t ON t.id=i.tenant_id WHERE i.id=d.invitation_id AND i.status='pending' AND i.expires_at>UTC_TIMESTAMP(6) AND i.token_generation=d.invitation_generation AND w.status='active' AND t.status='active') WHEN 'password_reset' THEN EXISTS(SELECT 1 FROM password_reset_tokens r JOIN users u ON u.id=r.user_id WHERE r.id=d.reset_token_id AND r.token_hash IS NOT NULL AND r.expires_at>UTC_TIMESTAMP(6) AND u.status='active') ELSE true END FROM mail_deliveries d WHERE d.id=? AND d.state='sending' AND d.lease_token=? AND d.locked_until>UTC_TIMESTAMP(6)`, `SELECT CASE WHEN D.PURPOSE='invitation' THEN CASE WHEN EXISTS(SELECT 1 FROM INVITATIONS I JOIN WORKSPACES W ON W.ID=I.WORKSPACE_ID JOIN TENANTS T ON T.ID=I.TENANT_ID WHERE I.ID=D.INVITATION_ID AND I.STATUS='pending' AND I.EXPIRES_AT>CURRENT_TIMESTAMP(6) AND I.TOKEN_GENERATION=D.INVITATION_GENERATION AND W.STATUS='active' AND T.STATUS='active') THEN 1 ELSE 0 END WHEN D.PURPOSE='password_reset' THEN CASE WHEN EXISTS(SELECT 1 FROM PASSWORD_RESET_TOKENS R JOIN USERS U ON U.ID=R.USER_ID WHERE R.ID=D.RESET_TOKEN_ID AND R.TOKEN_HASH IS NOT NULL AND R.EXPIRES_AT>CURRENT_TIMESTAMP(6) AND U.STATUS='active') THEN 1 ELSE 0 END ELSE 1 END FROM MAIL_DELIVERIES D WHERE D.ID=? AND D.STATE='sending' AND D.LEASE_TOKEN=? AND D.LOCKED_UNTIL>CURRENT_TIMESTAMP(6)`, c.ID, c.Lease)).Scan(&ok)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return ok, e
}
func (o *Outbox) finish(ctx context.Context, store transaction.Store, c claimed, state, category string) error {
	return store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		n, e := tx.Exec(ctx, statement(tx,
			`UPDATE mail_deliveries SET state=$1,error_category=NULLIF($2,''),payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$3 AND state='sending' AND lease_token=$4 AND locked_until>clock_timestamp()`,
			`UPDATE mail_deliveries SET state=?,error_category=NULLIF(?,''),payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL,lease_token=NULL,locked_until=NULL,completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND state='sending' AND lease_token=? AND locked_until>UTC_TIMESTAMP(6)`, `UPDATE MAIL_DELIVERIES SET STATE=?,ERROR_CATEGORY=NULLIF(?,''),PAYLOAD_KEY_ID=NULL,PAYLOAD_NONCE=NULL,PAYLOAD_CIPHERTEXT=NULL,LEASE_TOKEN=NULL,LOCKED_UNTIL=NULL,COMPLETED_AT=CURRENT_TIMESTAMP(6),UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE ID=? AND STATE='sending' AND LEASE_TOKEN=? AND LOCKED_UNTIL>CURRENT_TIMESTAMP(6)`, state, category, c.ID, c.Lease))
		if e != nil {
			return e
		}
		if n == 0 {
			return nil
		}
		if n != 1 {
			return errors.New("MAIL_LOCKED_STATE_CHANGED")
		}
		outcome := "success"
		if state == "failed" {
			outcome = "failure"
		}
		return audit.WriteWithin(ctx, tx, "", "", "system", "mail."+state, outcome, "mail_delivery", c.ID, "", "")
	})
}
func (o *Outbox) requeue(ctx context.Context, store transaction.Store, c claimed, wait time.Duration, category string) error {
	if store.Dialect() == transaction.Dameng {
		if wait < 0 || wait%time.Microsecond != 0 {
			return errors.New("MAIL_RETRY_INTERVAL_INVALID")
		}
		_, e := store.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE MAIL_DELIVERIES SET STATE='queued',LEASE_TOKEN=NULL,LOCKED_UNTIL=NULL,NEXT_ATTEMPT_AT=CURRENT_TIMESTAMP(6)+NUMTODSINTERVAL(?/1000000.0,'SECOND'),UPDATED_AT=CURRENT_TIMESTAMP(6),ERROR_CATEGORY=? WHERE ID=? AND STATE='sending' AND LEASE_TOKEN=? AND LOCKED_UNTIL>CURRENT_TIMESTAMP(6)`, wait.Microseconds(), category, c.ID, c.Lease))
		return e
	}
	q := transaction.SQL(transaction.PostgreSQL, `UPDATE mail_deliveries SET state='queued',lease_token=NULL,locked_until=NULL,next_attempt_at=clock_timestamp()+make_interval(secs=>$1),updated_at=clock_timestamp(),error_category=$2 WHERE id=$3 AND state='sending' AND lease_token=$4 AND locked_until>clock_timestamp()`, wait.Seconds(), category, c.ID, c.Lease)
	if store.Dialect() == transaction.MySQL {
		q = transaction.SQL(transaction.MySQL, `UPDATE mail_deliveries SET state='queued',lease_token=NULL,locked_until=NULL,next_attempt_at=TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6),error_category=? WHERE id=? AND state='sending' AND lease_token=? AND locked_until>UTC_TIMESTAMP(6)`, wait.Microseconds(), category, c.ID, c.Lease)
	}
	_, e := store.Exec(ctx, q)
	return e
}
