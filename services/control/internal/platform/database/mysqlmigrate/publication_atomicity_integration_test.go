//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// This is an isolated engine experiment. These tables and the injected failure
// are test fixtures, never product DDL, actual human review or model execution.
var publicationProbeDDL = []string{
	`CREATE TABLE model_publish_probe_models(id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,state VARCHAR(16) NOT NULL CHECK(state IN('staged','ready')),inject_failure boolean NOT NULL DEFAULT false,UNIQUE(tenant_id,workspace_id,id)) ENGINE=InnoDB`,
	`CREATE TABLE model_publish_probe_reviews(id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,artifact_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,model_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,decision VARCHAR(8) NOT NULL CHECK(decision IN('approve','reject')),model_name VARCHAR(100) NOT NULL,UNIQUE(tenant_id,workspace_id,id),FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_publish_probe_models(tenant_id,workspace_id,id),CHECK((decision='approve' AND model_id IS NOT NULL AND model_id=artifact_id AND CHAR_LENGTH(model_name)>0) OR(decision='reject' AND model_id IS NULL AND model_name=''))) ENGINE=InnoDB`,
	`CREATE TABLE model_publish_probe_preparations(id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,state VARCHAR(24) NOT NULL CHECK(state IN('awaiting_review','approved','rejected')),review_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,model_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,UNIQUE(tenant_id,workspace_id,id),FOREIGN KEY(tenant_id,workspace_id,review_id) REFERENCES model_publish_probe_reviews(tenant_id,workspace_id,id),FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_publish_probe_models(tenant_id,workspace_id,id),CHECK((state='awaiting_review' AND review_id IS NULL AND model_id IS NULL) OR(state='approved' AND review_id IS NOT NULL AND model_id IS NOT NULL) OR(state='rejected' AND review_id IS NOT NULL AND model_id IS NULL))) ENGINE=InnoDB`,
	`CREATE TABLE model_publish_probe_derivations(model_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,preparation_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,review_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,artifact_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,CONSTRAINT publish_probe_artifact_identity CHECK(model_id=artifact_id),UNIQUE(tenant_id,workspace_id,model_id),FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_publish_probe_models(tenant_id,workspace_id,id),FOREIGN KEY(tenant_id,workspace_id,preparation_id) REFERENCES model_publish_probe_preparations(tenant_id,workspace_id,id),FOREIGN KEY(tenant_id,workspace_id,review_id) REFERENCES model_publish_probe_reviews(tenant_id,workspace_id,id)) ENGINE=InnoDB`,
	`CREATE TABLE model_publish_probe_markers(model_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_publish_probe_derivations(tenant_id,workspace_id,model_id)) ENGINE=InnoDB`,
	`CREATE TRIGGER publish_probe_model_insert BEFORE INSERT ON model_publish_probe_models FOR EACH ROW BEGIN IF NEW.state<>'staged' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_STAGED_MODEL_REQUIRED'; END IF; END`,
	`CREATE TRIGGER publish_probe_model_update BEFORE UPDATE ON model_publish_probe_models FOR EACH ROW BEGIN
 IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.workspace_id<=>OLD.workspace_id) OR NOT(NEW.inject_failure<=>OLD.inject_failure) OR NEW.state<>'ready' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_MODEL_IDENTITY_IMMUTABLE'; END IF;
 IF NOT EXISTS(SELECT 1 FROM model_publish_probe_markers marker JOIN model_publish_probe_derivations d ON d.model_id=marker.model_id AND d.tenant_id=marker.tenant_id AND d.workspace_id=marker.workspace_id JOIN model_publish_probe_reviews r ON r.id=d.review_id AND r.tenant_id=d.tenant_id AND r.workspace_id=d.workspace_id JOIN model_publish_probe_preparations p ON p.id=d.preparation_id AND p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id WHERE marker.model_id=NEW.id AND marker.tenant_id=NEW.tenant_id AND marker.workspace_id=NEW.workspace_id AND r.decision='approve' AND r.model_id=NEW.id AND r.artifact_id=d.artifact_id AND p.state='approved' AND p.review_id=r.id AND p.model_id=NEW.id) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_COMPLETE_PUBLICATION_REQUIRED'; END IF;
 END`,
	`CREATE TRIGGER publish_probe_marker_before BEFORE INSERT ON model_publish_probe_markers FOR EACH ROW BEGIN
 IF NOT EXISTS(SELECT 1 FROM model_publish_probe_derivations d JOIN model_publish_probe_models m ON m.id=d.model_id AND m.tenant_id=d.tenant_id AND m.workspace_id=d.workspace_id JOIN model_publish_probe_reviews r ON r.id=d.review_id AND r.tenant_id=d.tenant_id AND r.workspace_id=d.workspace_id JOIN model_publish_probe_preparations p ON p.id=d.preparation_id AND p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id WHERE d.model_id=NEW.model_id AND d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id AND m.state='staged' AND r.decision='approve' AND r.model_id=m.id AND r.artifact_id=d.artifact_id AND p.state='approved' AND p.review_id=r.id AND p.model_id=m.id) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_APPROVED_GRAPH_REQUIRED'; END IF;
 END`,
	`CREATE TRIGGER publish_probe_marker_after AFTER INSERT ON model_publish_probe_markers FOR EACH ROW BEGIN UPDATE model_publish_probe_models SET state='ready' WHERE id=NEW.model_id AND tenant_id=NEW.tenant_id AND workspace_id=NEW.workspace_id AND state='staged'; IF ROW_COUNT()<>1 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_ONE_MODEL_REQUIRED'; END IF; END`,
	`CREATE TRIGGER publish_probe_prep_update BEFORE UPDATE ON model_publish_probe_preparations FOR EACH ROW BEGIN
 IF OLD.state IN('approved','rejected') THEN
  IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.workspace_id<=>OLD.workspace_id) OR NOT(NEW.state<=>OLD.state) OR NOT(NEW.review_id<=>OLD.review_id) OR NOT(NEW.model_id<=>OLD.model_id) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_TERMINAL_DECISION_IMMUTABLE'; END IF;
 ELSEIF NEW.state='approved' THEN
  IF NOT EXISTS(SELECT 1 FROM model_publish_probe_derivations d JOIN model_publish_probe_reviews r ON r.id=d.review_id AND r.tenant_id=d.tenant_id AND r.workspace_id=d.workspace_id JOIN model_publish_probe_models m ON m.id=d.model_id AND m.tenant_id=d.tenant_id AND m.workspace_id=d.workspace_id WHERE d.preparation_id=NEW.id AND d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id AND d.model_id=NEW.model_id AND d.review_id=NEW.review_id AND r.decision='approve' AND r.model_id=m.id AND r.artifact_id=d.artifact_id AND m.state='staged') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_REVIEW_BINDING_REQUIRED'; END IF;
 ELSEIF NEW.state='rejected' THEN
  IF NOT EXISTS(SELECT 1 FROM model_publish_probe_reviews r WHERE r.id=NEW.review_id AND r.tenant_id=NEW.tenant_id AND r.workspace_id=NEW.workspace_id AND r.decision='reject' AND r.model_id IS NULL) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_REJECTION_BINDING_REQUIRED'; END IF;
 END IF;
 END`,
	// Explicit test-only fault, after the real nested readiness update. It proves
	// statement rollback of both tables; no production failure switch is added.
	`CREATE TRIGGER publish_probe_injected_failure AFTER UPDATE ON model_publish_probe_models FOR EACH ROW BEGIN IF NEW.inject_failure AND NEW.state='ready' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_INJECTED_NESTED_FAILURE'; END IF; END`,
}

type publicationProbeDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type publicationProbeRows struct{ tenant, workspace, model, review, preparation string }

func publicationProbeExec(t *testing.T, db publicationProbeDB, statement string, arguments ...any) {
	t.Helper()
	if _, e := db.ExecContext(t.Context(), statement, arguments...); e != nil {
		var safe *mysqlstore.Error
		if errors.As(mysqlstore.SafeError(e), &safe) {
			t.Fatalf("publication experiment statement refused number=%d state_hex=%x code=%s", safe.Number, safe.SQLState, safe.Error())
		}
		t.Fatal("actual publication engine experiment", mysqlstore.SafeError(e))
	}
}

func publicationProbeModel(t *testing.T, db publicationProbeDB, r publicationProbeRows, injected bool) {
	t.Helper()
	publicationProbeExec(t, db, `INSERT INTO model_publish_probe_models(id,tenant_id,workspace_id,state,inject_failure) VALUES(?,?,?,'staged',?)`, r.model, r.tenant, r.workspace, injected)
}

func publicationProbeGraph(t *testing.T, db publicationProbeDB, decision string, injected bool) publicationProbeRows {
	t.Helper()
	r := publicationProbeRows{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	publicationProbeModel(t, db, r, injected)
	var model any = r.model
	name := "Synthetic publication experiment"
	if decision == "reject" {
		model, name = nil, ""
	}
	publicationProbeExec(t, db, `INSERT INTO model_publish_probe_reviews(id,tenant_id,workspace_id,artifact_id,model_id,decision,model_name) VALUES(?,?,?,?,?,?,?)`, r.review, r.tenant, r.workspace, r.model, model, decision, name)
	publicationProbeExec(t, db, `INSERT INTO model_publish_probe_preparations(id,tenant_id,workspace_id,state) VALUES(?,?,?,'awaiting_review')`, r.preparation, r.tenant, r.workspace)
	publicationProbeExec(t, db, `INSERT INTO model_publish_probe_derivations(model_id,tenant_id,workspace_id,preparation_id,review_id,artifact_id) VALUES(?,?,?,?,?,?)`, r.model, r.tenant, r.workspace, r.preparation, r.review, r.model)
	state := "approved"
	if decision == "reject" {
		state = "rejected"
	}
	publicationProbeExec(t, db, `UPDATE model_publish_probe_preparations SET state=?,review_id=?,model_id=? WHERE id=?`, state, r.review, model, r.preparation)
	return r
}

const publicationProbeMarker = `INSERT INTO model_publish_probe_markers(model_id,tenant_id,workspace_id) VALUES(?,?,?)`

func publicationProbePublish(t *testing.T, db publicationProbeDB, r publicationProbeRows) {
	t.Helper()
	publicationProbeExec(t, db, publicationProbeMarker, r.model, r.tenant, r.workspace)
}

func publicationProbeDigest(t *testing.T, db publicationProbeDB) string {
	t.Helper()
	digest := sha256.New()
	for _, item := range []struct{ table, columns, key string }{
		{"models", "id,tenant_id,workspace_id,state,inject_failure", "id"},
		{"reviews", "id,tenant_id,workspace_id,artifact_id,model_id,decision,model_name", "id"},
		{"preparations", "id,tenant_id,workspace_id,state,review_id,model_id", "id"},
		{"derivations", "model_id,tenant_id,workspace_id,preparation_id,review_id,artifact_id", "model_id"},
		{"markers", "model_id,tenant_id,workspace_id", "model_id"},
	} {
		digest.Write([]byte(item.table))
		digest.Write([]byte{0})
		rows, e := db.QueryContext(t.Context(), "SELECT JSON_ARRAY("+item.columns+") FROM model_publish_probe_"+item.table+" ORDER BY "+item.key)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				t.Fatal(mysqlstore.SafeError(e))
			}
			digest.Write(raw)
			digest.Write([]byte{0})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func publicationProbeRefuses(t *testing.T, db publicationProbeDB, number uint16, statement string, arguments ...any) {
	t.Helper()
	before := publicationProbeDigest(t, db)
	_, e := db.ExecContext(t.Context(), statement, arguments...)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != number {
		t.Fatalf("publication experiment refusal category expected=%d actual=%v", number, mysqlstore.SafeError(e))
	}
	if publicationProbeDigest(t, db) != before {
		t.Fatal("refused publication changed complete experimental data")
	}
}

func publicationProbeVisible(t *testing.T, db publicationProbeDB, r publicationProbeRows) int {
	t.Helper()
	var count int
	query := `SELECT COUNT(*) FROM model_publish_probe_models m JOIN model_publish_probe_markers marker ON marker.model_id=m.id AND marker.tenant_id=m.tenant_id AND marker.workspace_id=m.workspace_id JOIN model_publish_probe_derivations d ON d.model_id=m.id AND d.tenant_id=m.tenant_id AND d.workspace_id=m.workspace_id JOIN model_publish_probe_reviews review ON review.id=d.review_id AND review.tenant_id=d.tenant_id AND review.workspace_id=d.workspace_id JOIN model_publish_probe_preparations p ON p.id=d.preparation_id AND p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id WHERE m.id=? AND m.tenant_id=? AND m.workspace_id=? AND m.state='ready' AND review.decision='approve' AND review.model_id=m.id AND review.artifact_id=d.artifact_id AND p.state='approved' AND p.model_id=m.id AND p.review_id=review.id`
	if e := db.QueryRowContext(t.Context(), query, r.model, r.tenant, r.workspace).Scan(&count); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	return count
}

func TestActualMySQLPublicationAtomicity(t *testing.T) {
	s, _, _ := actualMigration(t)
	db := s.Pool()
	for index, statement := range publicationProbeDDL {
		t.Logf("PUBLICATION_EXPERIMENT_DDL_ORDINAL=%d", index+1)
		publicationProbeExec(t, db, statement)
	}
	for _, item := range []struct{ table, key string }{{"models", "id"}, {"reviews", "id"}, {"preparations", "id"}, {"derivations", "model_id"}, {"markers", "model_id"}} {
		for _, event := range []string{"UPDATE", "DELETE"} {
			if event == "UPDATE" && (item.table == "models" || item.table == "preparations") {
				continue
			}
			name := "publish_probe_" + item.table + "_" + strings.ToLower(event)
			publicationProbeExec(t, db, "CREATE TRIGGER "+name+" BEFORE "+event+" ON model_publish_probe_"+item.table+" FOR EACH ROW BEGIN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PROBE_PROVENANCE_IMMUTABLE'; END")
		}
	}
	t.Run("strict_profile", func(t *testing.T) {
		var version, zone, charset, collation string
		var checks int
		if e := db.QueryRowContext(t.Context(), `SELECT VERSION(),@@foreign_key_checks,@@session.time_zone,@@character_set_connection,@@collation_connection`).Scan(&version, &checks, &zone, &charset, &collation); e != nil || version != "8.4.12" || checks != 1 || zone != "+00:00" || charset != "utf8mb4" || collation != "utf8mb4_0900_bin" {
			t.Fatal("exact verified Oracle/session profile required", mysqlstore.SafeError(e))
		}
	})
	t.Run("ready_insert_refused", func(t *testing.T) {
		r := publicationProbeRows{tenant: uuid.NewString(), workspace: uuid.NewString(), model: uuid.NewString()}
		publicationProbeRefuses(t, db, 1644, `INSERT INTO model_publish_probe_models(id,tenant_id,workspace_id,state) VALUES(?,?,?,'ready')`, r.model, r.tenant, r.workspace)
		publicationProbeModel(t, db, r, false)
		publicationProbeRefuses(t, db, 1644, `UPDATE model_publish_probe_models SET state='ready' WHERE id=?`, r.model)
	})
	t.Run("staging_not_published", func(t *testing.T) {
		r := publicationProbeRows{tenant: uuid.NewString(), workspace: uuid.NewString(), model: uuid.NewString()}
		publicationProbeModel(t, db, r, false)
		if publicationProbeVisible(t, db, r) != 0 {
			t.Fatal("staged model was published")
		}
	})
	for _, null := range []bool{true, false} {
		name := "approval_identity_mismatch_refused"
		if null {
			name = "approval_NULL_refused"
		}
		t.Run(name, func(t *testing.T) {
			r := publicationProbeRows{tenant: uuid.NewString(), workspace: uuid.NewString(), model: uuid.NewString()}
			publicationProbeModel(t, db, r, false)
			var model any
			if !null {
				peer := r
				peer.model = uuid.NewString()
				publicationProbeModel(t, db, peer, false)
				model = peer.model
			}
			publicationProbeRefuses(t, db, 3819, `INSERT INTO model_publish_probe_reviews(id,tenant_id,workspace_id,artifact_id,model_id,decision,model_name) VALUES(?,?,?,?,?,'approve','Synthetic approval identity')`, uuid.NewString(), r.tenant, r.workspace, r.model, model)
		})
	}
	t.Run("rejected_review_refuses_publication", func(t *testing.T) {
		r := publicationProbeGraph(t, db, "reject", false)
		publicationProbeRefuses(t, db, 1644, publicationProbeMarker, r.model, r.tenant, r.workspace)
	})
	t.Run("incomplete_graph_refused", func(t *testing.T) {
		r := publicationProbeRows{tenant: uuid.NewString(), workspace: uuid.NewString(), model: uuid.NewString()}
		publicationProbeModel(t, db, r, false)
		publicationProbeRefuses(t, db, 1644, publicationProbeMarker, r.model, r.tenant, r.workspace)
	})
	t.Run("cross_scope_refused", func(t *testing.T) {
		r := publicationProbeGraph(t, db, "approve", false)
		publicationProbeRefuses(t, db, 1644, publicationProbeMarker, r.model, r.tenant, uuid.NewString())
		publicationProbeRefuses(t, db, 1644, publicationProbeMarker, r.model, uuid.NewString(), r.workspace)
	})
	t.Run("complete_graph_publishes_once", func(t *testing.T) {
		r := publicationProbeGraph(t, db, "approve", false)
		publicationProbePublish(t, db, r)
		if publicationProbeVisible(t, db, r) != 1 {
			t.Fatal("nested marker/model publication did not persist")
		}
		publicationProbeRefuses(t, db, 1644, publicationProbeMarker, r.model, r.tenant, r.workspace)
	})
	t.Run("induced_nested_failure_statement_atomic", func(t *testing.T) {
		r := publicationProbeGraph(t, db, "approve", true)
		publicationProbeRefuses(t, db, 1644, publicationProbeMarker, r.model, r.tenant, r.workspace)
		var state string
		if e := db.QueryRowContext(t.Context(), `SELECT state FROM model_publish_probe_models WHERE id=?`, r.model).Scan(&state); e != nil || state != "staged" || publicationProbeVisible(t, db, r) != 0 {
			t.Fatal("nested failure leaked model/marker state", mysqlstore.SafeError(e))
		}
	})
	t.Run("caller_rollback_removes_every_row", func(t *testing.T) {
		before := publicationProbeDigest(t, db)
		tx, e := db.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		r := publicationProbeGraph(t, tx, "approve", false)
		publicationProbePublish(t, tx, r)
		if publicationProbeVisible(t, tx, r) != 1 {
			t.Fatal("publication unavailable inside owning transaction")
		}
		if e = tx.Rollback(); e != nil || publicationProbeDigest(t, db) != before {
			t.Fatal("trigger escaped owning transaction rollback", mysqlstore.SafeError(e))
		}
	})
	t.Run("complete_commit_new_connection_readback", func(t *testing.T) {
		first, e := db.Conn(t.Context())
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer first.Close()
		var firstID int64
		if e = first.QueryRowContext(t.Context(), `SELECT CONNECTION_ID()`).Scan(&firstID); e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		tx, e := first.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		r := publicationProbeGraph(t, tx, "approve", false)
		publicationProbePublish(t, tx, r)
		if e = tx.Commit(); e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		second, e := db.Conn(t.Context())
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer second.Close()
		var secondID int64
		if e = second.QueryRowContext(t.Context(), `SELECT CONNECTION_ID()`).Scan(&secondID); e != nil || firstID == secondID || publicationProbeVisible(t, second, r) != 1 {
			t.Fatal("committed complete graph not visible on independent actual connection", mysqlstore.SafeError(e))
		}
	})
	t.Run("published_graph_sealed", func(t *testing.T) {
		r := publicationProbeGraph(t, db, "approve", false)
		publicationProbePublish(t, db, r)
		for _, item := range []struct{ table, field, value, key, id string }{
			{"models", "state", "staged", "id", r.model}, {"reviews", "model_name", "Changed", "id", r.review},
			{"preparations", "state", "awaiting_review", "id", r.preparation}, {"derivations", "artifact_id", uuid.NewString(), "model_id", r.model}, {"markers", "workspace_id", uuid.NewString(), "model_id", r.model},
		} {
			publicationProbeRefuses(t, db, 1644, "UPDATE model_publish_probe_"+item.table+" SET "+item.field+"=? WHERE "+item.key+"=?", item.value, item.id)
			publicationProbeRefuses(t, db, 1644, "DELETE FROM model_publish_probe_"+item.table+" WHERE "+item.key+"=?", item.id)
		}
	})
	t.Run("orphan_staged_not_ready", func(t *testing.T) {
		r := publicationProbeGraph(t, db, "approve", false)
		var count int
		query := `SELECT COUNT(*) FROM model_publish_probe_models m JOIN model_publish_probe_derivations d ON d.model_id=m.id AND d.tenant_id=m.tenant_id AND d.workspace_id=m.workspace_id JOIN model_publish_probe_preparations p ON p.id=d.preparation_id AND p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id LEFT JOIN model_publish_probe_markers marker ON marker.model_id=m.id WHERE m.id=? AND m.state='staged' AND p.state='approved' AND marker.model_id IS NULL`
		if e := db.QueryRowContext(t.Context(), query, r.model).Scan(&count); e != nil || count != 1 || publicationProbeVisible(t, db, r) != 0 {
			t.Fatal("staged incomplete commit must remain detected/unpublished", mysqlstore.SafeError(e))
		}
	})
}
