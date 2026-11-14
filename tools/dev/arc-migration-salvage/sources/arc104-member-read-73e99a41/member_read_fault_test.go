//go:build repository_integration

package cellclusters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/cellclusters"
	cellhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/cellclusters/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type diagnosticTraceKey struct{}
type diagnosticTrace struct {
	t                     *testing.T
	cancelAtCommit        context.CancelFunc
	awaitDeadlineAtCommit bool
}

func (d diagnosticTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	phase := ""
	switch {
	case strings.EqualFold(data.SQL, "commit"):
		phase = "commit"
		if d.cancelAtCommit != nil {
			d.cancelAtCommit()
		}
		if d.awaitDeadlineAtCommit {
			<-ctx.Done()
		}
	case strings.Contains(data.SQL, "FROM cell_cluster_members m JOIN managed_nodes"):
		phase = "member-select"
	case strings.Contains(data.SQL, "FROM users WHERE id="):
		phase = "identity-lock"
	}
	return context.WithValue(ctx, diagnosticTraceKey{}, phase)
}

func (d diagnosticTrace) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	phase, _ := ctx.Value(diagnosticTraceKey{}).(string)
	if phase == "" {
		return
	}
	var vendor *pgconn.PgError
	state := ""
	if errors.As(data.Err, &vendor) {
		state = vendor.Code
	}
	d.t.Logf("pg_phase=%s backend_pid=%d tag=%q sqlstate=%q error_type=%T cancelled=%t deadline=%t",
		phase, conn.PgConn().PID(), data.CommandTag.String(), state, data.Err,
		errors.Is(data.Err, context.Canceled), errors.Is(data.Err, context.DeadlineExceeded))
}

func diagnosticSeed(t *testing.T, x scenario) string {
	t.Helper()
	id := uuid.NewString()
	tx, err := x.account.DB.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if tag, err := tx.Exec(t.Context(), `INSERT INTO cell_clusters
        (id,tenant_id,name,distribution_version,created_by,idempotency_key,request_fingerprint)
        SELECT $1,n.tenant_id,'Member read fixture','diagnostic',$2,$3,$4 FROM managed_nodes n WHERE n.id=$5`,
		id, x.admin.Me.User.ID, uuid.NewString(), bytes.Repeat([]byte{1}, 32), x.node(0)); err != nil || tag.RowsAffected() != 1 {
		t.Fatal("synthetic cluster row", tag.RowsAffected(), err)
	}
	if tag, err := tx.Exec(t.Context(), `INSERT INTO cell_cluster_members
        (cluster_id,tenant_id,workspace_id,node_id,role,node_address,state,requested_by,join_attempts,task_id)
        SELECT $1,n.tenant_id,n.workspace_id,n.id,'server','10.40.0.1','joining',$2,1,$3
        FROM managed_nodes n WHERE n.id=$4`,
		id, x.admin.Me.User.ID, uuid.NewString(), x.node(0)); err != nil || tag.RowsAffected() != 1 {
		t.Fatal("synthetic joining member row", tag.RowsAffected(), err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestARC104MembersCommitDiagnosis(t *testing.T) {
	x := setup(t, 50, 1)
	cluster := diagnosticSeed(t, x)
	var postgresVersion string
	if err := x.account.DB.QueryRow(t.Context(), "SHOW server_version").Scan(&postgresVersion); err != nil {
		t.Fatal(err)
	}
	t.Logf("PostgreSQL %s; fixture=one synthetic joining member", postgresVersion)
	memberSnapshot := func(t *testing.T) string {
		t.Helper()
		var raw string
		if err := x.account.DB.QueryRow(t.Context(), `SELECT to_jsonb(m)::text FROM cell_cluster_members m WHERE cluster_id=$1`, cluster).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	initial := memberSnapshot(t)
	for _, tt := range []struct {
		name, code                                        string
		status                                            int
		lock, shortDeadline, cancelCommit, deadlineCommit bool
		fault                                             commitfault.Mode
	}{
		{name: "healthy", status: 200},
		{name: "lock_timeout_before_commit", status: 503, code: "STORAGE_UNAVAILABLE", lock: true},
		{name: "deadline_before_commit", status: 503, code: "STORAGE_TIMEOUT", lock: true, shortDeadline: true},
		{name: "connection_loss_before_commit_send", status: 409, code: "STORAGE_COMMIT_OUTCOME_UNKNOWN", fault: commitfault.DropBeforeCommit},
		{name: "lost_successful_commit_reply", status: 409, code: "STORAGE_COMMIT_OUTCOME_UNKNOWN", fault: commitfault.DropCommitAcknowledgement},
		{name: "cancel_during_commit_call", status: 409, code: "STORAGE_COMMIT_OUTCOME_UNKNOWN", cancelCommit: true},
		{name: "deadline_during_commit_call", status: 409, code: "STORAGE_COMMIT_OUTCOME_UNKNOWN", deadlineCommit: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := x.account.DB.Config()
			if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 {
				t.Fatal("requires the existing disposable isolated loopback connection")
			}
			cfg.MaxConns = 2
			cfg.ConnConfig.RuntimeParams["lock_timeout"] = "150ms"
			cfg.ConnConfig.RuntimeParams["application_name"] = "arc104-" + tt.name
			if tt.shortDeadline {
				cfg.ConnConfig.RuntimeParams["lock_timeout"] = "0"
			}
			var proxy *commitfault.PlaintextPostgreSQL
			if tt.fault != commitfault.Pass {
				var err error
				proxy, err = commitfault.NewPlaintextPostgreSQL(cfg.ConnConfig.Host, cfg.ConnConfig.Port)
				if err != nil {
					t.Fatal(err)
				}
				cfg.ConnConfig.Port = proxy.Port()
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					if err := proxy.Close(ctx); err != nil {
						t.Error(err)
					}
				})
			}
			requestContext, cancel := context.WithCancel(t.Context())
			defer cancel()
			tracer := diagnosticTrace{t: t, awaitDeadlineAtCommit: tt.deadlineCommit}
			if tt.cancelCommit {
				tracer.cancelAtCommit = cancel
			}
			cfg.ConnConfig.Tracer = tracer
			pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)
			if err = pool.Ping(t.Context()); err != nil {
				t.Fatal(err)
			}
			account := *x.account
			account.DB = pool
			service := *x.s
			service.Identity = &account
			handler := cellhttp.Handler{Service: &service}
			if tt.lock {
				locker, err := x.account.DB.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = locker.Exec(t.Context(), "LOCK TABLE cell_cluster_members IN ACCESS EXCLUSIVE MODE"); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := locker.Rollback(t.Context()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
						t.Error(err)
					}
				}()
			}
			if proxy != nil {
				if err = proxy.Arm(tt.fault); err != nil {
					t.Fatal(err)
				}
			}
			requestID := uuid.NewString()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r = r.WithContext(requestContext)
				if tt.shortDeadline || tt.deadlineCommit {
					ctx, cancel := context.WithTimeout(r.Context(), 300*time.Millisecond)
					defer cancel()
					r = r.WithContext(ctx)
				}
				page, status, err := handler.Handle(r, x.admin, requestID)
				if err != nil {
					httpserver.Problem(w, requestID, err)
					return
				}
				w.WriteHeader(status)
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			client := server.Client()
			client.Timeout = 30 * time.Second
			began := time.Now()
			response, err := client.Get(server.URL + cellhttp.Root + "/" + cluster + "/members")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]json.RawMessage
			if err = json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			t.Logf("https=%d public_code=%s request_id=%s elapsed=%s",
				response.StatusCode, body["code"], requestID, time.Since(began).Round(time.Millisecond))
			if response.StatusCode != tt.status {
				t.Fatalf("wanted %d, observed %s", tt.status, raw)
			}
			if tt.code == "" {
				var page cellclusters.Page[cellclusters.Member]
				if err = json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 || page.Items[0].State != "joining" {
					t.Fatal("expected only joining member", err)
				}
			} else if string(body["code"]) != fmt.Sprintf("%q", tt.code) || body["items"] != nil || body["data"] != nil || string(body["request_id"]) != fmt.Sprintf("%q", requestID) {
				t.Fatalf("unexpected problem body %s", raw)
			}
			if proxy != nil {
				select {
				case got := <-proxy.Outcomes():
					forwarded := tt.fault == commitfault.DropCommitAcknowledgement
					t.Logf("commit_mode=%v forwarded=%t server_ack=%t suppressed=%t commit_count=%d", got.Mode, got.Forwarded, got.ServerOK, got.Suppressed, proxy.Commits())
					if got.Mode != tt.fault || got.Forwarded != forwarded || got.ServerOK != forwarded || !got.Suppressed || proxy.Commits() != 1 {
						t.Fatal("unexpected PostgreSQL wire outcome", got)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("missing PostgreSQL wire outcome")
				}
			}
		})
		if current := memberSnapshot(t); current != initial {
			t.Fatal("member data changed after GET")
		}
	}
}
