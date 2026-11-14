//go:build repository_integration

package sso

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/google/uuid"
)

func directoryFixture(t *testing.T) (sourceRepositoryFixture, *directoryCandidate, identity.Auth, directoryGroups) {
	t.Helper()
	f := catalogRepositoryFixture(t)
	if f.f.PostgreSQL == nil {
		t.Fatal("actual PostgreSQL selected explicitly")
	}
	raw, err := os.ReadFile(os.Getenv("IAM_DIRECTORY_CANDIDATE_SQL_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.f.PostgreSQL.Exec(f.ctx, string(raw)); err != nil {
		t.Fatal("external candidate schema", err)
	}
	raw, err = os.ReadFile(os.Getenv("IAM_DIRECTORY_GRAPH_FILE"))
	if err != nil || len(raw) > 128<<10 {
		t.Fatal("bounded real-directory graph input required")
	}
	var proof struct {
		Objects map[string]struct {
			DN, ID, Revision, Kind string
			Members                []string
		}
		Direct, Effective   map[string][]string
		ObservedFrom, Until time.Time
	}
	if json.Unmarshal(raw, &proof) != nil || len(proof.Effective) != 7 || len(proof.Objects) != 9 || !proof.ObservedFrom.Before(proof.Until) {
		t.Fatal("actual nested-directory export mismatch")
	}
	groups := directoryGroups{}
	for id, members := range proof.Effective {
		if proof.Objects[id].Kind != "group" {
			t.Fatal("non-group proof")
		}
		groups[id] = slices.Clone(members)
		for _, user := range members {
			if proof.Objects[user].Kind != "user" {
				t.Fatal("non-user closure proof")
			}
		}
	}
	d := &directoryCandidate{s: f.s, RunSeconds: 20, FreshSeconds: 60, MaxGroups: 20, MaxMemberships: 100}
	return f, d, catalogAuth(t, f, f.f.User, "admin", nil), groups
}
func directoryWant(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("expected %s, got %v", want, err)
	}
}
func directoryStart(t *testing.T, d *directoryCandidate, a identity.Auth, connector string) directoryRun {
	t.Helper()
	r, err := d.Start(context.Background(), a, connector, 1, uuid.NewString(), uuid.NewString())
	if err != nil || r.ID == "" || r.State != "running" {
		t.Fatal("actual run start", err)
	}
	return r
}
func directoryFinish(t *testing.T, d *directoryCandidate, a identity.Auth, connector string, r directoryRun, groups directoryGroups) directoryRun {
	t.Helper()
	out, err := d.Finish(context.Background(), a, connector, r.ID, groups, false, uuid.NewString())
	if err != nil || out.ID != r.ID || out.State != "succeeded" {
		t.Fatal("actual publication", err)
	}
	return out
}
func directoryScalar(t *testing.T, f sourceRepositoryFixture, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.f.PostgreSQL.QueryRow(f.ctx, sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func directoryRunCounts(t *testing.T, f sourceRepositoryFixture, id string) [3]int {
	return [3]int{directoryScalar(t, f, `SELECT count(*) FROM iam_directory_groups WHERE run_id=$1`, id), directoryScalar(t, f, `SELECT count(*) FROM iam_directory_members WHERE run_id=$1`, id), directoryScalar(t, f, `SELECT count(*) FROM iam_directory_heads WHERE run_id=$1`, id)}
}
func directoryExpected(g directoryGroups) [3]int {
	n := 0
	for _, members := range g {
		n += len(members)
	}
	return [3]int{len(g), n, 1}
}
func directoryEvents(t *testing.T, f sourceRepositoryFixture, action string) int {
	return directoryScalar(t, f, `SELECT count(*) FROM audit_events WHERE action=$1`, action)
}
func directoryComplete(t *testing.T, f sourceRepositoryFixture, d *directoryCandidate, a identity.Auth, groups directoryGroups) directoryRun {
	return directoryFinish(t, d, a, f.source.ConnectorID, directoryStart(t, d, a, f.source.ConnectorID), groups)
}

func TestDirectoryGenerationPublishedLifecycle(t *testing.T) {
	f, d, a, groups := directoryFixture(t)
	key := uuid.NewString()
	r, err := d.Start(f.ctx, a, f.source.ConnectorID, 1, key, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := d.Start(f.ctx, a, f.source.ConnectorID, 1, key, uuid.NewString())
	if err != nil || replay != r || directoryEvents(t, f, "sso.directory_sync_started") != 1 {
		t.Fatal("start replay changed run or expiry", err)
	}
	directoryFinish(t, d, a, f.source.ConnectorID, r, groups)
	current, err := d.Current(f.ctx, a, f.source.ConnectorID)
	if err != nil || current.Run.ID != r.ID || !reflect.DeepEqual(current.Groups, groups) {
		t.Fatal("real-directory closure did not persist", err)
	}
	failed := directoryStart(t, d, a, f.source.ConnectorID)
	out, err := d.Finish(f.ctx, a, f.source.ConnectorID, failed.ID, nil, true, uuid.NewString())
	if err != nil || out.State != "failed" || out.Error != "DIRECTORY_SCAN_FAILED" || directoryRunCounts(t, f, failed.ID) != ([3]int{}) {
		t.Fatal("failure published membership", err)
	}
	after, err := d.Current(f.ctx, a, f.source.ConnectorID)
	if err != nil || !reflect.DeepEqual(current, after) {
		t.Fatal("failed scan changed previous complete generation or expiry", err)
	}
	empty := directoryStart(t, d, a, f.source.ConnectorID)
	directoryFinish(t, d, a, f.source.ConnectorID, empty, directoryGroups{})
	now, err := d.Current(f.ctx, a, f.source.ConnectorID)
	if err != nil || now.Run.ID != empty.ID || len(now.Groups) != 0 || now.Groups == nil {
		t.Fatal("complete empty generation confused with failure", err)
	}
}
func TestDirectoryGenerationScopeAndAuthority(t *testing.T) {
	t.Run("foreign_connector_run_and_sql_parent", func(t *testing.T) {
		f, d, a, groups := directoryFixture(t)
		run := directoryStart(t, d, a, f.source.ConnectorID)
		foreignTenant := uuid.NewString()
		f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Foreign directory tenant','active')`, ``, foreignTenant))
		foreign := catalogSeedConnector(t, f, foreignTenant, f.accounts.Config.SSO.Providers[0].Origin, "Foreign directory authority", true, time.Now().UTC().Truncate(time.Microsecond))
		foreignRun := uuid.NewString()
		_, err := f.f.PostgreSQL.Exec(f.ctx, `INSERT INTO iam_directory_runs SELECT $1,$2,$3,connector_revision,auth_version,actor_user_id,actor_session_id,actor_epoch,$4,fingerprint,state,error_code,completion_digest,created_at,run_until,valid_until,finished_at FROM iam_directory_runs WHERE id=$5`, foreignRun, foreignTenant, foreign, uuid.NewString(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if out, e := d.Start(f.ctx, a, foreign, 1, uuid.NewString(), uuid.NewString()); out.ID != "" {
			t.Fatal("foreign start DTO")
		} else {
			directoryWant(t, e, "SSO_CONNECTOR_NOT_FOUND")
		}
		for _, pair := range [][2]string{{foreign, foreignRun}, {f.source.ConnectorID, foreignRun}} {
			out, e := d.Finish(f.ctx, a, pair[0], pair[1], groups, false, uuid.NewString())
			if out.ID != "" {
				t.Fatal("foreign finish DTO")
			}
			want := "DIRECTORY_RUN_NOT_FOUND"
			if pair[0] == foreign {
				want = "SSO_CONNECTOR_NOT_FOUND"
			}
			directoryWant(t, e, want)
			out, e = d.Status(f.ctx, a, pair[0], pair[1])
			if out.ID != "" {
				t.Fatal("foreign status DTO")
			}
			directoryWant(t, e, want)
		}
		if out, e := d.Current(f.ctx, a, foreign); out.Run.ID != "" || out.Groups != nil {
			t.Fatal("foreign current DTO")
		} else {
			directoryWant(t, e, "SSO_CONNECTOR_NOT_FOUND")
		}
		for _, statement := range []string{`INSERT INTO iam_directory_groups VALUES($1,$2,$3,'11111111-1111-1111-1111-111111111111')`, `INSERT INTO iam_directory_heads VALUES($1,$2,$3)`} {
			if _, e := f.f.PostgreSQL.Exec(f.ctx, statement, f.f.Tenant, f.source.ConnectorID, foreignRun); e == nil {
				t.Fatal("mixed-scope generation parent accepted")
			}
		}
		if directoryRunCounts(t, f, run.ID) != ([3]int{}) || directoryEvents(t, f, "sso.directory_sync_completed") != 0 {
			t.Fatal("foreign request changed local generation")
		}
	})
	t.Run("different_current_session", func(t *testing.T) {
		f, d, a, g := directoryFixture(t)
		run := directoryStart(t, d, a, f.source.ConnectorID)
		other := catalogAuth(t, f, f.f.User, "admin", nil)
		if out, e := d.Finish(f.ctx, other, f.source.ConnectorID, run.ID, g, false, uuid.NewString()); out.ID != "" {
			t.Fatal("other session finish DTO")
		} else {
			directoryWant(t, e, "DIRECTORY_RUN_NOT_FOUND")
		}
		if out, e := d.Status(f.ctx, other, f.source.ConnectorID, run.ID); out.ID != "" {
			t.Fatal("other session status DTO")
		} else {
			directoryWant(t, e, "DIRECTORY_RUN_NOT_FOUND")
		}
	})
	cases := []struct {
		name string
		edit func(*testing.T, sourceRepositoryFixture, identity.Auth) identity.Auth
		want string
	}{
		{"portal", func(t *testing.T, f sourceRepositoryFixture, a identity.Auth) identity.Auth {
			return catalogAuth(t, f, f.f.User, "portal", nil)
		}, "ACCESS_DENIED"},
		{"revoked", func(t *testing.T, f sourceRepositoryFixture, a identity.Auth) identity.Auth {
			f.exec(t, f.f.Statement(`UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='directory_fixture' WHERE id=$1`, ``, a.SessionID))
			return a
		}, "AUTH_SESSION_EXPIRED"},
		{"disabled_user", func(t *testing.T, f sourceRepositoryFixture, a identity.Auth) identity.Auth {
			f.exec(t, f.f.Statement(`UPDATE users SET status='disabled' WHERE id=$1`, ``, a.Me.User.ID))
			return a
		}, "AUTH_SESSION_EXPIRED"},
		{"removed_admin", func(t *testing.T, f sourceRepositoryFixture, a identity.Auth) identity.Auth {
			f.exec(t, f.f.Statement(`UPDATE users SET is_platform_admin=false WHERE id=$1`, ``, a.Me.User.ID))
			return a
		}, "AUTH_SESSION_EXPIRED"},
		{"new_epoch", func(t *testing.T, f sourceRepositoryFixture, a identity.Auth) identity.Auth {
			f.exec(t, f.f.Statement(`UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, ``, a.Me.User.ID))
			return a
		}, "AUTH_SESSION_EXPIRED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, d, a, g := directoryFixture(t)
			run := directoryStart(t, d, a, f.source.ConnectorID)
			a = tc.edit(t, f, a)
			out, e := d.Start(f.ctx, a, f.source.ConnectorID, 1, uuid.NewString(), uuid.NewString())
			directoryWant(t, e, tc.want)
			if out.ID != "" {
				t.Fatal("stale start DTO")
			}
			out, e = d.Finish(f.ctx, a, f.source.ConnectorID, run.ID, g, false, uuid.NewString())
			directoryWant(t, e, tc.want)
			if out.ID != "" {
				t.Fatal("stale finish DTO")
			}
			out, e = d.Status(f.ctx, a, f.source.ConnectorID, run.ID)
			directoryWant(t, e, tc.want)
			if out.ID != "" {
				t.Fatal("stale status DTO")
			}
			current, e := d.Current(f.ctx, a, f.source.ConnectorID)
			directoryWant(t, e, tc.want)
			if current.Run.ID != "" || current.Groups != nil {
				t.Fatal("stale current DTO")
			}
			if directoryRunCounts(t, f, run.ID) != ([3]int{}) {
				t.Fatal("stale actor published")
			}
		})
	}
	for _, disabled := range []bool{false, true} {
		name := "changed_revision"
		if disabled {
			name = "disabled_connector"
		}
		t.Run(name, func(t *testing.T) {
			f, d, a, g := directoryFixture(t)
			directoryComplete(t, f, d, a, g)
			run := directoryStart(t, d, a, f.source.ConnectorID)
			enabled := !disabled
			f.exec(t, f.f.Statement(`UPDATE sso_connectors SET enabled=$2,revision=revision+1,auth_version=auth_version+1,updated_at=clock_timestamp() WHERE id=$1`, ``, f.source.ConnectorID, enabled))
			out, e := d.Finish(f.ctx, a, f.source.ConnectorID, run.ID, g, false, uuid.NewString())
			directoryWant(t, e, "DIRECTORY_SOURCE_CHANGED")
			if out.ID != "" {
				t.Fatal("stale connector finish")
			}
			current, e := d.Current(f.ctx, a, f.source.ConnectorID)
			directoryWant(t, e, "DIRECTORY_SOURCE_CHANGED")
			if current.Groups != nil {
				t.Fatal("stale connector authority")
			}
		})
	}
}
func TestDirectoryGenerationConcurrency(t *testing.T) {
	f, d, a, g := directoryFixture(t)
	key := uuid.NewString()
	gate := make(chan struct{})
	type result struct {
		r directoryRun
		e error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-gate
			r, e := d.Start(f.ctx, a, f.source.ConnectorID, 1, key, uuid.NewString())
			results <- result{r, e}
		}()
	}
	close(gate)
	first, second := <-results, <-results
	if first.e != nil || second.e != nil || first.r != second.r || directoryEvents(t, f, "sso.directory_sync_started") != 1 {
		t.Fatal("concurrent replay duplicated run", first.e, second.e)
	}
	if out, e := d.Start(f.ctx, a, f.source.ConnectorID, 1, uuid.NewString(), uuid.NewString()); out.ID != "" {
		t.Fatal("competing start returned run")
	} else {
		directoryWant(t, e, "DIRECTORY_SYNC_IN_PROGRESS")
	}
	gate = make(chan struct{})
	for range 2 {
		go func() {
			<-gate
			r, e := d.Finish(f.ctx, a, f.source.ConnectorID, first.r.ID, g, false, uuid.NewString())
			results <- result{r, e}
		}()
	}
	close(gate)
	left, right := <-results, <-results
	if left.e != nil || right.e != nil || left.r != right.r || directoryEvents(t, f, "sso.directory_sync_completed") != 1 {
		t.Fatal("concurrent finish duplicated generation", left.e, right.e)
	}
	if out, e := d.Finish(f.ctx, a, f.source.ConnectorID, first.r.ID, directoryGroups{}, false, uuid.NewString()); out.ID != "" {
		t.Fatal("conflicting replay returned result")
	} else {
		directoryWant(t, e, "IDEMPOTENCY_CONFLICT")
	}
	if got := directoryRunCounts(t, f, first.r.ID); got != directoryExpected(g) {
		t.Fatal("incomplete concurrent graph", got)
	}
}

func directoryAuditTrigger(t *testing.T, f sourceRepositoryFixture, action string) func() {
	t.Helper()
	_, err := f.f.PostgreSQL.Exec(f.ctx, `CREATE FUNCTION iam_directory_refuse_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='`+action+`' THEN RAISE EXCEPTION 'DIRECTORY_FIXTURE_AUDIT_REFUSED'; END IF; RETURN NEW; END; $$; CREATE TRIGGER iam_directory_refuse_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION iam_directory_refuse_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, e := f.f.PostgreSQL.Exec(f.ctx, `DROP TRIGGER iam_directory_refuse_audit ON audit_events; DROP FUNCTION iam_directory_refuse_audit()`); e != nil {
			t.Fatal(e)
		}
	}
}
func TestDirectoryGenerationRollbackCancellation(t *testing.T) {
	for _, mode := range []string{"start_audit", "complete_audit", "failure_audit", "cancel_after_stage"} {
		t.Run(mode, func(t *testing.T) {
			f, d, a, g := directoryFixture(t)
			old := directoryComplete(t, f, d, a, g)
			before, e := d.Current(f.ctx, a, f.source.ConnectorID)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "start_audit" {
				undo := directoryAuditTrigger(t, f, "sso.directory_sync_started")
				n := directoryScalar(t, f, `SELECT count(*) FROM iam_directory_runs`)
				out, e := d.Start(f.ctx, a, f.source.ConnectorID, 1, uuid.NewString(), uuid.NewString())
				if e == nil || out.ID != "" || directoryScalar(t, f, `SELECT count(*) FROM iam_directory_runs`) != n {
					t.Fatal("failed start retained run", e)
				}
				undo()
				directoryStart(t, d, a, f.source.ConnectorID)
				return
			}
			run := directoryStart(t, d, a, f.source.ConnectorID)
			ctx := f.ctx
			failed := mode == "failure_audit"
			groups := g
			var undo func()
			if failed {
				groups = nil
				undo = directoryAuditTrigger(t, f, "sso.directory_sync_failed")
			} else if mode == "complete_audit" {
				undo = directoryAuditTrigger(t, f, "sso.directory_sync_completed")
			} else {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				d.phase = func(string) {
					if got := directoryRunCounts(t, f, run.ID); got != ([3]int{}) {
						t.Fatal("uncommitted graph visible to independent reader", got)
					}
					if directoryScalar(t, f, `SELECT count(*) FROM iam_directory_heads WHERE run_id=$1`, old.ID) != 1 {
						t.Fatal("old head disappeared before commit")
					}
					cancel()
				}
			}
			out, e := d.Finish(ctx, a, f.source.ConnectorID, run.ID, groups, failed, uuid.NewString())
			d.phase = nil
			if e == nil || out.ID != "" || directoryRunCounts(t, f, run.ID) != ([3]int{}) {
				t.Fatal("failed publication retained partial graph", e)
			}
			if undo != nil {
				undo()
			}
			current, e := d.Current(f.ctx, a, f.source.ConnectorID)
			if e != nil || !reflect.DeepEqual(before, current) {
				t.Fatal("rollback changed previous complete generation", e)
			}
			status, e := d.Status(f.ctx, a, f.source.ConnectorID, run.ID)
			if e != nil || status.State != "running" {
				t.Fatal("failed transaction committed run transition", e)
			}
			out, e = d.Finish(f.ctx, a, f.source.ConnectorID, run.ID, groups, failed, uuid.NewString())
			if e != nil || out.ID != run.ID || !out.ValidUntil.Equal(run.ValidUntil) {
				t.Fatal("explicit retry failed or refreshed expiry", e)
			}
		})
	}
}
func TestDirectoryGenerationCommitUncertainty(t *testing.T) {
	for _, op := range []string{"start", "complete", "failed"} {
		t.Run(op, func(t *testing.T) {
			for _, mode := range []commitfault.Mode{commitfault.DropBeforeCommit, commitfault.DropCommitAcknowledgement} {
				name := "not_forwarded"
				committed := false
				if mode == commitfault.DropCommitAcknowledgement {
					name = "ack_suppressed"
					committed = true
				}
				t.Run(name, func(t *testing.T) {
					f, d, a, g := directoryFixture(t)
					var run directoryRun
					var old directoryRun
					if op != "start" {
						old = directoryComplete(t, f, d, a, g)
						run = directoryStart(t, d, a, f.source.ConnectorID)
					}
					key := uuid.NewString()
					store, proxy := connectorFaultStore(t, f)
					originalStore, originalDB := f.accounts.Storage, f.accounts.DB
					defer func() { f.accounts.Storage, f.accounts.DB = originalStore, originalDB }()
					f.accounts.Storage, f.accounts.DB = store, nil
					if e := proxy.Arm(mode); e != nil {
						t.Fatal(e)
					}
					var out directoryRun
					var e error
					if op == "start" {
						out, e = d.Start(f.ctx, a, f.source.ConnectorID, 1, key, uuid.NewString())
					} else {
						groups := g
						if op == "failed" {
							groups = nil
						}
						out, e = d.Finish(f.ctx, a, f.source.ConnectorID, run.ID, groups, op == "failed", uuid.NewString())
					}
					f.accounts.Storage, f.accounts.DB = originalStore, originalDB
					directoryWant(t, e, string(transaction.CommitUnknown))
					if out.ID != "" || proxy.Commits() != 1 {
						t.Fatal("unknown commit released DTO or resent write", proxy.Commits())
					}
					select {
					case observed := <-proxy.Outcomes():
						if observed.Forwarded != committed || observed.ServerOK != committed || !observed.Suppressed {
							t.Fatal("actual commit fault not established", observed)
						}
					case <-f.ctx.Done():
						t.Fatal("wire observation deadline")
					}
					if op == "start" {
						want := 0
						if committed {
							want = 1
						}
						if directoryScalar(t, f, `SELECT count(*) FROM iam_directory_runs WHERE idempotency_key=$1`, key) != want || directoryEvents(t, f, "sso.directory_sync_started") != want {
							t.Fatal("independent uncertain start outcome")
						}
						if committed {
							var id string
							if e = f.f.PostgreSQL.QueryRow(f.ctx, `SELECT id::text FROM iam_directory_runs WHERE idempotency_key=$1`, key).Scan(&id); e != nil {
								t.Fatal(e)
							}
							if _, e = d.Status(f.ctx, a, f.source.ConnectorID, id); e != nil {
								t.Fatal("authorized readback", e)
							}
						}
						out, e = d.Start(f.ctx, a, f.source.ConnectorID, 1, key, uuid.NewString())
						if e != nil || out.ID == "" || directoryEvents(t, f, "sso.directory_sync_started") != 1 {
							t.Fatal("explicit start retry", e)
						}
						return
					}
					status, e := d.Status(f.ctx, a, f.source.ConnectorID, run.ID)
					if e != nil {
						t.Fatal("authorized result readback", e)
					}
					expectedState := "running"
					if committed {
						expectedState = "succeeded"
						if op == "failed" {
							expectedState = "failed"
						}
					}
					if status.State != expectedState {
						t.Fatal("independent committed run mismatch", status.State)
					}
					wantCounts := [3]int{}
					if committed && op == "complete" {
						wantCounts = directoryExpected(g)
					}
					if directoryRunCounts(t, f, run.ID) != wantCounts {
						t.Fatal("uncertain publication was not atomic")
					}
					head, e := d.Current(f.ctx, a, f.source.ConnectorID)
					expectedHead := old.ID
					if committed && op == "complete" {
						expectedHead = run.ID
					}
					if e != nil || head.Run.ID != expectedHead {
						t.Fatal("uncertain current pointer", e)
					}
					groups := g
					if op == "failed" {
						groups = nil
					}
					out, e = d.Finish(f.ctx, a, f.source.ConnectorID, run.ID, groups, op == "failed", uuid.NewString())
					if e != nil || out.ID != run.ID || !out.ValidUntil.Equal(run.ValidUntil) {
						t.Fatal("fresh explicit completion replay", e)
					}
					action := "sso.directory_sync_completed"
					wantEvents := 2
					if op == "failed" {
						action = "sso.directory_sync_failed"
						wantEvents = 1
					}
					if directoryEvents(t, f, action) != wantEvents {
						t.Fatal("replay duplicated completion audit")
					}
				})
			}
		})
	}
}
func TestDirectoryGenerationExpiryAndRetry(t *testing.T) {
	f, d, a, g := directoryFixture(t)
	d.RunSeconds = 1
	d.FreshSeconds = 2
	old := directoryComplete(t, f, d, a, g)
	failed := directoryStart(t, d, a, f.source.ConnectorID)
	if _, e := d.Finish(f.ctx, a, f.source.ConnectorID, failed.ID, nil, true, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	waitExpired := func(id, column string) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if directoryScalar(t, f, `SELECT count(*) FROM iam_directory_runs WHERE id=$1 AND `+column+`<=clock_timestamp()`, id) == 1 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("bounded actual database expiry not observed")
	}
	waitExpired(old.ID, "valid_until")
	current, e := d.Current(f.ctx, a, f.source.ConnectorID)
	directoryWant(t, e, "DIRECTORY_GENERATION_EXPIRED")
	if current.Groups != nil {
		t.Fatal("failed scan refreshed old authority")
	}
	run := directoryStart(t, d, a, f.source.ConnectorID)
	waitExpired(run.ID, "run_until")
	out, e := d.Finish(f.ctx, a, f.source.ConnectorID, run.ID, g, false, uuid.NewString())
	directoryWant(t, e, "DIRECTORY_RUN_EXPIRED")
	if out.ID != "" || directoryRunCounts(t, f, run.ID) != ([3]int{}) {
		t.Fatal("expired run published")
	}
	next := directoryStart(t, d, a, f.source.ConnectorID)
	status, e := d.Status(f.ctx, a, f.source.ConnectorID, run.ID)
	if e != nil || status.State != "failed" || status.Error != "DIRECTORY_RUN_EXPIRED" {
		t.Fatal("explicit retry did not retire expired run", e)
	}
	directoryFinish(t, d, a, f.source.ConnectorID, next, g)
}
