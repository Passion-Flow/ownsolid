//go:build repository_integration

package retention

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

// All rows use the actual migrated product schema. Ciphertext/hash/identity
// envelopes below exercise retention only; no delivery/credential claim is made.
type retentionFixture struct {
	t         *testing.T
	f         repositoryfixture.Fixture
	ctx       context.Context
	now       time.Time
	policy    config.RetentionPolicy
	workspace string
	protected []retentionProjection
}
type retentionProjection struct {
	table, wherePG, whereMy string
	args                    []any
}
type retentionRows struct {
	expire, reset, cancel, terminal, oldReset, oldInvite, session, idem, audit []string
	rate                                                                       [][]byte
}

var retentionSnapshotTables = map[string]string{
	"tenants": "id", "workspaces": "id", "users": "id", "installation_state": "id",
	"invitations": "id", "password_reset_tokens": "id", "mail_deliveries": "id", "sessions": "id",
	"rate_limit_buckets": "bucket_key", "idempotency_records": "actor_user_id,operation_id,idempotency_key", "audit_events": "id",
}
var retentionColumnIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func newRetentionFixture(t *testing.T, seeded bool) *retentionFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	var f repositoryfixture.Fixture
	if seeded {
		f = repositoryfixture.New(t)
	} else {
		f = repositoryfixture.NewUnbootstrapped(t)
	}
	g := &retentionFixture{t: t, f: f, ctx: ctx, policy: config.DefaultRuntimePolicy().Retention}
	if e := f.Store.QueryRow(ctx, f.Statement(`SELECT clock_timestamp()`, `SELECT UTC_TIMESTAMP(6)`)).Scan(&g.now); e != nil {
		t.Fatal("actual retention clock", e)
	}
	if seeded {
		g.workspace = uuid.NewString()
		g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Retention actual Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Retention actual Workspace','active')`, g.workspace, f.Tenant)
	}
	return g
}
func (g *retentionFixture) exec(pg, my string, args ...any) {
	g.t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		g.t.Fatal("actual retention fixture statement refused", e)
	}
}
func (g *retentionFixture) count(pg, my string, args ...any) int {
	g.t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(pg, my, args...)).Scan(&n); e != nil {
		g.t.Fatal("actual retention read", e)
	}
	return n
}
func (g *retentionFixture) pass() {
	g.t.Helper()
	if e := PassSelected(g.ctx, g.f.Store, g.policy); e != nil {
		g.t.Fatal("actual selected retention pass", e)
	}
}
func retentionBytes() []byte { v := sha256.Sum256([]byte(uuid.NewString())); return v[:] }
func (g *retentionFixture) protect(table string, key any) {
	column := "id"
	if table == "rate_limit_buckets" {
		column = "bucket_key"
	}
	if table == "idempotency_records" {
		column = "idempotency_key"
	}
	g.protected = append(g.protected, retentionProjection{table, column + "=$1", column + "=?", []any{key}})
}
func (g *retentionFixture) digest(projections []retentionProjection) string {
	g.t.Helper()
	sum := sha256.New()
	if e := g.f.Store.Within(g.ctx, true, func(ctx context.Context, q transaction.Executor) error {
		for _, p := range projections {
			order, ok := retentionSnapshotTables[p.table]
			if !ok {
				return errors.New("RETENTION_TEST_UNOWNED_PROJECTION")
			}
			columns, e := q.Query(ctx, g.f.Statement(`SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 ORDER BY ordinal_position`, `SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`, p.table))
			if e != nil {
				return e
			}
			var names []string
			for columns.Next() {
				var name string
				if e = columns.Scan(&name); e != nil {
					columns.Close()
					return e
				}
				if !retentionColumnIdentifier.MatchString(name) {
					columns.Close()
					return errors.New("RETENTION_TEST_COLUMN_INVALID")
				}
				names = append(names, name)
			}
			e = columns.Err()
			columns.Close()
			if e != nil {
				return e
			}
			if len(names) == 0 {
				return errors.New("RETENTION_TEST_ACTUAL_TABLE_REQUIRED")
			}
			// Names/table/order are actual validated metadata or the fixed test inventory.
			// This selects all actual columns; it does not rewrite vendor mutations.
			pg, my := "SELECT * FROM "+p.table, "SELECT * FROM "+p.table
			if p.wherePG != "" {
				pg += " WHERE " + p.wherePG
				my += " WHERE " + p.whereMy
			}
			pg += " ORDER BY " + order
			my += " ORDER BY " + order
			rows, e := q.Query(ctx, g.f.Statement(pg, my, p.args...))
			if e != nil {
				return e
			}
			fmt.Fprintln(sum, p.table, strings.Join(names, ","))
			for rows.Next() {
				values := make([]any, len(names))
				dest := make([]any, len(names))
				for n := range values {
					dest[n] = &values[n]
				}
				if e = rows.Scan(dest...); e != nil {
					rows.Close()
					return e
				}
				raw, e := json.Marshal(values)
				if e != nil {
					rows.Close()
					return e
				}
				sum.Write(raw)
				sum.Write([]byte{'\n'})
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		g.t.Fatal("complete actual retention graph fingerprint", e)
	}
	return hex.EncodeToString(sum.Sum(nil))
}
func (g *retentionFixture) snapshot() string {
	var projections []retentionProjection
	for _, name := range []string{"tenants", "workspaces", "users", "installation_state", "invitations", "password_reset_tokens", "mail_deliveries", "sessions", "rate_limit_buckets", "idempotency_records", "audit_events"} {
		projections = append(projections, retentionProjection{table: name})
	}
	return g.digest(projections)
}
func (g *retentionFixture) invite(tenant, workspace, status string, created, updated, expires time.Time, version int64) string {
	id := uuid.NewString()
	var token []byte
	var accepted, acceptedAt, revoked any
	if status == "pending" {
		token = retentionBytes()
	}
	if status == "accepted" {
		accepted = g.f.User
		acceptedAt = updated
	}
	if status == "revoked" {
		revoked = updated
	}
	g.exec(`INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,version,created_by,accepted_user_id,created_at,updated_at,expires_at,accepted_at,revoked_at) VALUES($1,$2,$3,$4,'en',$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, `INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,version,created_by,accepted_user_id,created_at,updated_at,expires_at,accepted_at,revoked_at) VALUES(?,?,?,?,'en',?,?,?,?,?,?,?,?,?,?)`, id, tenant, workspace, id+"@example.invalid", status, token, version, g.f.User, accepted, created, updated, expires, acceptedAt, revoked)
	return id
}
func (g *retentionFixture) reset(created, expires time.Time, revoked any) string {
	id := uuid.NewString()
	var token []byte
	if revoked == nil {
		token = retentionBytes()
	}
	g.exec(`INSERT INTO password_reset_tokens(id,user_id,audience,token_hash,created_at,expires_at,revoked_at) VALUES($1,$2,'portal',$3,$4,$5,$6)`, `INSERT INTO password_reset_tokens(id,user_id,audience,token_hash,created_at,expires_at,revoked_at) VALUES(?,?,'portal',?,?,?,?)`, id, g.f.User, token, created, expires, revoked)
	return id
}
func (g *retentionFixture) mail(purpose, reference, state string, created time.Time, completed any) string {
	id := uuid.NewString()
	var invitation, generation, reset, user, key, nonce, cipher, lease, locked any
	switch purpose {
	case "invitation":
		invitation = reference
		generation = 1
	case "password_reset":
		reset = reference
		user = g.f.User
	case "password_changed":
		user = g.f.User
	}
	if state == "queued" || state == "sending" {
		key = "explicit-retention-envelope"
		nonce = make([]byte, 12)
		cipher = []byte("explicit encrypted payload envelope, no delivery proof")
	}
	if state == "sending" {
		lease = uuid.NewString()
		locked = g.now.Add(time.Minute)
	}
	g.exec(`INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,invitation_id,invitation_generation,reset_token_id,user_id,state,payload_key_id,payload_nonce,payload_ciphertext,next_attempt_at,lease_token,locked_until,message_id,created_at,updated_at,completed_at) VALUES($1,$2,'retention@example.invalid','en',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, `INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,invitation_id,invitation_generation,reset_token_id,user_id,state,payload_key_id,payload_nonce,payload_ciphertext,next_attempt_at,lease_token,locked_until,message_id,created_at,updated_at,completed_at) VALUES(?,?,'retention@example.invalid','en',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, purpose, invitation, generation, reset, user, state, key, nonce, cipher, created, lease, locked, id+"@example.invalid", created, created, completed)
	return id
}
func (g *retentionFixture) session(created, last, expires time.Time, revoked any) string {
	id := uuid.NewString()
	var reason any
	if revoked != nil {
		reason = "retention_envelope"
	}
	g.exec(`INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at,revoked_at,revoke_reason) VALUES($1,$2,'portal',$3,1,$4,$5,$6,$7,$8)`, `INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at,revoked_at,revoke_reason) VALUES(?,?,'portal',?,1,?,?,?,?,?)`, id, g.f.User, retentionBytes(), created, last, expires, revoked, reason)
	return id
}
func (g *retentionFixture) bucket(created, expires time.Time) []byte {
	key := retentionBytes()
	g.exec(`INSERT INTO rate_limit_buckets(bucket_key,window_started_at,expires_at,attempt_count) VALUES($1,$2,$3,1)`, `INSERT INTO rate_limit_buckets(bucket_key,window_started_at,expires_at,attempt_count) VALUES(?,?,?,1)`, key, created, expires)
	return key
}
func (g *retentionFixture) idempotencyTuple(key, operation string, created, expires time.Time) {
	g.exec(`INSERT INTO idempotency_records(actor_user_id,operation_id,idempotency_key,request_hash,response_status,response_body,created_at,expires_at) VALUES($1,$2,$3,$4,201,'{}'::jsonb,$5,$6)`, `INSERT INTO idempotency_records(actor_user_id,operation_id,idempotency_key,request_hash,response_status,response_body,created_at,expires_at) VALUES(?,?,?,?,201,CAST('{}' AS JSON),?,?)`, g.f.User, operation, key, retentionBytes(), created, expires)
}
func (g *retentionFixture) idempotency(created, expires time.Time) string {
	key := uuid.NewString()
	g.idempotencyTuple(key, "createInvitation", created, expires)
	return key
}
func (g *retentionFixture) audit(tenant, workspace any, occurred time.Time) string {
	id := uuid.NewString()
	g.exec(`INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,request_id) VALUES($1,$2,$3,$4,'system','retention.fixture','success',$5)`, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,request_id) VALUES(?,?,?,?,'system','retention.fixture','success',?)`, id, occurred, tenant, workspace, uuid.NewString())
	return id
}
func (g *retentionFixture) seed(n int) retentionRows {
	var r retentionRows
	now := g.now
	p := g.policy
	for i := 0; i < n; i++ {
		inv := g.invite(g.f.Tenant, g.workspace, "pending", now.Add(-3*time.Hour), now.Add(-3*time.Hour), now.Add(-time.Hour+time.Duration(i)*time.Minute), 1)
		r.expire = append(r.expire, inv)
		reset := g.reset(now.Add(-3*time.Hour), now.Add(-time.Hour), nil)
		r.reset = append(r.reset, reset)
		r.cancel = append(r.cancel, g.mail("invitation", inv, "sending", now.Add(-2*time.Hour), nil), g.mail("password_reset", reset, "queued", now.Add(-2*time.Hour), nil))
		oldReset := g.reset(now.AddDate(0, 0, -p.ResetDays-4), now.AddDate(0, 0, -p.ResetDays-3), now.AddDate(0, 0, -p.ResetDays-2))
		r.oldReset = append(r.oldReset, oldReset)
		oldInv := g.invite(g.f.Tenant, g.workspace, "revoked", now.AddDate(0, 0, -p.InvitationDays-4), now.AddDate(0, 0, -p.InvitationDays-2), now.AddDate(0, 0, -p.InvitationDays-1), 1)
		r.oldInvite = append(r.oldInvite, oldInv)
		purpose, reference := "password_reset", oldReset
		if i%2 != 0 {
			purpose, reference = "invitation", oldInv
		}
		r.terminal = append(r.terminal, g.mail(purpose, reference, "failed", now.AddDate(0, 0, -p.MailDays-4), now.AddDate(0, 0, -p.MailDays-2)))
		created := now.AddDate(0, 0, -p.SessionDays-5)
		r.session = append(r.session, g.session(created, created.Add(time.Hour), created.Add(24*time.Hour), now.AddDate(0, 0, -p.SessionDays-2)))
		r.rate = append(r.rate, g.bucket(now.Add(-config.Seconds(p.RateGraceSeconds)-2*time.Hour), now.Add(-config.Seconds(p.RateGraceSeconds)-time.Hour)))
		key := g.idempotency(now.Add(-3*time.Hour), now.Add(-time.Hour))
		r.idem = append(r.idem, key)
		g.idempotencyTuple(key, "resendInvitation", now.Add(-time.Hour), now.Add(time.Hour))
		g.protected = append(g.protected, retentionProjection{"idempotency_records", "actor_user_id=$1 AND operation_id=$2 AND idempotency_key=$3", "actor_user_id=? AND operation_id=? AND idempotency_key=?", []any{g.f.User, "resendInvitation", key}})
		var tenant, workspace any
		if i%2 != 0 {
			tenant = g.f.Tenant
			workspace = g.workspace
		}
		r.audit = append(r.audit, g.audit(tenant, workspace, now.AddDate(0, 0, -p.AuditDays-2)))
	}
	accepted := g.invite(g.f.Tenant, g.workspace, "accepted", now.AddDate(0, 0, -p.InvitationDays-10), now.AddDate(0, 0, -p.InvitationDays-9), now.AddDate(0, 0, -p.InvitationDays-8), 1)
	g.protect("invitations", accepted)
	liveInv := g.invite(g.f.Tenant, g.workspace, "pending", now.Add(-time.Hour), now.Add(-time.Hour), now.Add(24*time.Hour), 1)
	g.protect("invitations", liveInv)
	liveReset := g.reset(now.Add(-time.Hour), now.Add(24*time.Hour), nil)
	g.protect("password_reset_tokens", liveReset)
	for _, id := range []string{g.mail("invitation", liveInv, "queued", now, nil), g.mail("password_reset", liveReset, "sending", now, nil), g.mail("password_changed", "", "queued", now, nil)} {
		g.protect("mail_deliveries", id)
	}
	g.protect("sessions", g.session(now.Add(-time.Hour), now.Add(-time.Minute), now.Add(24*time.Hour), nil))
	g.protect("rate_limit_buckets", g.bucket(now.Add(-time.Hour), now.Add(time.Hour)))
	g.protect("idempotency_records", g.idempotency(now.Add(-time.Hour), now.Add(time.Hour)))
	return r
}
func (g *retentionFixture) remaining(table string, ids []string, condition string) int {
	total := 0
	for _, id := range ids {
		if table == "idempotency_records" {
			total += g.count(`SELECT COUNT(*) FROM idempotency_records WHERE actor_user_id=$1 AND operation_id='createInvitation' AND idempotency_key=$2`, `SELECT COUNT(*) FROM idempotency_records WHERE actor_user_id=? AND operation_id='createInvitation' AND idempotency_key=?`, g.f.User, id)
		} else {
			pg, my := "SELECT COUNT(*) FROM "+table+" WHERE id=$1", "SELECT COUNT(*) FROM "+table+" WHERE id=?"
			if condition != "" {
				pg += " AND " + condition
				my += " AND " + condition
			}
			total += g.count(pg, my, id)
		}
	}
	return total
}
func (g *retentionFixture) progress(r retentionRows) []int {
	rates := 0
	for _, key := range r.rate {
		rates += g.count(`SELECT COUNT(*) FROM rate_limit_buckets WHERE bucket_key=$1`, `SELECT COUNT(*) FROM rate_limit_buckets WHERE bucket_key=?`, key)
	}
	return []int{
		g.remaining("invitations", r.expire, "status='expired' AND token_hash IS NULL AND version=2"),
		g.remaining("password_reset_tokens", r.reset, "revoked_at IS NOT NULL AND token_hash IS NULL"),
		g.remaining("mail_deliveries", r.cancel, "state='cancelled' AND payload_key_id IS NULL AND payload_nonce IS NULL AND payload_ciphertext IS NULL AND lease_token IS NULL AND locked_until IS NULL AND completed_at IS NOT NULL AND error_category='token_obsolete'"),
		len(r.terminal) - g.remaining("mail_deliveries", r.terminal, ""), len(r.oldReset) - g.remaining("password_reset_tokens", r.oldReset, ""), len(r.oldInvite) - g.remaining("invitations", r.oldInvite, ""),
		len(r.session) - g.remaining("sessions", r.session, ""), len(r.rate) - rates, len(r.idem) - g.remaining("idempotency_records", r.idem, ""), len(r.audit) - g.remaining("audit_events", r.audit, "")}
}
func retentionComplete(g *retentionFixture, r retentionRows) {
	g.t.Helper()
	want := []int{len(r.expire), len(r.reset), len(r.cancel), len(r.terminal), len(r.oldReset), len(r.oldInvite), len(r.session), len(r.rate), len(r.idem), len(r.audit)}
	if got := g.progress(r); fmt.Sprint(got) != fmt.Sprint(want) {
		g.t.Fatal("ten actual owner results incomplete", got, want)
	}
}
func (g *retentionFixture) auditCount(action string) int {
	return g.count(`SELECT COUNT(*) FROM audit_events WHERE action=$1`, `SELECT COUNT(*) FROM audit_events WHERE action=?`, action)
}

// Instruments never invent a row/affected count/COMMIT. Audit injection executes
// the real owning audit first, then an actual constraint-rejected INSERT in that
// same transaction. ACK loss is added only after actual Store.Within committed.
type retentionFaultStore struct {
	transaction.Store
	g                        *retentionFixture
	unknown, auditFailure    bool
	dialect                  transaction.Dialect
	calls, commits, rejected atomic.Int64
	readAttempt              chan struct{}
	readOnce                 sync.Once
}

func (s *retentionFaultStore) Dialect() transaction.Dialect {
	if s.dialect != "" {
		return s.dialect
	}
	return s.Store.Dialect()
}
func (s *retentionFaultStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls.Add(1)
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		baseline := 0
		if s.auditFailure {
			if e := q.QueryRow(ctx, s.g.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE action='system.retention'`, `SELECT COUNT(*) FROM audit_events WHERE action='system.retention'`)).Scan(&baseline); e != nil {
				return e
			}
		}
		return fn(ctx, &retentionFaultExecutor{Executor: q, owner: s, baseline: baseline})
	})
	if e != nil {
		return e
	}
	s.commits.Add(1)
	if s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}

type retentionFaultExecutor struct {
	transaction.Executor
	owner    *retentionFaultStore
	baseline int
}

func (q *retentionFaultExecutor) QueryRow(ctx context.Context, s transaction.Statement) transaction.Row {
	if q.owner.readAttempt != nil {
		q.owner.readOnce.Do(func() { close(q.owner.readAttempt) })
	}
	return q.Executor.QueryRow(ctx, s)
}
func (q *retentionFaultExecutor) Exec(ctx context.Context, s transaction.Statement) (int64, error) {
	count, e := q.Executor.Exec(ctx, s)
	if e != nil || !q.owner.auditFailure {
		return count, e
	}
	var actual int
	if e = q.Executor.QueryRow(ctx, q.owner.g.f.Statement(`SELECT COUNT(*) FROM audit_events WHERE action='system.retention'`, `SELECT COUNT(*) FROM audit_events WHERE action='system.retention'`)).Scan(&actual); e != nil {
		return count, e
	}
	if actual > q.baseline {
		_, e = q.Executor.Exec(ctx, q.owner.g.f.Statement(`INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id,metadata) VALUES($1,$2,'system','retention.fixture.rejected','failure',$3,'[]'::jsonb)`, `INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id,metadata) VALUES(?,?,'system','retention.fixture.rejected','failure',?,CAST('[]' AS JSON))`, uuid.NewString(), q.owner.g.now, uuid.NewString()))
		if e == nil {
			return count, errors.New("RETENTION_TEST_REAL_AUDIT_CONSTRAINT_MISSING")
		}
		q.owner.rejected.Add(1)
		return count, e
	}
	return count, nil
}
func retentionHeldLock(g *retentionFixture, pg, my string, args ...any) func() {
	g.t.Helper()
	ctx, cancel := context.WithCancel(g.ctx)
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var result error
	var once sync.Once
	go func() {
		defer close(done)
		result = g.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var id string
			if e := q.QueryRow(ctx, g.f.Statement(pg, my, args...)).Scan(&id); e != nil {
				return e
			}
			close(ready)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	unlock := func() {
		once.Do(func() { close(release) })
		select {
		case <-done:
		case <-g.ctx.Done():
			cancel()
			<-done
		}
		cancel()
		if result != nil {
			g.t.Error("actual retention lock holder failed", result)
		}
	}
	g.t.Cleanup(unlock)
	select {
	case <-ready:
		return unlock
	case <-done:
		g.t.Fatal("actual retention holder never acquired row", result)
	case <-g.ctx.Done():
		g.t.Fatal("actual retention holder deadline")
	}
	return unlock
}
func retentionAwait(t *testing.T, ctx context.Context, done <-chan error) error {
	t.Helper()
	select {
	case e := <-done:
		return e
	case <-ctx.Done():
		t.Fatal("retention worker did not join before actual test deadline")
		return ctx.Err()
	}
}
func TestActualPairedRetentionRuntime(t *testing.T) {
	t.Run("all_ten_owners_real_ciphertext_null_binary_composite_and_audit", func(t *testing.T) {
		g := newRetentionFixture(t, true)
		r := g.seed(1)
		protected := g.digest(g.protected)
		before := g.auditCount("system.retention")
		g.pass()
		retentionComplete(g, r)
		if g.digest(g.protected) != protected || g.auditCount("system.retention") != before+1 || g.auditCount("invitation.expired") != 1 || g.auditCount("mail.cancelled") != 2 {
			t.Fatal("actual authority, exact audit delta or preserved live tuple changed")
		}
		stable := g.snapshot()
		g.pass()
		if g.snapshot() != stable {
			t.Fatal("quiescent repeat changed data or emitted duplicate audit")
		}
	})
	t.Run("batch_one_three_rows_each_owner_and_repeat_convergence", func(t *testing.T) {
		g := newRetentionFixture(t, true)
		g.policy.BatchSize = 1
		g.policy.MailDays = 2
		g.policy.ResetDays = 2
		g.policy.InvitationDays = 2
		g.policy.SessionDays = 2
		g.policy.AuditDays = 2
		g.policy.RateGraceSeconds = 1
		r := g.seed(3)
		protected := g.digest(g.protected)
		last := g.progress(r)
		passes := 0
		for ; passes < 8; passes++ {
			before := g.snapshot()
			audits := g.auditCount("system.retention")
			g.pass()
			next := g.progress(r)
			for owner, n := range next {
				if n-last[owner] < 0 || n-last[owner] > 1 {
					t.Fatal("actual independent owner batch1 bound violated", owner, last, next)
				}
			}
			after := g.snapshot()
			if before == after {
				break
			}
			if g.auditCount("system.retention") != audits+1 {
				t.Fatal("one owning audit per changed pass required")
			}
			last = next
		}
		if passes < 3 || passes >= 8 {
			t.Fatal("actual batch1 did not converge within finite fixture bound", passes)
		}
		retentionComplete(g, r)
		if g.digest(g.protected) != protected {
			t.Fatal("live/accepted or same-key different-operation tuple was purged")
		}
		stable := g.snapshot()
		g.pass()
		if stable != g.snapshot() {
			t.Fatal("converged actual graph changed")
		}
	})
	t.Run("actual_system_audit_constraint_failure_rolls_back_every_owner", func(t *testing.T) {
		g := newRetentionFixture(t, true)
		g.seed(1)
		before := g.snapshot()
		instrument := &retentionFaultStore{Store: g.f.Store, g: g, auditFailure: true}
		e := PassSelected(g.ctx, instrument, g.policy)
		var classified *transaction.Error
		if e == nil || !errors.As(e, &classified) || classified.Kind != transaction.CheckConflict || instrument.calls.Load() != 1 || instrument.commits.Load() != 0 || instrument.rejected.Load() != 1 {
			t.Fatal("actual rejected audit did not propagate and roll back", e)
		}
		if g.snapshot() != before {
			t.Fatal("actual ten-owner/audit transaction partially committed")
		}
		g.pass()
	})
	t.Run("committed_ack_unknown_readback_and_no_duplicate_replay", func(t *testing.T) {
		g := newRetentionFixture(t, true)
		r := g.seed(1)
		baseline := g.auditCount("system.retention")
		instrument := &retentionFaultStore{Store: g.f.Store, g: g, unknown: true}
		e := PassSelected(g.ctx, instrument, g.policy)
		var classified *transaction.Error
		if e == nil || !errors.As(e, &classified) || classified.Kind != transaction.CommitUnknown || instrument.calls.Load() != 1 || instrument.commits.Load() != 1 {
			t.Fatal("actual committed ACK loss was retried or hidden", e)
		}
		retentionComplete(g, r)
		if g.auditCount("system.retention") != baseline+1 {
			t.Fatal("committed pass lacked actual audit")
		}
		committed := g.snapshot()
		g.pass()
		if committed != g.snapshot() {
			t.Fatal("later independent pass duplicated committed audit or mutation")
		}
	})
	t.Run("installed_tenant_real_workspace_and_foreign_fact_preservation", func(t *testing.T) {
		g := newRetentionFixture(t, true)
		r := g.seed(1)
		foreign, workspace := uuid.NewString(), uuid.NewString()
		g.exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Foreign actual tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign actual tenant','active')`, foreign)
		g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign actual Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign actual Workspace','active')`, workspace, foreign)
		inv := g.invite(foreign, workspace, "pending", g.now.Add(-3*time.Hour), g.now.Add(-3*time.Hour), g.now.Add(-time.Hour), 1)
		active := g.mail("invitation", inv, "sending", g.now.Add(-2*time.Hour), nil)
		old := g.mail("invitation", inv, "failed", g.now.AddDate(0, 0, -g.policy.MailDays-4), g.now.AddDate(0, 0, -g.policy.MailDays-2))
		foreignAudit := g.audit(foreign, workspace, g.now.AddDate(0, 0, -g.policy.AuditDays-2))
		installedAudit := g.audit(g.f.Tenant, g.workspace, g.now.AddDate(0, 0, -g.policy.AuditDays-2))
		g.protect("invitations", inv)
		g.protect("mail_deliveries", active)
		g.protect("mail_deliveries", old)
		g.protect("audit_events", foreignAudit)
		protected := g.digest(g.protected)
		g.pass()
		retentionComplete(g, r)
		if g.digest(g.protected) != protected || g.remaining("audit_events", []string{installedAudit}, "") != 0 {
			t.Fatal("foreign facts changed or installed audit retained")
		}
		for _, binding := range []struct{ action, target, workspace string }{{"invitation.expired", r.expire[0], g.workspace}, {"mail.cancelled", r.cancel[0], g.workspace}, {"mail.cancelled", r.cancel[1], g.f.Workspace}} {
			if g.count(`SELECT COUNT(*) FROM audit_events WHERE action=$1 AND target_id=$2 AND tenant_id=$3 AND workspace_id=$4`, `SELECT COUNT(*) FROM audit_events WHERE action=? AND target_id=? AND tenant_id=? AND workspace_id=?`, binding.action, binding.target, g.f.Tenant, binding.workspace) != 1 {
				t.Fatal("actual changed row audit Workspace provenance mismatched")
			}
		}
	})
	t.Run("actual_skip_locked_and_two_concurrent_passes", func(t *testing.T) {
		g := newRetentionFixture(t, true)
		g.policy.BatchSize = 1
		var ids []string
		for n := 0; n < 3; n++ {
			ids = append(ids, g.invite(g.f.Tenant, g.workspace, "pending", g.now.Add(-3*time.Hour), g.now.Add(-3*time.Hour), g.now.Add(-time.Hour+time.Duration(n)*time.Minute), 1))
		}
		unlock := retentionHeldLock(g, `SELECT id::text FROM invitations WHERE id=$1 FOR UPDATE`, `SELECT id FROM invitations WHERE id=? FOR UPDATE`, ids[0])
		g.pass()
		if g.remaining("invitations", []string{ids[0]}, "status='pending' AND version=1 AND token_hash IS NOT NULL") != 1 || g.remaining("invitations", ids, "status='expired'") != 1 {
			t.Fatal("actual locked key was mutated or SKIP LOCKED made no progress")
		}
		unlock()
		start := make(chan struct{})
		done := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for n := 0; n < 2; n++ {
			go func() { ready.Done(); <-start; done <- PassSelected(g.ctx, g.f.Store, g.policy) }()
		}
		ready.Wait()
		close(start)
		var concurrentErrors []error
		for n := 0; n < 2; n++ {
			concurrentErrors = append(concurrentErrors, retentionAwait(t, g.ctx, done))
		}
		for _, e := range concurrentErrors {
			if e != nil {
				t.Fatal("actual concurrent selected pass", e)
			}
		}
		// InnoDB may lock examined rows before sorting/limiting a locking read.
		// A competing SKIP LOCKED pass may therefore defer an eligible row.
		// Each actual transition must still have exactly one owning audit, and
		// an independently selected later pass must converge without duplicates.
		completed := g.remaining("invitations", ids, "status='expired' AND version=2 AND token_hash IS NULL")
		t.Logf("actual concurrent completion: invitations=%d invitation_audits=%d system_audits=%d", completed, g.auditCount("invitation.expired"), g.auditCount("system.retention"))
		if completed < 2 || completed > 3 || g.auditCount("invitation.expired") != completed || g.auditCount("system.retention") != completed {
			t.Fatal("actual concurrent passes lost/doubled a transition or audit")
		}
		if completed < 3 {
			g.pass()
		}
		if g.remaining("invitations", ids, "status='expired' AND version=2 AND token_hash IS NULL") != 3 || g.auditCount("invitation.expired") != 3 || g.auditCount("system.retention") != 3 {
			t.Fatal("actual deferred lock cleanup failed to converge exactly once")
		}
		stable := g.snapshot()
		g.pass()
		if stable != g.snapshot() {
			t.Fatal("concurrent result not quiescent")
		}
	})
	t.Run("invalid_prebootstrap_missing_and_inactive_installation", func(t *testing.T) {
		g := newRetentionFixture(t, false)
		// Real preauthentication history can exist before onboarding. It must remain
		// untouched while the authoritative installation is explicitly unbootstrapped.
		g.bucket(g.now.Add(-config.Seconds(g.policy.RateGraceSeconds)-2*time.Hour), g.now.Add(-config.Seconds(g.policy.RateGraceSeconds)-time.Hour))
		g.audit(nil, nil, g.now.AddDate(0, 0, -g.policy.AuditDays-2))
		before := g.snapshot()
		instrument := &retentionFaultStore{Store: g.f.Store, g: g}
		invalid := g.policy
		invalid.BatchSize = 0
		for _, e := range []error{PassSelected(nil, instrument, g.policy), PassSelected(g.ctx, nil, g.policy), PassSelected(g.ctx, instrument, invalid), RunSelected(nil, instrument, g.policy), RunSelected(g.ctx, nil, g.policy), RunSelected(g.ctx, instrument, invalid)} {
			if e == nil {
				t.Fatal("invalid retention admission was accepted")
			}
		}
		unsupported := &retentionFaultStore{Store: g.f.Store, g: g, dialect: transaction.Dialect("unowned")}
		if PassSelected(g.ctx, unsupported, g.policy) == nil || RunSelected(g.ctx, unsupported, g.policy) == nil || instrument.calls.Load() != 0 || unsupported.calls.Load() != 0 || before != g.snapshot() {
			t.Fatal("invalid retention admission touched actual store")
		}
		g.pass()
		if g.snapshot() != before || g.auditCount("system.retention") != 0 {
			t.Fatal("real prebootstrap installation mutated or audited")
		}
		// The actual CHECK prevents malformed stored bootstrap combinations. Do not
		// manufacture an impossible row or disable constraints to fake this branch.
		_, e := g.f.Store.Exec(g.ctx, g.f.Statement(`UPDATE installation_state SET bootstrap_complete=true WHERE id=1`, `UPDATE installation_state SET bootstrap_complete=true WHERE id=1`))
		var check *transaction.Error
		if e == nil || !errors.As(e, &check) || check.Kind != transaction.CheckConflict || before != g.snapshot() {
			t.Fatal("actual malformed installation was not rejected atomically", e)
		}
		g.exec(`DELETE FROM installation_state WHERE id=1`, `DELETE FROM installation_state WHERE id=1`)
		missing := g.snapshot()
		e = PassSelected(g.ctx, g.f.Store, g.policy)
		if e == nil || !errors.As(e, &check) || check.Kind != transaction.NoRows || g.snapshot() != missing {
			t.Fatal("absent installation silently treated as prebootstrap", e)
		}
		g.exec(`INSERT INTO installation_state(id) VALUES(1)`, `INSERT INTO installation_state(id) VALUES(1)`)
		g.exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Retention installed tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Retention installed tenant','active')`, g.f.Tenant)
		g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Retention installed Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Retention installed Workspace','active')`, g.f.Workspace, g.f.Tenant)
		email := g.f.User + "@example.invalid"
		g.exec(`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Retention installed actor','en','active',true)`, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Retention installed actor','en','active',true)`, g.f.User, email, email)
		g.exec(`UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=$4 WHERE id=1`, `UPDATE installation_state SET bootstrap_complete=true,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=? WHERE id=1`, g.f.Tenant, g.f.Workspace, g.f.User, g.now)
		g.exec(`UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, g.f.Tenant)
		inactive := g.snapshot()
		if e = PassSelected(g.ctx, g.f.Store, g.policy); e == nil || !errors.As(e, &check) || check.Kind != transaction.NoRows || g.snapshot() != inactive {
			t.Fatal("inactive installed tenant was adopted", e)
		}
	})
	t.Run("run_selected_cancel_active_pass_error_and_join", func(t *testing.T) {
		g := newRetentionFixture(t, true)
		g.seed(1)
		before := g.snapshot()
		cancelled, cancel := context.WithCancel(g.ctx)
		cancel()
		if e := PassSelected(cancelled, g.f.Store, g.policy); !errors.Is(e, context.Canceled) {
			t.Fatal("actual cancelled pass swallowed", e)
		}
		expired, stop := context.WithDeadline(g.ctx, g.now.Add(-time.Hour))
		defer stop()
		if e := PassSelected(expired, g.f.Store, g.policy); !errors.Is(e, context.DeadlineExceeded) {
			t.Fatal("actual expired pass swallowed", e)
		}
		if e := RunSelected(cancelled, g.f.Store, g.policy); e != nil || before != g.snapshot() {
			t.Fatal("normal cancelled lifecycle changed data", e)
		}
		policy := g.policy
		policy.IntervalSeconds = 1
		g.exec(`UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, g.f.Tenant)
		failedCtx, failedCancel := context.WithCancel(g.ctx)
		defer failedCancel()
		failure := make(chan error, 1)
		failureDone := make(chan struct{})
		go func() { defer close(failureDone); failure <- RunSelected(failedCtx, g.f.Store, policy) }()
		t.Cleanup(func() {
			failedCancel()
			cleanup, stop := context.WithTimeout(context.WithoutCancel(g.ctx), 8*time.Second)
			defer stop()
			select {
			case <-failureDone:
			case <-cleanup.Done():
				t.Error("actual failed retention worker did not join")
			}
		})
		bound, boundCancel := context.WithTimeout(g.ctx, 8*time.Second)
		e := retentionAwait(t, bound, failure)
		boundCancel()
		var classified *transaction.Error
		if e == nil || !errors.As(e, &classified) || classified.Kind != transaction.NoRows {
			t.Fatal("actual maintenance failure was swallowed", e)
		}
		g.exec(`UPDATE tenants SET status='active' WHERE id=$1`, `UPDATE tenants SET status='active' WHERE id=?`, g.f.Tenant)
		unlock := retentionHeldLock(g, `SELECT id::text FROM installation_state WHERE id=1 FOR UPDATE`, `SELECT CAST(id AS CHAR) FROM installation_state WHERE id=1 FOR UPDATE`)
		ctx, cancelRun := context.WithCancel(g.ctx)
		instrument := &retentionFaultStore{Store: g.f.Store, g: g, readAttempt: make(chan struct{})}
		done := make(chan struct{})
		var runError error
		go func() { defer close(done); runError = RunSelected(ctx, instrument, policy) }()
		t.Cleanup(func() {
			cancelRun()
			cleanup, stop := context.WithTimeout(context.WithoutCancel(g.ctx), 8*time.Second)
			defer stop()
			select {
			case <-done:
			case <-cleanup.Done():
				t.Error("cancelled actual retention run remained unjoined")
			}
		})
		select {
		case <-instrument.readAttempt:
		case <-done:
			t.Fatal("actual run exited before attempting owning installation lock", runError)
		case <-g.ctx.Done():
			t.Fatal("actual run never selected its pass")
		}
		cancelRun()
		select {
		case <-done:
		case <-g.ctx.Done():
			t.Fatal("active retention cancellation never joined")
		}
		unlock()
		if runError != nil || instrument.calls.Load() != 1 || instrument.commits.Load() != 0 || before != g.snapshot() {
			t.Fatal("active cancellation committed, escaped join or returned wrong error", runError)
		}
	})
	t.Run("invitation_version_overflow_rolls_back_without_reactivation", func(t *testing.T) {
		const maximumVersion int64 = 9007199254740991
		g := newRetentionFixture(t, true)
		g.seed(1)
		id := g.invite(g.f.Tenant, g.workspace, "pending", g.now.Add(-5*time.Hour), g.now.Add(-5*time.Hour), g.now.Add(-2*time.Hour), maximumVersion)
		before := g.snapshot()
		instrument := &retentionFaultStore{Store: g.f.Store, g: g}
		e := PassSelected(g.ctx, instrument, g.policy)
		var classified *transaction.Error
		if e == nil || !errors.As(e, &classified) || classified.Kind != transaction.CheckConflict || instrument.calls.Load() != 1 || instrument.commits.Load() != 0 || before != g.snapshot() {
			t.Fatal("actual invitation overflow did not roll back owning pass", e)
		}
		if g.count(`SELECT COUNT(*) FROM invitations WHERE id=$1 AND status='pending' AND token_hash IS NOT NULL AND version=$2`, `SELECT COUNT(*) FROM invitations WHERE id=? AND status='pending' AND token_hash IS NOT NULL AND version=?`, id, maximumVersion) != 1 {
			t.Fatal("overflow reactivated/cleared original token")
		}
	})
}
