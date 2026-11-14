//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// All records in this suite are explicitly synthetic SQL metadata. They do not
// establish physical stop, human review, model quality, file or authorization evidence.
type publicationSchemaDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type publicationSchemaTable struct{ name, columns, order string }
type publicationSchemaGraph struct {
	preparationSchemaRows
	review, key, report string
}

func publicationSchemaExec(t *testing.T, q publicationSchemaDB, statement string, args ...any) {
	t.Helper()
	if _, e := q.ExecContext(t.Context(), statement, args...); e != nil {
		trainingSchemaDigestError(t, e)
	}
}
func publicationSchemaLayout(t *testing.T, q publicationSchemaDB) []publicationSchemaTable {
	t.Helper()
	rows, e := q.QueryContext(t.Context(), `SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE() AND table_type='BASE TABLE' ORDER BY table_name`)
	if e != nil {
		trainingSchemaDigestError(t, e)
	}
	var tables []string
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil || !identifier.MatchString(n) {
			t.Fatal("owned publication table name required")
		}
		tables = append(tables, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		trainingSchemaDigestError(t, e)
	}
	var out []publicationSchemaTable
	for _, n := range tables {
		rows, e = q.QueryContext(t.Context(), `SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`, n)
		if e != nil {
			trainingSchemaDigestError(t, e)
		}
		var columns []string
		for rows.Next() {
			var c string
			if e = rows.Scan(&c); e != nil || !identifier.MatchString(c) {
				t.Fatal("owned publication column required")
			}
			columns = append(columns, "HEX(CAST(`"+c+"` AS BINARY))")
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			trainingSchemaDigestError(t, e)
		}
		rows, e = q.QueryContext(t.Context(), `SELECT column_name FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name='PRIMARY' ORDER BY seq_in_index`, n)
		if e != nil {
			trainingSchemaDigestError(t, e)
		}
		var order []string
		for rows.Next() {
			var c string
			if e = rows.Scan(&c); e != nil || !identifier.MatchString(c) {
				t.Fatal("owned publication primary key required")
			}
			order = append(order, "`"+c+"`")
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			trainingSchemaDigestError(t, e)
		}
		if len(columns) == 0 || len(order) == 0 {
			t.Fatal("complete publication fingerprint layout required")
		}
		out = append(out, publicationSchemaTable{n, strings.Join(columns, ","), strings.Join(order, ",")})
	}
	return out
}
func publicationSchemaDigest(t *testing.T, q publicationSchemaDB, layout []publicationSchemaTable, oldJournal bool) string {
	t.Helper()
	sum := sha256.New()
	for _, v := range layout {
		sum.Write([]byte(v.name))
		sum.Write([]byte{0})
		filter := ""
		if oldJournal && v.name == journalObject {
			filter = " WHERE step<=117"
		}
		rows, e := q.QueryContext(t.Context(), "SELECT JSON_ARRAY("+v.columns+") FROM `"+v.name+"` FORCE INDEX(PRIMARY)"+filter+" ORDER BY "+v.order)
		if e != nil {
			trainingSchemaDigestError(t, e)
		}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				trainingSchemaDigestError(t, e)
			}
			row := sha256.Sum256(raw)
			sum.Write(row[:])
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			trainingSchemaDigestError(t, e)
		}
	}
	return hex.EncodeToString(sum.Sum(nil))
}
func publicationSchemaRefuses(t *testing.T, q publicationSchemaDB, layout []publicationSchemaTable, number uint16, statement string, args ...any) {
	t.Helper()
	before := publicationSchemaDigest(t, q, layout, false)
	_, e := q.ExecContext(t.Context(), statement, args...)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != number {
		if safe != nil {
			var vendor *driver.MySQLError
			if errors.As(e, &vendor) && vendor.Number == 1267 {
				pairs := regexp.MustCompile(`\(([a-z0-9_]+),(IMPLICIT|COERCIBLE|EXPLICIT|NONE)\)`).FindAllStringSubmatch(vendor.Message, -1)
				for _, pair := range pairs {
					t.Logf("PUBLICATION_COLLATION_DIAGNOSTIC collation=%s coercibility=%s", pair[1], pair[2])
				}
			}
			t.Fatalf("PUBLICATION_REFUSAL expected=%d actual=%d state_hex=%x", number, safe.Number, safe.SQLState)
		}
		t.Fatal("publication safe numeric refusal required")
	}
	if before != publicationSchemaDigest(t, q, layout, false) {
		t.Fatal("refused publication changed complete business/journal fingerprint")
	}
}
func publicationSchemaSeed(t *testing.T, s *mysqlstore.Store, old holdoutSchemaRows, gate, stopped bool) publicationSchemaGraph {
	t.Helper()
	r := preparationSchemaSeed(t, s, old)
	// Freeze synthetic report/stop fixtures before immutable output guards exist.
	raw := []byte(fmt.Sprintf(`{"event_count":1,"gate_passed":%t,"status":"prepared","synthetic_schema":true}`, gate))
	digest := hash(string(raw))
	publicationSchemaExec(t, s.Pool(), `UPDATE model_preparation_attempts SET outcome=CAST(? AS JSON),outcome_bytes=?,outcome_sha256=? WHERE id=?`, raw, raw, digest, r.preparationAttempt)
	publicationSchemaExec(t, s.Pool(), `UPDATE model_preparation_artifacts SET report=CAST(? AS JSON),report_bytes=?,manifest_sha256=? WHERE id=?`, raw, raw, digest, r.preparationUpload)
	if !stopped {
		publicationSchemaExec(t, s.Pool(), `UPDATE model_preparation_attempts SET physical_stop_receipt=NULL,physical_stop_sha256=NULL,physical_stopped_at=NULL WHERE id=?`, r.preparationAttempt)
	}
	publicationSchemaExec(t, s.Pool(), `UPDATE model_preparations SET pending_recovery_id=NULL WHERE id=?`, r.preparation)
	publicationSchemaExec(t, s.Pool(), preparationSchemaInsertLease, uuid.NewString(), r.tenant, r.workspace, r.preparationNode, r.preparationGPU, nil, nil, r.preparation, "released")
	return publicationSchemaGraph{r, uuid.NewString(), uuid.NewString(), digest}
}
func publicationSchemaCatalog(r publicationSchemaGraph) string {
	return `{"model_id":"` + r.preparationUpload + `","synthetic_schema":true}`
}
func publicationSchemaStage(t *testing.T, q publicationSchemaDB, r publicationSchemaGraph) {
	publicationSchemaExec(t, q, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES(?,?,?,'Synthetic derivative \u4e16\u754c','qwen3-0.6b',?,CAST(? AS JSON),'staged',?,'merged_lora')", r.preparationUpload, r.tenant, r.workspace, hash(publicationSchemaCatalog(r)), publicationSchemaCatalog(r), r.user)
}

const publicationSchemaReviewInsert = `INSERT INTO model_preparation_reviews(id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,report_sha256,expected_revision,decision,model_id,model_name,reviewed_by,audience,reason,idempotency_key,fingerprint,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'admin','Synthetic SQL metadata; no human review',?,?,?)`

func publicationSchemaReviewArgs(r publicationSchemaGraph, decision string) []any {
	var model any = r.preparationUpload
	name := "Synthetic derivative \u4e16\u754c"
	if decision == "reject" {
		model = nil
		name = ""
	}
	return []any{r.review, r.tenant, r.workspace, r.preparation, r.preparationAttempt, r.preparationUpload, r.report, int64(2), decision, model, name, r.user, r.key, []byte(strings.Repeat("r", 32)), r.heartbeat}
}
func publicationSchemaDerive(t *testing.T, q publicationSchemaDB, r publicationSchemaGraph) {
	publicationSchemaExec(t, q, publicationSchemaDerivationInsert, publicationSchemaDerivationArgs(r)...)
}

const publicationSchemaDerivationInsert = `INSERT INTO model_derivations(model_id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,base_model_id,review_id,catalog_digest,catalog,report_sha256) VALUES(?,?,?,?,?,?,?,?,?,CAST(? AS JSON),?)`

func publicationSchemaDerivationArgs(r publicationSchemaGraph) []any {
	return []any{r.preparationUpload, r.tenant, r.workspace, r.preparation, r.preparationAttempt, r.preparationUpload, r.model, r.review, hash(publicationSchemaCatalog(r)), publicationSchemaCatalog(r), r.report}
}
func publicationSchemaBind(t *testing.T, q publicationSchemaDB, r publicationSchemaGraph, decision string) {
	var model any = r.preparationUpload
	state := "approved"
	if decision == "reject" {
		model = nil
		state = "rejected"
	}
	publicationSchemaExec(t, q, `UPDATE model_preparations SET state=?,review_id=?,derived_model_id=?,revision=revision+1 WHERE id=?`, state, r.review, model, r.preparation)
}
func publicationSchemaApproved(t *testing.T, q publicationSchemaDB, r publicationSchemaGraph) {
	publicationSchemaStage(t, q, r)
	publicationSchemaExec(t, q, publicationSchemaReviewInsert, publicationSchemaReviewArgs(r, "approve")...)
	publicationSchemaDerive(t, q, r)
	publicationSchemaBind(t, q, r, "approve")
}
func publicationSchemaPublish(t *testing.T, q publicationSchemaDB, r publicationSchemaGraph) {
	publicationSchemaExec(t, q, `INSERT INTO model_publications(model_id,tenant_id,workspace_id) VALUES(?,?,?)`, r.preparationUpload, r.tenant, r.workspace)
}
func publicationSchemaTransaction(t *testing.T, s *mysqlstore.Store) *sql.Tx {
	t.Helper()
	tx, e := s.Pool().BeginTx(t.Context(), nil)
	if e != nil {
		trainingSchemaDigestError(t, e)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

// The new137-step workload has its own bounded test budget. Do not change the
// historical128-step helper or production operator configuration to pass a test.
func publicationSchemaRun(t *testing.T, s *mysqlstore.Store, c Catalog, mode Mode) {
	t.Helper()
	options := testOptions()
	options.MaximumSteps = publicationSchemaStepCount
	result, e := Run(t.Context(), s, c, options, mode, nil)
	if e != nil || !result.Ready || !result.LockReleased {
		t.Fatal("actual product publication migration", result, e)
	}
}

func TestActualMySQLPublicationSchema(t *testing.T) {
	s, c, _ := actualMigration(t)
	if c.Steps() != publicationSchemaStepCount {
		t.Fatal("complete product publication137 catalog required")
	}
	initial := c
	initial.steps = initial.steps[:103]
	publicationSchemaRun(t, s, initial, Apply)
	old := holdoutSchemaSeed(t, s, holdoutSchemaOldRows(t, s))
	seeds := make([]publicationSchemaGraph, 8)
	for n := range seeds {
		seeds[n] = publicationSchemaSeed(t, s, old, n != 6, n != 7)
	}
	initial.steps = c.steps[:117]
	publicationSchemaRun(t, s, initial, Apply)
	layout117 := publicationSchemaLayout(t, s.Pool())
	before117 := publicationSchemaDigest(t, s.Pool(), layout117, true)
	publicationSchemaRun(t, s, c, Apply)
	layout := publicationSchemaLayout(t, s.Pool())
	t.Run("populated117_upgrade", func(t *testing.T) {
		if publicationSchemaDigest(t, s.Pool(), layout117, true) != before117 {
			t.Fatal("publication upgrade changed prior117 data/journal")
		}
	})
	cases := []string{"approve_NULL_model", "approve_wrong_model", "reject_with_model", "review_wrong_revision", "review_wrong_report", "review_pending_recovery", "review_unreleased_gpu", "review_unstopped_attempt", "review_failed_gate", "review_foreign_scope", "derivation_wrong_catalog", "derivation_wrong_reviewer", "derivation_wrong_base", "publication_incomplete_parent", "direct_ready_insert", "direct_ready_update", "terminal_decision_rebind", "terminal_attempt_rebind", "review_update", "review_delete", "derivation_update", "derivation_delete", "marker_update", "marker_delete", "derived_identity_update", "derived_physical_delete", "base_kind_change", "invalid_UUID", "invalid_fingerprint", "invalid_revision_ceiling", "invalid_JSON_bound"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			tx := publicationSchemaTransaction(t, s)
			r := seeds[0]
			if name == "review_failed_gate" {
				r = seeds[6]
			}
			if name == "review_unstopped_attempt" {
				r = seeds[7]
			}
			if name == "base_kind_change" {
				publicationSchemaRefuses(t, tx, layout, 1644, `UPDATE model_versions SET kind='merged_lora' WHERE id=?`, r.model)
				return
			}
			if name == "direct_ready_insert" {
				publicationSchemaRefuses(t, tx, layout, 1644, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES(?,?,?,'Synthetic','qwen3-0.6b',?,JSON_OBJECT(),'ready',?,'merged_lora')`, r.preparationUpload, r.tenant, r.workspace, hash("synthetic"), r.user)
				return
			}
			if name == "invalid_JSON_bound" {
				publicationSchemaRefuses(t, tx, layout, 3819, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES(?,?,?,'Synthetic','qwen3-0.6b',?,JSON_OBJECT('value',REPEAT('x',16384)),'staged',?,'merged_lora')`, r.preparationUpload, r.tenant, r.workspace, hash("synthetic"), r.user)
				return
			}
			staged := r
			if name == "derivation_wrong_reviewer" {
				other := uuid.NewString()
				publicationSchemaExec(t, tx, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,CONCAT(?,'@example.invalid'),CONCAT(?,'@example.invalid'),'Other synthetic reviewer','en','active',true)`, other, other, other)
				staged.user = other
			}
			publicationSchemaStage(t, tx, staged)
			if name == "direct_ready_update" {
				publicationSchemaRefuses(t, tx, layout, 1644, `UPDATE model_versions SET state='ready' WHERE id=?`, r.preparationUpload)
				return
			}
			args := publicationSchemaReviewArgs(r, "approve")
			number := uint16(1644)
			switch name {
			case "approve_NULL_model":
				args[9] = nil
				number = 3819
			case "approve_wrong_model":
				args[9] = r.model
				number = 3819
			case "reject_with_model":
				args[8] = "reject"
				args[10] = ""
				number = 3819
			case "review_wrong_revision":
				args[7] = int64(3)
			case "review_wrong_report":
				args[6] = hash("different report")
			case "review_pending_recovery":
				publicationSchemaExec(t, tx, `UPDATE model_preparations SET pending_recovery_id=? WHERE id=?`, r.preparationRecovery, r.preparation)
			case "review_unreleased_gpu":
				publicationSchemaExec(t, tx, `UPDATE gpu_leases SET state='held' WHERE model_preparation_id=?`, r.preparation)
			case "review_foreign_scope":
				args[2] = uuid.NewString()
			case "invalid_UUID":
				args[0] = "INVALID"
				number = 3819
			case "invalid_fingerprint":
				args[13] = []byte("short")
				number = 3819
			case "invalid_revision_ceiling":
				publicationSchemaExec(t, tx, `UPDATE model_preparations SET revision=9007199254740991 WHERE id=?`, r.preparation)
				args[7] = int64(9007199254740991)
				number = 3819
			}
			switch name {
			case "approve_NULL_model", "approve_wrong_model", "reject_with_model", "review_wrong_revision", "review_wrong_report", "review_pending_recovery", "review_unreleased_gpu", "review_unstopped_attempt", "review_failed_gate", "review_foreign_scope", "invalid_UUID", "invalid_fingerprint", "invalid_revision_ceiling":
				publicationSchemaRefuses(t, tx, layout, number, publicationSchemaReviewInsert, args...)
				return
			}
			publicationSchemaExec(t, tx, publicationSchemaReviewInsert, args...)
			if strings.HasPrefix(name, "derivation_wrong_") {
				args = publicationSchemaDerivationArgs(r)
				switch name {
				case "derivation_wrong_catalog":
					args[9] = `{"different":true}`
				case "derivation_wrong_base":
					args[6] = r.preparationUpload
				case "derivation_wrong_reviewer":
				}
				publicationSchemaRefuses(t, tx, layout, 1644, publicationSchemaDerivationInsert, args...)
				return
			}
			if name == "publication_incomplete_parent" {
				publicationSchemaRefuses(t, tx, layout, 1644, `INSERT INTO model_publications(model_id,tenant_id,workspace_id) VALUES(?,?,?)`, r.preparationUpload, r.tenant, r.workspace)
				return
			}
			switch name {
			case "review_update":
				publicationSchemaRefuses(t, tx, layout, 1644, `UPDATE model_preparation_reviews SET reason='changed' WHERE id=?`, r.review)
				return
			case "review_delete":
				publicationSchemaRefuses(t, tx, layout, 1644, `DELETE FROM model_preparation_reviews WHERE id=?`, r.review)
				return
			}
			publicationSchemaDerive(t, tx, r)
			switch name {
			case "derivation_update":
				publicationSchemaRefuses(t, tx, layout, 1644, `UPDATE model_derivations SET report_sha256=? WHERE model_id=?`, hash("changed"), r.preparationUpload)
				return
			case "derivation_delete":
				publicationSchemaRefuses(t, tx, layout, 1644, `DELETE FROM model_derivations WHERE model_id=?`, r.preparationUpload)
				return
			}
			publicationSchemaBind(t, tx, r, "approve")
			publicationSchemaPublish(t, tx, r)
			switch name {
			case "terminal_decision_rebind":
				publicationSchemaRefuses(t, tx, layout, 1644, `UPDATE model_preparations SET state='awaiting_review',review_id=NULL,derived_model_id=NULL WHERE id=?`, r.preparation)
			case "terminal_attempt_rebind":
				publicationSchemaRefuses(t, tx, layout, 1644, `UPDATE model_preparations SET current_attempt_id=NULL,attempt_number=0 WHERE id=?`, r.preparation)
			case "marker_update":
				publicationSchemaRefuses(t, tx, layout, 1644, `UPDATE model_publications SET created_at=TIMESTAMPADD(SECOND,1,created_at) WHERE model_id=?`, r.preparationUpload)
			case "marker_delete":
				publicationSchemaRefuses(t, tx, layout, 1644, `DELETE FROM model_publications WHERE model_id=?`, r.preparationUpload)
			case "derived_identity_update":
				publicationSchemaRefuses(t, tx, layout, 1644, `UPDATE model_versions SET name='changed' WHERE id=?`, r.preparationUpload)
			case "derived_physical_delete":
				publicationSchemaRefuses(t, tx, layout, 1644, `DELETE FROM model_versions WHERE id=?`, r.preparationUpload)
			default:
				t.Fatal("unhandled frozen publication refusal", name)
			}
		})
	}
	t.Run("nested_failure_statement_rollback", func(t *testing.T) {
		publicationSchemaExec(t, s.Pool(), `CREATE TRIGGER publication_schema_test_fault AFTER UPDATE ON model_versions FOR EACH ROW BEGIN IF NEW.kind='merged_lora' AND NEW.state='ready' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='IDENTIFIED_TEST_ONLY_PUBLICATION_FAILURE'; END IF; END`)
		t.Cleanup(func() {
			// testing cancels t.Context before cleanup. Rollback runs first by LIFO;
			// use this distinct bounded context for the explicitly owned test trigger.
			cleanup, cancel := context.WithTimeout(context.Background(), time.Duration(testOptions().CleanupSeconds)*time.Second)
			defer cancel()
			if _, e := s.Pool().ExecContext(cleanup, `DROP TRIGGER publication_schema_test_fault`); e != nil {
				t.Errorf("publication test fault cleanup: %s", mysqlstore.SafeError(e))
			}
		})
		tx := publicationSchemaTransaction(t, s)
		publicationSchemaApproved(t, tx, seeds[0])
		publicationSchemaRefuses(t, tx, layout, 1644, `INSERT INTO model_publications(model_id,tenant_id,workspace_id) VALUES(?,?,?)`, seeds[0].preparationUpload, seeds[0].tenant, seeds[0].workspace)
		if e := tx.Rollback(); e != nil {
			trainingSchemaDigestError(t, e)
		}
	})
	t.Run("caller_rollback_complete_graph", func(t *testing.T) {
		before := publicationSchemaDigest(t, s.Pool(), layout, false)
		tx := publicationSchemaTransaction(t, s)
		publicationSchemaApproved(t, tx, seeds[0])
		publicationSchemaPublish(t, tx, seeds[0])
		if e := tx.Rollback(); e != nil {
			trainingSchemaDigestError(t, e)
		}
		if before != publicationSchemaDigest(t, s.Pool(), layout, false) {
			t.Fatal("caller rollback changed complete graph")
		}
	})
	t.Run("complete_approve_new_connection", func(t *testing.T) {
		r := seeds[1]
		tx := publicationSchemaTransaction(t, s)
		var first int
		if e := tx.QueryRowContext(t.Context(), `SELECT CONNECTION_ID()`).Scan(&first); e != nil {
			trainingSchemaDigestError(t, e)
		}
		publicationSchemaApproved(t, tx, r)
		publicationSchemaPublish(t, tx, r)
		other, e := s.Pool().Conn(t.Context())
		if e != nil {
			trainingSchemaDigestError(t, e)
		}
		defer other.Close()
		var second int
		if e = other.QueryRowContext(t.Context(), `SELECT CONNECTION_ID()`).Scan(&second); e != nil || first == second {
			t.Fatal("different actual connection required")
		}
		if e = tx.Commit(); e != nil {
			trainingSchemaDigestError(t, e)
		}
		var count int
		if e = other.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_versions m JOIN model_publications x ON x.model_id=m.id JOIN model_preparations p ON p.derived_model_id=m.id WHERE m.id=? AND m.tenant_id=? AND m.workspace_id=? AND m.state='ready' AND p.state='approved' AND p.review_id=?`, r.preparationUpload, r.tenant, r.workspace, r.review).Scan(&count); e != nil || count != 1 {
			t.Fatal("durable approved graph on new connection required", mysqlstore.SafeError(e))
		}
	})
	t.Run("complete_reject_without_model", func(t *testing.T) {
		r := seeds[2]
		tx := publicationSchemaTransaction(t, s)
		publicationSchemaExec(t, tx, publicationSchemaReviewInsert, publicationSchemaReviewArgs(r, "reject")...)
		publicationSchemaBind(t, tx, r, "reject")
		if e := tx.Commit(); e != nil {
			trainingSchemaDigestError(t, e)
		}
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM model_versions WHERE id=?)+(SELECT COUNT(*) FROM model_derivations WHERE model_id=?)+(SELECT COUNT(*) FROM model_publications WHERE model_id=?)`, r.preparationUpload, r.preparationUpload, r.preparationUpload).Scan(&count); e != nil || count != 0 {
			t.Fatal("rejection must create no model or publication", mysqlstore.SafeError(e))
		}
	})
	t.Run("staged_orphan_unavailable", func(t *testing.T) {
		r := seeds[3]
		tx := publicationSchemaTransaction(t, s)
		publicationSchemaStage(t, tx, r)
		if e := tx.Commit(); e != nil {
			trainingSchemaDigestError(t, e)
		}
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_versions WHERE id=? AND state='ready'`, r.preparationUpload).Scan(&count); e != nil || count != 0 {
			t.Fatal("committed staged orphan must remain unavailable", mysqlstore.SafeError(e))
		}
	})
	t.Run("retired_model_no_resurrection", func(t *testing.T) {
		r := seeds[4]
		tx := publicationSchemaTransaction(t, s)
		publicationSchemaApproved(t, tx, r)
		publicationSchemaPublish(t, tx, r)
		publicationSchemaExec(t, tx, `UPDATE model_versions SET state='deleted',deleted_at=UTC_TIMESTAMP(6) WHERE id=?`, r.preparationUpload)
		publicationSchemaRefuses(t, tx, layout, 1644, `UPDATE model_versions SET state='ready',deleted_at=NULL WHERE id=?`, r.preparationUpload)
	})
}

// A real empty database installs the complete137 catalog in one Apply. This is
// separate from the populated117 upgrade and never shares its retained rows.
func TestActualMySQLPublicationSchemaFresh(t *testing.T) {
	s, c, _ := actualMigration(t)
	t.Run("fresh137_noop_readiness", func(t *testing.T) {
		var objects int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE())+(SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE())+(SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema=DATABASE())+(SELECT COUNT(*) FROM information_schema.events WHERE event_schema=DATABASE())`).Scan(&objects); e != nil || objects != 0 {
			t.Fatal("empty isolated database required for direct137 installation", mysqlstore.SafeError(e))
		}
		publicationSchemaRun(t, s, c, Apply)
		layout := publicationSchemaLayout(t, s.Pool())
		before := publicationSchemaDigest(t, s.Pool(), layout, false)
		publicationSchemaRun(t, s, c, Apply)
		publicationSchemaRun(t, s, c, Readiness)
		if before != publicationSchemaDigest(t, s.Pool(), layout, false) {
			t.Fatal("fresh complete137 no-op/readiness changed persisted graph/journal")
		}
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM model_versions)+(SELECT COUNT(*) FROM model_preparations)+(SELECT COUNT(*) FROM model_preparation_reviews)+(SELECT COUNT(*) FROM model_derivations)+(SELECT COUNT(*) FROM model_publications)`).Scan(&objects); e != nil || objects != 0 {
			t.Fatal("fresh migration fabricated product data", mysqlstore.SafeError(e))
		}
	})
}

// Each journal/drift leaf uses its own new database. Cancellation represents
// exact observed DDL journal boundaries, not process death or power failure.
func publicationSchemaLegacy117(t *testing.T, s *mysqlstore.Store, c Catalog) ([]publicationSchemaTable, string) {
	t.Helper()
	prefix := c
	prefix.steps = c.steps[:117]
	publicationSchemaRun(t, s, prefix, Apply)
	old := holdoutSchemaSeed(t, s, holdoutSchemaOldRows(t, s))
	preparationSchemaSeed(t, s, old)
	layout := publicationSchemaLayout(t, s.Pool())
	return layout, publicationSchemaDigest(t, s.Pool(), layout, true)
}
func publicationSchemaJournalBoundary(t *testing.T, step int, stage string) {
	t.Helper()
	s, c, _ := actualMigration(t)
	layout, before := publicationSchemaLegacy117(t, s, c)
	options := testOptions()
	options.MaximumSteps = publicationSchemaStepCount
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result, e := Run(ctx, s, c, options, Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || result.Ready || result.DirtyStep != step || !result.LockReleased {
		t.Fatal("publication interruption lost dirty intent/lock", result, e)
	}
	var state string
	if e = s.Pool().QueryRowContext(t.Context(), `SELECT state FROM schema_migrations WHERE step=?`, step).Scan(&state); e != nil || state != Applying {
		t.Fatal("publication applying journal required", mysqlstore.SafeError(e))
	}
	if result, e = Run(t.Context(), s, c, options, Apply, nil); e == nil || e.Error() != CodeReconciliationRequired || result.Ready || !result.LockReleased {
		t.Fatal("dirty publication was implicitly retried", result, e)
	}
	if result, e = Run(t.Context(), s, c, options, Readiness, nil); e == nil || result.Ready || !result.LockReleased {
		t.Fatal("dirty publication served ready", result, e)
	}
	if publicationSchemaDigest(t, s.Pool(), layout, true) != before {
		t.Fatal("publication interruption/refusal changed prior117 rows/journal")
	}
	publicationSchemaRun(t, s, c, Reconcile)
	if publicationSchemaDigest(t, s.Pool(), layout, true) != before {
		t.Fatal("publication reconciliation changed prior117 rows/journal")
	}
	publicationSchemaRun(t, s, c, Readiness)
}
func TestActualMySQLPublicationSchemaJournal(t *testing.T) {
	for step := 118; step <= publicationSchemaStepCount; step++ {
		t.Run(fmt.Sprintf("Before%d", step), func(t *testing.T) { publicationSchemaJournalBoundary(t, step, Applying) })
		t.Run(fmt.Sprintf("After%d", step), func(t *testing.T) { publicationSchemaJournalBoundary(t, step, Verified) })
	}
}
func TestActualMySQLPublicationSchemaDrift(t *testing.T) {
	for _, name := range []string{"changed_table", "missing_trigger", "extra_trigger"} {
		t.Run(name, func(t *testing.T) {
			s, c, _ := actualMigration(t)
			oldLayout, oldBefore := publicationSchemaLegacy117(t, s, c)
			publicationSchemaRun(t, s, c, Apply)
			layout := publicationSchemaLayout(t, s.Pool())
			before := publicationSchemaDigest(t, s.Pool(), layout, false)
			code := CodeDefinitionDrift
			switch name {
			case "changed_table":
				publicationSchemaExec(t, s.Pool(), `ALTER TABLE model_publications ADD INDEX publication_schema_unexpected_index(created_at)`)
			case "missing_trigger":
				publicationSchemaExec(t, s.Pool(), `DROP TRIGGER model_publication_insert_guard`)
			case "extra_trigger":
				publicationSchemaExec(t, s.Pool(), `CREATE TRIGGER publication_schema_extra_probe BEFORE UPDATE ON model_publications FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='IDENTIFIED_TEST_ONLY_EXTRA_TRIGGER'; END`)
				code = CodeUnjournaledObject
			}
			options := testOptions()
			options.MaximumSteps = publicationSchemaStepCount
			for _, mode := range []Mode{Readiness, Apply, Reconcile} {
				result, e := Run(t.Context(), s, c, options, mode, nil)
				if e == nil || e.Error() != code || result.Ready || !result.LockReleased {
					t.Fatal("publication drift accepted or wrong safe category", mode, result, e)
				}
				if publicationSchemaDigest(t, s.Pool(), layout, false) != before || publicationSchemaDigest(t, s.Pool(), oldLayout, true) != oldBefore {
					t.Fatal("publication drift refusal changed data or journal")
				}
			}
			// Explicit fixture-only repair; production runners never silently do this.
			switch name {
			case "changed_table":
				publicationSchemaExec(t, s.Pool(), `ALTER TABLE model_publications DROP INDEX publication_schema_unexpected_index`)
				var object, definition string
				if e := s.Pool().QueryRowContext(t.Context(), `SHOW CREATE TABLE model_publications`).Scan(&object, &definition); e != nil {
					trainingSchemaDigestError(t, e)
				}
				t.Log("PUBLICATION_AFTER_INVERSE_ALTER_DEFINITION=" + canonicalDefinition(definition))
				t.Log("PUBLICATION_EXPECTED_TABLE_DEFINITION=" + c.steps[121].Definition)
				// Inverse ALTER need not reproduce original CHECK literal charsets. Keep
				// definition checks strict. Only this demonstrably empty disposable test
				// leaf may be rebuilt from its exact owned table and four trigger sources.
				var rows int
				if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_publications`).Scan(&rows); e != nil || rows != 0 {
					t.Fatal("empty identified fixture table required for exact reconstruction", mysqlstore.SafeError(e))
				}
				publicationSchemaExec(t, s.Pool(), `DROP TABLE model_publications`)
				publicationSchemaExec(t, s.Pool(), c.steps[121].sql)
				for _, step := range []int{133, 134, 135, 136} {
					publicationSchemaExec(t, s.Pool(), c.steps[step-1].sql)
				}

			case "missing_trigger":
				publicationSchemaExec(t, s.Pool(), c.steps[132].sql)
			case "extra_trigger":
				publicationSchemaExec(t, s.Pool(), `DROP TRIGGER publication_schema_extra_probe`)
			}
			publicationSchemaRun(t, s, c, Readiness)
			if publicationSchemaDigest(t, s.Pool(), layout, false) != before {
				t.Fatal("explicit publication drift repair changed persisted graph/journal")
			}
		})
	}
}

func publicationSchemaDecision(ctx context.Context, tx *sql.Tx, r publicationSchemaGraph, decision string) error {
	defer tx.Rollback()
	execute := func(statement string, args ...any) error { _, e := tx.ExecContext(ctx, statement, args...); return e }
	if decision == "approve" {
		if e := execute("INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES(?,?,?,'Synthetic derivative \u4e16\u754c','qwen3-0.6b',?,CAST(? AS JSON),'staged',?,'merged_lora')", r.preparationUpload, r.tenant, r.workspace, hash(publicationSchemaCatalog(r)), publicationSchemaCatalog(r), r.user); e != nil {
			return e
		}
	}
	if e := execute(publicationSchemaReviewInsert, publicationSchemaReviewArgs(r, decision)...); e != nil {
		return e
	}
	state := "rejected"
	var model any
	if decision == "approve" {
		if e := execute(publicationSchemaDerivationInsert, publicationSchemaDerivationArgs(r)...); e != nil {
			return e
		}
		state = "approved"
		model = r.preparationUpload
	}
	if e := execute(`UPDATE model_preparations SET state=?,review_id=?,derived_model_id=?,revision=revision+1 WHERE id=?`, state, r.review, model, r.preparation); e != nil {
		return e
	}
	if decision == "approve" {
		if e := execute(`INSERT INTO model_publications(model_id,tenant_id,workspace_id) VALUES(?,?,?)`, r.preparationUpload, r.tenant, r.workspace); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func TestActualMySQLPublicationSchemaConcurrency(t *testing.T) {
	t.Run("approve_reject_one_durable_winner", func(t *testing.T) {
		s, c, _ := actualMigration(t)
		prefix := c
		prefix.steps = c.steps[:103]
		publicationSchemaRun(t, s, prefix, Apply)
		old := holdoutSchemaSeed(t, s, holdoutSchemaOldRows(t, s))
		approved := publicationSchemaSeed(t, s, old, true, true)
		rejected := approved
		rejected.review = uuid.NewString()
		rejected.key = uuid.NewString()
		publicationSchemaRun(t, s, c, Apply)
		connections := make([]*sql.Conn, 2)
		transactions := make([]*sql.Tx, 2)
		ids := make([]int, 2)
		for i := range connections {
			var e error
			connections[i], e = s.Pool().Conn(t.Context())
			if e != nil {
				trainingSchemaDigestError(t, e)
			}
			t.Cleanup(func() { _ = connections[i].Close() })
			if e = connections[i].QueryRowContext(t.Context(), `SELECT CONNECTION_ID()`).Scan(&ids[i]); e != nil {
				trainingSchemaDigestError(t, e)
			}
			transactions[i], e = connections[i].BeginTx(t.Context(), nil)
			if e != nil {
				trainingSchemaDigestError(t, e)
			}
			t.Cleanup(func() { _ = transactions[i].Rollback() })
		}
		if ids[0] == ids[1] {
			t.Fatal("two actual transaction connections required")
		}
		type outcome struct {
			decision string
			err      error
		}
		results := make(chan outcome, 2)
		start := make(chan struct{})
		go func() {
			<-start
			results <- outcome{"approve", publicationSchemaDecision(t.Context(), transactions[0], approved, "approve")}
		}()
		go func() {
			<-start
			results <- outcome{"reject", publicationSchemaDecision(t.Context(), transactions[1], rejected, "reject")}
		}()
		close(start)
		winner := ""
		losers := 0
		for range 2 {
			select {
			case result := <-results:
				if result.err == nil {
					if winner != "" {
						t.Fatal("two concurrent decisions committed")
					}
					winner = result.decision
				} else {
					var safe *mysqlstore.Error
					if !errors.As(mysqlstore.SafeError(result.err), &safe) || (safe.Number != 1062 && safe.Number != 1213 && safe.Number != 1205 && safe.Number != 1644) {
						t.Fatal("concurrent loser missing expected safe duplicate/lock/decision category", mysqlstore.SafeError(result.err))
					}
					losers++
				}
			case <-t.Context().Done():
				t.Fatal("concurrent publication did not terminate")
			}
		}
		for _, connection := range connections {
			if e := connection.Close(); e != nil {
				t.Fatal("release completed decision connection", mysqlstore.SafeError(e))
			}
		}
		if winner == "" || losers != 1 {
			t.Fatal("exactly one durable decision winner required")
		}
		var state, review string
		var revision int64
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT state,review_id,revision FROM model_preparations WHERE id=?`, approved.preparation).Scan(&state, &review, &revision); e != nil {
			trainingSchemaDigestError(t, e)
		}
		expectedReview, expectedState := rejected.review, "rejected"
		expectedDerived := 0
		if winner == "approve" {
			expectedReview, expectedState, expectedDerived = approved.review, "approved", 1
		}
		if state != expectedState || review != expectedReview || revision != 3 {
			t.Fatal("concurrent parent rebound or advanced twice")
		}
		for _, table := range []string{"model_preparation_reviews", "model_versions", "model_derivations", "model_publications"} {
			column := "model_id"
			expected := expectedDerived
			id := approved.preparationUpload
			if table == "model_preparation_reviews" {
				column = "preparation_id"
				expected = 1
				id = approved.preparation
			}
			if table == "model_versions" {
				column = "id"
			}
			var count int
			if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE "+column+"=?", id).Scan(&count); e != nil || count != expected {
				t.Fatal("concurrent decision left partial/duplicate publication", table, mysqlstore.SafeError(e))
			}
		}
		publicationSchemaRun(t, s, c, Readiness)
	})
}
func publicationSchemaRecoveryReadback(t *testing.T, s *mysqlstore.Store, c Catalog) {
	t.Helper()
	publicationSchemaRun(t, s, c, Readiness)
	var approved, rejected, derived, reviews, markers int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM model_preparations WHERE state='approved'),(SELECT COUNT(*) FROM model_preparations WHERE state='rejected'),(SELECT COUNT(*) FROM model_versions WHERE kind='merged_lora' AND state='ready'),(SELECT COUNT(*) FROM model_preparation_reviews),(SELECT COUNT(*) FROM model_publications)`).Scan(&approved, &rejected, &derived, &reviews, &markers); e != nil || approved != 1 || rejected != 1 || derived != 1 || reviews != 2 || markers != 1 {
		t.Fatal("complete recovered approval/rejection graph required", mysqlstore.SafeError(e))
	}
	var exact int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM model_publications x JOIN model_versions m ON m.id=x.model_id AND m.tenant_id=x.tenant_id AND m.workspace_id=x.workspace_id JOIN model_derivations d ON d.model_id=m.id AND d.tenant_id=m.tenant_id AND d.workspace_id=m.workspace_id JOIN model_preparation_reviews r ON r.id=d.review_id AND r.tenant_id=d.tenant_id AND r.workspace_id=d.workspace_id JOIN model_preparations p ON p.id=d.preparation_id AND p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id WHERE m.kind='merged_lora' AND m.state='ready' AND m.catalog_digest=d.catalog_digest AND m.manifest=d.catalog AND r.decision='approve' AND r.model_id=d.model_id AND r.artifact_id=d.artifact_id AND r.report_sha256=d.report_sha256 AND r.reviewed_by=m.created_by AND r.model_name=m.name AND p.state='approved' AND p.review_id=r.id AND p.derived_model_id=m.id AND p.current_attempt_id=d.attempt_id AND p.revision=r.expected_revision+1`).Scan(&exact); e != nil || exact != 1 {
		t.Fatal("recovered exact publication bindings required", mysqlstore.SafeError(e))
	}
	t.Log("MYSQL_PUBLICATION_DATA_SHA256=" + publicationSchemaDigest(t, s.Pool(), publicationSchemaLayout(t, s.Pool()), false))
}
func TestActualMySQLPublicationSchemaRecovery(t *testing.T) {
	t.Run("prepare_complete_graph", func(t *testing.T) {
		s, c, _ := actualMigration(t)
		prefix := c
		prefix.steps = c.steps[:103]
		publicationSchemaRun(t, s, prefix, Apply)
		old := holdoutSchemaSeed(t, s, holdoutSchemaOldRows(t, s))
		approved := publicationSchemaSeed(t, s, old, true, true)
		rejected := publicationSchemaSeed(t, s, old, true, true)
		publicationSchemaRun(t, s, c, Apply)
		tx := publicationSchemaTransaction(t, s)
		publicationSchemaApproved(t, tx, approved)
		publicationSchemaPublish(t, tx, approved)
		publicationSchemaExec(t, tx, publicationSchemaReviewInsert, publicationSchemaReviewArgs(rejected, "reject")...)
		publicationSchemaBind(t, tx, rejected, "reject")
		if e := tx.Commit(); e != nil {
			trainingSchemaDigestError(t, e)
		}
		publicationSchemaRecoveryReadback(t, s, c)
	})
	for _, name := range []string{"after_restart_readback", "fresh_logical_restore_readback"} {
		t.Run(name, func(t *testing.T) { s, c, _ := actualMigration(t); publicationSchemaRecoveryReadback(t, s, c) })
	}
}
