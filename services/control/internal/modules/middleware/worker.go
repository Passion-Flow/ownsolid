package middleware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type actorRecord struct {
	User      string `json:"user"`
	Audience  string `json:"audience"`
	Session   string `json:"session"`
	TokenHash []byte `json:"token_hash"`
	Epoch     int64  `json:"epoch"`
}

func snapshot(a identity.Auth) actorRecord {
	return actorRecord{a.Me.User.ID, a.Me.Audience, a.SessionID, append([]byte(nil), a.TokenHash...), a.Epoch}
}
func (r actorRecord) auth() identity.Auth {
	var a identity.Auth
	a.Me.User.ID = r.User
	a.Me.Audience = r.Audience
	a.SessionID = r.Session
	a.TokenHash = append([]byte(nil), r.TokenHash...)
	a.Epoch = r.Epoch
	return a
}

type claimed struct {
	Operation
	actor             identity.Auth
	input             OperationInput
	instanceVersion   int64
	fingerprint       string
	intentFingerprint string
	request           string
	attempt           string
	requestedBy       string
}

const workPG = `id::text,tenant_id::text,workspace_id::text,instance_id::text,kind,state,actor_json,input_json,instance_version,profile_fingerprint,intent_fingerprint,request_id::text,requested_by::text,COALESCE(attempt_id::text,'')`
const workMy = `id,tenant_id,workspace_id,instance_id,kind,state,actor_json,input_json,instance_version,profile_fingerprint,intent_fingerprint,request_id,requested_by,COALESCE(attempt_id,'')`

func scanClaim(row transaction.Row) (*claimed, error) {
	c := &claimed{}
	var actorJSON, inputJSON string
	e := row.Scan(&c.ID, &c.TenantID, &c.WorkspaceID, &c.InstanceID, &c.Kind, &c.State, &actorJSON, &inputJSON, &c.instanceVersion, &c.fingerprint, &c.intentFingerprint, &c.request, &c.requestedBy, &c.attempt)
	if e != nil {
		return nil, e
	}
	var saved actorRecord
	if len(actorJSON) > 65536 || len(inputJSON) > 65536 || wire.StrictJSON([]byte(actorJSON), &saved) != nil || wire.StrictJSON([]byte(inputJSON), &c.input) != nil {
		return nil, fail("MIDDLEWARE_STORED_INTENT_INVALID")
	}
	c.actor = saved.auth()
	if !actor(c.actor) || c.actor.Me.User.ID != c.requestedBy || c.Kind != c.input.Kind || c.instanceVersion != c.input.Version || c.intentFingerprint != hash([]byte(c.InstanceID+"\x00"+inputJSON+"\x00"+c.fingerprint)) || !validID(c.ID) || !validID(c.TenantID) || !validID(c.WorkspaceID) || !validID(c.InstanceID) || !validID(c.request) || !validID(c.input.IdempotencyKey) {
		return nil, fail("MIDDLEWARE_STORED_INTENT_INVALID")
	}
	return c, nil
}

func workerAudit(ctx context.Context, q transaction.Executor, c *claimed, action, outcome, code string) error {
	_, e := q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id,metadata) VALUES($1,clock_timestamp(),$2,$3,$4,'system',$5,$6,'middleware_operation',$7,$8,$9::jsonb)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'system',?,?,'middleware_operation',?,?,CAST(? AS JSON))`, uuid.NewString(), c.requestedBy, c.TenantID, c.WorkspaceID, action, outcome, c.ID, c.request, string(mustJSON(struct {
		Code string `json:"code"`
	}{code}))))
	return e
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (s *Service) expire(ctx context.Context) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	return store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		rows, e := q.Query(ctx, statement(q, "SELECT "+workPG+" FROM middleware_operations WHERE state='running' AND lease_expires_at<=clock_timestamp() ORDER BY lease_expires_at,id LIMIT $1 FOR UPDATE SKIP LOCKED", "SELECT "+workMy+" FROM middleware_operations WHERE state='running' AND lease_expires_at<=UTC_TIMESTAMP(6) ORDER BY lease_expires_at,id LIMIT ? FOR UPDATE SKIP LOCKED", s.configuration.Policy.PageSize))
		if e != nil {
			return e
		}
		// A bounded batch permits vendor connections to finish their cursor before
		// any mutation. Only small intent metadata is retained, never file payloads.
		batch := []*claimed{}
		for rows.Next() {
			c, e := scanClaim(rows)
			if e != nil {
				rows.Close()
				return e
			}
			batch = append(batch, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, c := range batch {
			_, e = q.Exec(ctx, statement(q, `UPDATE middleware_operations SET state='unknown',lease_expires_at=NULL,error_code='MIDDLEWARE_EXECUTION_UNCERTAIN',completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND state='running'`, `UPDATE middleware_operations SET state='unknown',lease_expires_at=NULL,error_code='MIDDLEWARE_EXECUTION_UNCERTAIN',completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND state='running'`, c.ID))
			if e != nil {
				return e
			}
			if e = workerAudit(ctx, q, c, "middleware.operation_uncertain", "failure", "MIDDLEWARE_EXECUTION_UNCERTAIN"); e != nil {
				return e
			}
		}
		return nil
	})
}

func (s *Service) candidate(ctx context.Context) (*claimed, error) {
	store, e := s.repository()
	if e != nil {
		return nil, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	c, e := scanClaim(store.QueryRow(ctx, statement(store, "SELECT "+workPG+" FROM middleware_operations WHERE state='queued' ORDER BY created_at,id LIMIT 1", "SELECT "+workMy+" FROM middleware_operations WHERE state='queued' ORDER BY created_at,id LIMIT 1")))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return c, e
}

func (s *Service) claim(ctx context.Context, c *claimed) (bool, error) {
	store, e := s.repository()
	if e != nil {
		return false, e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	claimedNow := false
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, c.actor, c.WorkspaceID, true)
		if e != nil {
			return e
		}
		if w.TenantID != c.TenantID {
			return fail("MIDDLEWARE_SCOPE_CHANGED")
		}
		v, e := instanceWithin(ctx, q, w, c.InstanceID, true)
		if e != nil {
			return e
		}
		if v.State != "active" || v.Version != c.instanceVersion || v.Fingerprint != c.fingerprint {
			return fail("MIDDLEWARE_INPUT_CHANGED")
		}
		current, e := scanClaim(q.QueryRow(ctx, statement(q, "SELECT "+workPG+" FROM middleware_operations WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", "SELECT "+workMy+" FROM middleware_operations WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE", c.TenantID, c.WorkspaceID, c.ID)))
		if e != nil {
			return e
		}
		if current.State != "queued" {
			return nil
		}
		p, e := s.profile(v.ProfileKey)
		if e != nil {
			return e
		}
		captured, e := Capture(p)
		if e != nil {
			return e
		}
		if captured.fingerprint != c.fingerprint {
			return fail("MIDDLEWARE_INPUT_CHANGED")
		}
		c.attempt = uuid.NewString()
		_, e = q.Exec(ctx, statement(q, `UPDATE middleware_operations SET state='running',attempt_id=$1,lease_expires_at=clock_timestamp()+$2::bigint*interval '1 second',updated_at=clock_timestamp() WHERE id=$3 AND state='queued'`, `UPDATE middleware_operations SET state='running',attempt_id=?,lease_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND state='queued'`, c.attempt, s.configuration.Policy.LeaseSeconds, c.ID))
		if e != nil {
			return e
		}
		if e = workerAudit(ctx, q, c, "middleware.operation_started", "accepted", ""); e != nil {
			return e
		}
		claimedNow = true
		return nil
	})
	if e != nil {
		return false, e
	}
	return claimedNow, nil
}

func (s *Service) renew(ctx context.Context, c *claimed) error {
	if e := s.Identity.RequireBusinessIntegrity(ctx); e != nil {
		return e
	}
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	return store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authority(ctx, q, c.actor, c.WorkspaceID, true)
		if e != nil {
			return e
		}
		if w.TenantID != c.TenantID {
			return fail("MIDDLEWARE_SCOPE_CHANGED")
		}
		v, e := instanceWithin(ctx, q, w, c.InstanceID, true)
		if e != nil {
			return e
		}
		if v.State != "active" || v.Version != c.instanceVersion || v.Fingerprint != c.fingerprint {
			return fail("MIDDLEWARE_INPUT_CHANGED")
		}
		p, e := s.profile(v.ProfileKey)
		if e != nil {
			return e
		}
		captured, e := Capture(p)
		if e != nil {
			return e
		}
		if captured.fingerprint != c.fingerprint {
			return fail("MIDDLEWARE_INPUT_CHANGED")
		}
		n, e := q.Exec(ctx, statement(q, `UPDATE middleware_operations SET lease_expires_at=clock_timestamp()+$1::bigint*interval '1 second',updated_at=clock_timestamp() WHERE id=$2 AND state='running' AND attempt_id=$3 AND lease_expires_at>clock_timestamp()`, `UPDATE middleware_operations SET lease_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND state='running' AND attempt_id=? AND lease_expires_at>UTC_TIMESTAMP(6)`, s.configuration.Policy.LeaseSeconds, c.ID, c.attempt))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("MIDDLEWARE_ATTEMPT_STALE")
		}
		return nil
	})
}

func (s *Service) terminal(ctx context.Context, c *claimed, state, code string, result []byte, requireAuthority bool) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := s.bounded(ctx)
	defer cancel()
	if state == "succeeded" && (len(result) == 0 || len(result) > s.configuration.Policy.MaximumEvidenceBytes || !json.Valid(result)) {
		return fail("MIDDLEWARE_RESULT_INVALID")
	}
	return store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if requireAuthority {
			w, e := s.authority(ctx, q, c.actor, c.WorkspaceID, true)
			if e != nil {
				return e
			}
			if w.TenantID != c.TenantID {
				return fail("MIDDLEWARE_SCOPE_CHANGED")
			}
			v, e := instanceWithin(ctx, q, w, c.InstanceID, true)
			if e != nil {
				return e
			}
			if v.State != "active" || v.Version != c.instanceVersion || v.Fingerprint != c.fingerprint {
				return fail("MIDDLEWARE_INPUT_CHANGED")
			}
			p, e := s.profile(v.ProfileKey)
			if e != nil {
				return e
			}
			captured, e := Capture(p)
			if e != nil {
				return e
			}
			if captured.fingerprint != c.fingerprint {
				return fail("MIDDLEWARE_INPUT_CHANGED")
			}
		}
		var raw, digest, errorCode any
		if state == "succeeded" {
			raw = string(result)
			digest = hash(result)
		} else {
			errorCode = code
		}
		expected := "running"
		if c.attempt == "" {
			expected = "queued"
		}
		pg := `UPDATE middleware_operations SET state=$1,result_json=$2,result_sha256=$3,error_code=$4,lease_expires_at=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$5 AND state=$6 AND COALESCE(attempt_id::text,'')=$7`
		my := `UPDATE middleware_operations SET state=?,result_json=?,result_sha256=?,error_code=?,lease_expires_at=NULL,completed_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND state=? AND COALESCE(attempt_id,'')=?`
		if state == "succeeded" {
			pg += " AND lease_expires_at>clock_timestamp()"
			my += " AND lease_expires_at>UTC_TIMESTAMP(6)"
		}
		n, e := q.Exec(ctx, statement(q, pg, my, state, raw, digest, errorCode, c.ID, expected, c.attempt))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("MIDDLEWARE_ATTEMPT_STALE")
		}
		outcome := "failure"
		if state == "succeeded" {
			outcome = "success"
		}
		return workerAudit(ctx, q, c, "middleware.operation_"+state, outcome, code)
	})
}

func rejection(e error) bool {
	if e == nil {
		return false
	}
	code := normalize(e).Error()
	return code == "AUTH_SESSION_EXPIRED" || code == "ACCESS_DENIED" || code == "RESOURCE_NOT_FOUND" || code == "MIDDLEWARE_INPUT_CHANGED" || code == "MIDDLEWARE_SCOPE_CHANGED" || code == "MIDDLEWARE_PROFILE_UNAVAILABLE"
}

// Pass executes at most one queued operation. Expired running operations are
// marked uncertain; they never become executable candidates again.
func (s *Service) Pass(ctx context.Context) error {
	if _, e := s.repository(); e != nil {
		return e
	}
	if e := s.Identity.RequireBusinessIntegrity(ctx); e != nil {
		return e
	}
	if e := s.expire(ctx); e != nil {
		return e
	}
	c, e := s.candidate(ctx)
	if e != nil || c == nil {
		return e
	}
	started, e := s.claim(ctx, c)
	if e != nil {
		if rejection(e) {
			c.attempt = ""
			return s.terminal(ctx, c, "failed", normalize(e).Error(), nil, false)
		}
		return e
	}
	if !started {
		return nil
	}
	work, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.Policy.OperationSeconds)*time.Second)
	defer cancel()
	type completed struct {
		data []byte
		err  error
	}
	done := make(chan completed, 1)
	go func() { data, e := s.execute(work, c); done <- completed{data, e} }()
	ticker := time.NewTicker(time.Duration(s.configuration.Policy.RenewSeconds) * time.Second)
	defer ticker.Stop()
	var result completed
	for {
		select {
		case result = <-done:
			goto finished
		case <-work.Done():
			cancel()
			result = <-done
			if result.err == nil {
				result.err = work.Err()
			}
			goto finished
		case <-ticker.C:
			if e = s.renew(work, c); e != nil {
				cancel()
				result = <-done
				result.data = nil
				result.err = e
				goto finished
			}
		}
	}
finished:
	// The actual owner has returned and its Go call is joined before publication.
	// Its journal still owns any retained container/SQL/file cleanup obligation.
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(s.configuration.Policy.RequestSeconds)*time.Second)
	defer stop()
	if e = s.Identity.RequireBusinessIntegrity(cleanup); e != nil {
		return e
	}
	if result.err != nil {
		state := "unknown"
		if c.Kind == "health" {
			state = "failed"
		}
		return s.terminal(cleanup, c, state, safeExecutionCode(result.err), nil, false)
	}
	if e = s.terminal(cleanup, c, "succeeded", "", result.data, true); e != nil {
		if rejection(e) {
			state := "unknown"
			if c.Kind == "health" {
				state = "failed"
			}
			return s.terminal(cleanup, c, state, normalize(e).Error(), nil, false)
		}
		return e
	}
	return nil
}

func safeExecutionCode(e error) string {
	code := normalize(e).Error()
	if code == "MIDDLEWARE_STORAGE_UNAVAILABLE" {
		return "MIDDLEWARE_EXECUTION_UNCERTAIN"
	}
	return code
}

// Run belongs to the existing control process lifecycle. Its caller cancels and
// joins it; no independent daemon, shutdown timeout success or process is added.
func (s *Service) Run(ctx context.Context) error {
	s.workerMu.Lock()
	if s.workerRunning {
		s.workerMu.Unlock()
		return fail("MIDDLEWARE_WORKER_ALREADY_RUNNING")
	}
	s.workerRunning = true
	s.workerMu.Unlock()
	defer func() { s.workerMu.Lock(); s.workerRunning = false; s.workerMu.Unlock() }()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if e := s.Pass(ctx); e != nil {
			if ctx.Err() != nil && transaction.IsCancellationOnly(e) {
				return nil
			}
			return e
		}
		timer := time.NewTimer(time.Duration(s.configuration.Policy.PollMilliseconds) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
