//go:build repository_integration

package boundary

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ownsolid-iam106-source-inspection/wirefault"

	"github.com/jackc/pgx/v5"
)

func auditRefusal(t *testing.T, f *protocolFixture) func() {
	t.Helper()
	f.exec(t, `CREATE FUNCTION refuse_code_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='code_redeemed' THEN RAISE EXCEPTION 'PRIVATE_AUDIT_REFUSAL'; END IF; RETURN NEW; END; $$; CREATE TRIGGER refuse_code_event BEFORE INSERT ON op_fixture_events FOR EACH ROW EXECUTE FUNCTION refuse_code_event()`)
	return func() {
		f.exec(t, `DROP TRIGGER refuse_code_event ON op_fixture_events; DROP FUNCTION refuse_code_event()`)
	}
}
func TestLiveOPAtomicSigningAndAuditFailure(t *testing.T) {
	for _, mode := range []string{"signing", "audit"} {
		t.Run(mode, func(t *testing.T) {
			f := newProtocolFixture(t, nil)
			trace := f.issueCode(t, nil)
			var restore func()
			if mode == "signing" {
				backup := filepath.Join(f.folder, "owned-signing-backup.pem")
				if os.Rename(f.config.SigningFile, backup) != nil {
					t.Fatal("owned signing fault not installed")
				}
				restored := false
				restore = func() {
					if !restored {
						if os.Rename(backup, f.config.SigningFile) != nil {
							t.Fatal("owned signing key restore failed")
						}
						restored = true
					}
				}
				defer restore()
			} else {
				restore = auditRefusal(t, f)
			}
			status, fields := f.exchange(t, trace, f.secret, trace.Verifier, f.rpOrigin+"/callback", f.client)
			if status != 500 {
				t.Fatal("atomic signing/audit fault returned wrong class")
			}
			noTokenFields(t, fields)
			if f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != 0 || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE consumed_at IS NOT NULL`) != 0 || f.scalar(t, `SELECT count(*) FROM op_fixture_events WHERE action='code_redeemed'`) != 0 {
				t.Fatal("signing/audit refusal retained partial issuance")
			}
			restore()
			status, fields = f.exchange(t, trace, f.secret, trace.Verifier, f.rpOrigin+"/callback", f.client)
			if status != 200 || fields["access_token"] == nil || fields["id_token"] == nil || f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != 1 || f.scalar(t, `SELECT count(*) FROM op_fixture_events WHERE action='code_redeemed'`) != 1 {
				t.Fatal("explicit corrected issuance failed")
			}
		})
	}
}

type exchangeObservation struct {
	status int
	fields map[string]json.RawMessage
	failed bool
}

func (f *protocolFixture) exchangeRequest(ctx context.Context, trace codeTrace) *http.Request {
	body := url.Values{"grant_type": {"authorization_code"}, "code": {trace.Code}, "redirect_uri": {f.rpOrigin + "/callback"}, "code_verifier": {trace.Verifier}}
	r, _ := http.NewRequestWithContext(ctx, "POST", f.opOrigin+"/oauth/token", strings.NewReader(body.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(url.QueryEscape(f.client), url.QueryEscape(f.secret))
	return r
}
func (f *protocolFixture) observeExchange(ctx context.Context, trace codeTrace) exchangeObservation {
	response, err := f.serverClient.Do(f.exchangeRequest(ctx, trace))
	if err != nil {
		return exchangeObservation{failed: true}
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return exchangeObservation{failed: true}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return exchangeObservation{failed: true}
	}
	return exchangeObservation{status: response.StatusCode, fields: fields}
}
func holdCode(t *testing.T, f *protocolFixture, trace codeTrace) (pgx.Tx, int) {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal("actual code-row blocker unavailable")
	}
	var pid int
	var id string
	if tx.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid) != nil || tx.QueryRow(f.ctx, `SELECT id FROM op_fixture_requests WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, f.config.Tenant, trace.RequestID).Scan(&id) != nil {
		rollback(tx, f.config.Policy.Operation)
		t.Fatal("actual code-row blocker not held")
	}
	return tx, pid
}
func waitCodeWaiters(t *testing.T, f *protocolFixture, pid, minimum int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.scalar(t, `WITH RECURSIVE blocked(pid) AS (SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))) SELECT count(*) FROM blocked`, pid) >= minimum {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("actual PostgreSQL code-row wait not observed")
}
func TestLiveOPConcurrentAndCanceledRedemption(t *testing.T) {
	t.Run("competing_redeemers", func(t *testing.T) {
		f := newProtocolFixture(t, nil)
		trace := f.issueCode(t, nil)
		blocker, pid := holdCode(t, f, trace)
		defer rollback(blocker, f.config.Policy.Operation)
		results := make(chan exchangeObservation, 2)
		for range 2 {
			go func() { results <- f.observeExchange(f.ctx, trace) }()
		}
		waitCodeWaiters(t, f, pid, 2)
		rollback(blocker, f.config.Policy.Operation)
		success, refused := 0, 0
		for range 2 {
			select {
			case result := <-results:
				if result.failed {
					t.Fatal("actual concurrent request transport failed")
				}
				if result.status == 200 {
					success++
				} else if result.status == 400 {
					noTokenFields(t, result.fields)
					refused++
				} else {
					t.Fatal("concurrent request returned wrong outcome")
				}
			case <-time.After(f.config.Policy.Operation):
				t.Fatal("bounded concurrent exchange deadline")
			}
		}
		if success != 1 || refused != 1 || f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != 1 || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE consumed_at IS NOT NULL`) != 1 || f.scalar(t, `SELECT count(*) FROM op_fixture_events WHERE action='code_redeemed'`) != 1 {
			t.Fatal("competing redeemers did not serialize one issuance")
		}
	})
	t.Run("blocked_exchange_cancellation", func(t *testing.T) {
		f := newProtocolFixture(t, nil)
		trace := f.issueCode(t, nil)
		blocker, pid := holdCode(t, f, trace)
		defer rollback(blocker, f.config.Policy.Operation)
		status, fields := f.exchange(t, trace, random(t), trace.Verifier, f.rpOrigin+"/callback", f.client)
		if status != 401 {
			t.Fatal("bad client reached held code row")
		}
		noTokenFields(t, fields)
		ctx, cancel := context.WithCancel(f.ctx)
		defer cancel()
		result := make(chan exchangeObservation, 1)
		go func() { result <- f.observeExchange(ctx, trace) }()
		waitCodeWaiters(t, f, pid, 1)
		cancel()
		select {
		case out := <-result:
			if !out.failed {
				noTokenFields(t, out.fields)
			}
		case <-time.After(f.config.Policy.Operation):
			t.Fatal("blocked HTTPS cancellation did not return")
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if f.scalar(t, `WITH RECURSIVE blocked(pid) AS (SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))) SELECT count(*) FROM blocked`, pid) == 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if f.scalar(t, `WITH RECURSIVE blocked(pid) AS (SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))) SELECT count(*) FROM blocked`, pid) != 0 || f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != 0 || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE consumed_at IS NOT NULL`) != 0 {
			t.Fatal("canceled exchange retained worker or partial issuance")
		}
		rollback(blocker, f.config.Policy.Operation)
		status, fields = f.exchange(t, trace, f.secret, trace.Verifier, f.rpOrigin+"/callback", f.client)
		if status != 200 || fields["access_token"] == nil {
			t.Fatal("explicit exchange after cancellation failed")
		}
	})
}

func TestLiveOPDisableAndIssuanceLockOrder(t *testing.T) {
	t.Run("disable_commits_before_exchange", func(t *testing.T) {
		f := newProtocolFixture(t, nil)
		trace := f.issueCode(t, nil)
		writer, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal("owned disable transaction unavailable")
		}
		defer rollback(writer, f.config.Policy.Operation)
		var pid int
		if writer.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid) != nil {
			t.Fatal("owned disable backend unavailable")
		}
		if _, err = writer.Exec(f.ctx, `UPDATE op_fixture_clients SET enabled=false,revision=revision+1 WHERE tenant_id=$1 AND client_id=$2`, f.config.Tenant, f.client); err != nil {
			t.Fatal("owned disable row not held")
		}
		result := make(chan exchangeObservation, 1)
		go func() { result <- f.observeExchange(f.ctx, trace) }()
		waitCodeWaiters(t, f, pid, 1)
		if writer.Commit(f.ctx) != nil {
			t.Fatal("owned disable commit failed")
		}
		select {
		case out := <-result:
			if out.failed || out.status != 401 {
				t.Fatal("exchange overtook earlier committed disable")
			}
			noTokenFields(t, out.fields)
		case <-time.After(f.config.Policy.Operation):
			t.Fatal("exchange did not drain after disable")
		}
		if f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != 0 || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE consumed_at IS NOT NULL`) != 0 || f.scalar(t, `SELECT count(*) FROM op_fixture_events WHERE action='code_redeemed'`) != 0 {
			t.Fatal("disabled exchange retained issuance")
		}
	})
	t.Run("exchange_commits_before_disable", func(t *testing.T) {
		f := newProtocolFixture(t, nil)
		trace := f.issueCode(t, nil)
		blocker, pid := holdCode(t, f, trace)
		defer rollback(blocker, f.config.Policy.Operation)
		exchange := make(chan exchangeObservation, 1)
		go func() { exchange <- f.observeExchange(f.ctx, trace) }()
		waitCodeWaiters(t, f, pid, 1)
		disabled := make(chan bool, 1)
		go func() {
			result, err := f.pool.Exec(f.ctx, `UPDATE op_fixture_clients SET enabled=false,revision=revision+1 WHERE tenant_id=$1 AND client_id=$2`, f.config.Tenant, f.client)
			disabled <- err == nil && result.RowsAffected() == 1
		}()
		// The writer waits behind the exchange's client fence, which in turn waits
		// on our actual code-row lock. Observe both links before releasing the row.
		waitCodeWaiters(t, f, pid, 2)
		rollback(blocker, f.config.Policy.Operation)
		var issued exchangeObservation
		select {
		case issued = <-exchange:
			if issued.failed || issued.status != 200 {
				t.Fatal("earlier fenced exchange did not commit")
			}
		case <-time.After(f.config.Policy.Operation):
			t.Fatal("fenced exchange did not drain")
		}
		select {
		case ok := <-disabled:
			if !ok {
				t.Fatal("later disable did not commit")
			}
		case <-time.After(f.config.Policy.Operation):
			t.Fatal("later disable did not drain")
		}
		var token string
		if json.Unmarshal(issued.fields["access_token"], &token) != nil || token == "" {
			t.Fatal("earlier exchange token absent")
		}
		f.rejectUserInfo(t, token)
		if f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != 1 || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE consumed_at IS NOT NULL`) != 1 || f.scalar(t, `SELECT count(*) FROM op_fixture_events WHERE action='code_redeemed'`) != 1 {
			t.Fatal("exchange/disable order lost atomic issuance")
		}
	})
}
func TestLiveOPActualCommitLoss(t *testing.T) {
	for _, mode := range []commitfault.Mode{commitfault.DropBeforeCommit, commitfault.DropCommitAcknowledgement} {
		name := "not_forwarded"
		committed := false
		if mode == commitfault.DropCommitAcknowledgement {
			name = "ack_suppressed"
			committed = true
		}
		t.Run(name, func(t *testing.T) {
			f := newProtocolFixture(t, nil)
			trace := f.issueCode(t, nil)
			configuration := f.pool.Config()
			proxy, err := commitfault.NewPlaintextPostgreSQL("127.0.0.1", configuration.ConnConfig.Port)
			if err != nil {
				t.Fatal("actual PostgreSQL forwarder unavailable")
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if proxy.Close(ctx) != nil {
					t.Error("actual wire forwarder did not drain")
				}
			}()
			raw, err := readPrivateFile(f.config.DSNFile, 16384)
			if err != nil {
				t.Fatal("private original database reference unavailable")
			}
			parsed, err := url.Parse(strings.TrimSpace(string(raw)))
			clear(raw)
			if err != nil {
				t.Fatal("private database reference invalid")
			}
			original := f.config.DSNFile
			parsed.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(int(proxy.Port())))
			f.config.DSNFile = filepath.Join(f.folder, "owned-proxy.dsn")
			privateWrite(t, f.config.DSNFile, []byte(parsed.String()))
			f.restart(t)
			if err = proxy.Arm(mode); err != nil {
				t.Fatal("actual commit fault not armed")
			}
			before := f.serverTransport.tokenRequests.Load()
			response := f.request(t, f.browser, "GET", trace.Callback, nil, "")
			fields := responseBody(t, response, 401)
			var refusal map[string]json.RawMessage
			if json.Unmarshal(fields, &refusal) != nil {
				t.Fatal("RP uncertain-exchange error invalid")
			}
			noTokenFields(t, refusal)
			if f.serverTransport.tokenRequests.Load()-before != 1 || proxy.Commits() != 1 {
				t.Fatal("RP or OP automatically repeated uncertain exchange")
			}
			select {
			case observed := <-proxy.Outcomes():
				if observed.Mode != mode || observed.Forwarded != committed || observed.ServerOK != committed || !observed.Suppressed {
					t.Fatal("actual wire outcome not established")
				}
			case <-time.After(f.config.Policy.Operation):
				t.Fatal("actual wire outcome deadline")
			}
			want := 0
			if committed {
				want = 1
			}
			if f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != want || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE consumed_at IS NOT NULL`) != want || f.scalar(t, `SELECT count(*) FROM op_fixture_events WHERE action='code_redeemed'`) != want {
				t.Fatal("independent actual commit outcome was not atomic")
			}
			select {
			case <-f.app.results:
				t.Fatal("uncertain exchange signed RP in")
			default:
			}
			f.config.DSNFile = original
			f.restart(t)
			fresh := f.issueCode(t, nil)
			response = f.request(t, f.browser, "GET", fresh.Callback, nil, "")
			responseBody(t, response, 200)
			result := f.rpResult(t)
			if result.subject != f.subject || fresh.RequestID == trace.RequestID || f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != want+1 {
				t.Fatal("explicit fresh authorization after unknown commit failed")
			}
		})
	}
}
