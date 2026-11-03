//go:build mysql_integration

package mysqlmigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// Synthetic metadata only: actual SQL/TLS/persistence, not trained models,
// uploaded checkpoint files, approved rights or physical GPU/stop evidence.
type trainingSchemaRows struct {
	datasetSchemaRows
	training, trainingAttempt, checkpoint, artifact, recovery string
}

var trainingSchemaTables = []string{"training_jobs", "training_attempts", "training_events", "training_checkpoints", "training_artifacts", "training_recoveries", "training_uploads", "training_upload_chunks"}

func trainingSchemaPrefixDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	c, e := DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	// Freeze old column ownership from the exact measured59 catalog, so the new
	// GPU id/owner columns cannot change the before/after upgrade digest shape.
	definitions := map[string]string{}
	for _, item := range c.steps[:59] {
		if item.Kind == createTable || item.Kind == alterTable {
			definitions[item.Object] = item.Definition
		}
	}
	tables := make([]string, 0, len(definitions))
	for name := range definitions {
		tables = append(tables, name)
	}
	sort.Strings(tables)
	result := ""
	for _, table := range tables {
		var columns []string
		for _, line := range strings.Split(definitions[table], "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "`") {
				continue
			}
			end := strings.Index(line[1:], "`") + 1
			if end < 2 || !identifier.MatchString(line[1:end]) {
				t.Fatal("exact old measured column identity")
			}
			quoted := line[:end+1]
			typeName := strings.Fields(line[end+1:])[0]
			if strings.Contains(typeName, "binary") || strings.Contains(typeName, "blob") {
				quoted = "HEX(" + quoted + ")"
			}
			columns = append(columns, quoted)
		}
		if len(columns) == 0 {
			t.Fatal("complete old measured column list")
		}
		result += trainingSchemaColumnDigest(t, s, table, columns)
	}
	return hash(result + licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step<=59 ORDER BY step`))
}

func trainingSchemaColumnDigest(t *testing.T, s *mysqlstore.Store, table string, columns []string) string {
	t.Helper()
	keys, e := s.Pool().QueryContext(t.Context(), `SELECT column_name FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name='PRIMARY' ORDER BY seq_in_index`, table)
	if e != nil {
		trainingSchemaDigestError(t, e)
	}
	var order []string
	for keys.Next() {
		var name string
		if e = keys.Scan(&name); e != nil || !identifier.MatchString(name) {
			keys.Close()
			t.Fatal("owned primary-key fingerprint order required")
		}
		order = append(order, "`"+name+"`")
	}
	e = keys.Err()
	keys.Close()
	if e != nil {
		trainingSchemaDigestError(t, e)
	}
	if len(order) == 0 {
		t.Fatal("owned table primary key required for complete row fingerprint")
	}
	// Sorting full16MiB JSON produced actual MySQL1038/HY001. Walk the exact
	// primary key instead; hash every canonical row byte without changing server
	// sort buffers, dropping fields or retaining all large rows in Go memory.
	rows, e := s.Pool().QueryContext(t.Context(), "SELECT JSON_ARRAY("+strings.Join(columns, ",")+") FROM "+table+" FORCE INDEX(PRIMARY) ORDER BY "+strings.Join(order, ","))
	if e != nil {
		trainingSchemaDigestError(t, e)
	}
	defer rows.Close()
	fingerprint := sha256.New()
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			trainingSchemaDigestError(t, e)
		}
		row := sha256.Sum256(raw)
		fingerprint.Write(row[:])
	}
	if e = rows.Err(); e != nil {
		trainingSchemaDigestError(t, e)
	}
	return hex.EncodeToString(fingerprint.Sum(nil))
}

func trainingSchemaDigestError(t *testing.T, e error) {
	t.Helper()
	safe := mysqlstore.SafeError(e)
	var value *mysqlstore.Error
	if errors.As(safe, &value) {
		t.Fatalf("TRAINING_DIGEST_QUERY_FAILED code=%s number=%d state_hex=%x", safe.Error(), value.Number, value.SQLState)
	}
	t.Fatal("TRAINING_DIGEST_QUERY_FAILED classified_error_unavailable")
}

func trainingSchemaDataDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	result := trainingSchemaPrefixDigest(t, s)
	for _, table := range trainingSchemaTables {
		rows, e := s.Pool().QueryContext(t.Context(), `SELECT column_name,data_type FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`, table)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		var columns []string
		for rows.Next() {
			var name, kind string
			if e = rows.Scan(&name, &kind); e != nil || !identifier.MatchString(name) {
				t.Fatal("owned training digest column", mysqlstore.SafeError(e))
			}
			quoted := "`" + name + "`"
			if strings.Contains(kind, "binary") || strings.Contains(kind, "blob") {
				quoted = "HEX(" + quoted + ")"
			}
			columns = append(columns, quoted)
		}
		e = rows.Err()
		rows.Close()
		if e != nil || len(columns) == 0 {
			t.Fatal("complete training digest columns", mysqlstore.SafeError(e))
		}
		result += trainingSchemaColumnDigest(t, s, table, columns)
	}
	result += licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,deployment_id,training_job_id) FROM gpu_leases ORDER BY id`)
	result += licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step>=60 ORDER BY step`)
	return hash(result)
}

func trainingSchemaSeed(t *testing.T, s *mysqlstore.Store, old datasetSchemaRows) trainingSchemaRows {
	t.Helper()
	r := trainingSchemaRows{old, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fingerprint := make([]byte, 32)
	fingerprint[0] = 255
	migrationExec(t, s, "INSERT INTO training_jobs(id,tenant_id,workspace_id,node_id,current_node_id,model_id,dataset_id,dataset_version_id,gpu_uuid,configuration,configuration_sha256,execution_recipe,dataset_manifest_sha256,model_catalog_sha256,created_by,requested_by,requested_audience,requested_epoch,reason,creation_key,creation_fingerprint,state,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,JSON_OBJECT('label','\u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629'),?,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,?,?,?, 'admin',1,'Synthetic training schema only',?,?,'queued',?,?)", r.training, r.tenant, r.workspace, r.node, r.node, r.model, r.dataset, r.version, r.gpu, digest, digest, digest, r.user, r.user, uuid.NewString(), fingerprint, r.heartbeat, r.heartbeat)
	migrationExec(t, s, `INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,attempt_number,state,requested_by,requested_audience,requested_epoch,input_inventory,last_event_sequence,last_global_step,created_at,updated_at) VALUES(?,?,?,?,?,1,'succeeded',?,'admin',1,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),1,1,?,?)`, r.trainingAttempt, r.tenant, r.workspace, r.training, r.node, r.user, r.heartbeat, r.heartbeat)
	migrationExec(t, s, `INSERT INTO training_events(tenant_id,workspace_id,job_id,attempt_id,sequence,kind,global_step,payload,canonical_sha256,recorded_at) VALUES(?,?,?,?,1,'checkpoint',1,JSON_OBJECT('synthetic_schema',CAST('true' AS JSON)),?,?)`, r.tenant, r.workspace, r.training, r.trainingAttempt, digest, r.heartbeat)
	for _, item := range []struct{ id, kind string }{{r.checkpoint, "checkpoint"}, {r.artifact, "artifact"}} {
		migrationExec(t, s, `INSERT INTO training_uploads(id,tenant_id,workspace_id,job_id,attempt_id,kind,global_step,manifest_sha256,manifest,state,manifest_bytes,created_at,updated_at) VALUES(?,?,?,?,?,?,1,?,JSON_OBJECT(),'accepted',?,?,?)`, item.id, r.tenant, r.workspace, r.training, r.trainingAttempt, item.kind, digest, []byte("{}"), r.heartbeat, r.heartbeat)
	}
	migrationExec(t, s, `INSERT INTO training_upload_chunks(upload_id,asset,byte_offset,size_bytes,sha256) VALUES(?,'synthetic.bin',0,2,?)`, r.checkpoint, digest)
	migrationExec(t, s, `INSERT INTO training_checkpoints(id,tenant_id,workspace_id,job_id,attempt_id,global_step,receipt_sha256,receipt,file_inventory,storage_key,created_at) VALUES(?,?,?,?,?,1,?,JSON_OBJECT(),JSON_OBJECT(),?,?)`, r.checkpoint, r.tenant, r.workspace, r.training, r.trainingAttempt, digest, "checkpoints/"+r.training+"/"+r.trainingAttempt+"/"+r.checkpoint, r.heartbeat)
	migrationExec(t, s, `INSERT INTO training_artifacts(id,tenant_id,workspace_id,job_id,attempt_id,manifest_sha256,manifest,file_inventory,storage_key,created_at) VALUES(?,?,?,?,?,?,JSON_OBJECT(),JSON_OBJECT(),?,?)`, r.artifact, r.tenant, r.workspace, r.training, r.trainingAttempt, digest, "artifacts/"+r.training+"/"+r.trainingAttempt+"/"+r.artifact, r.heartbeat)
	migrationExec(t, s, `INSERT INTO training_recoveries(id,tenant_id,workspace_id,job_id,previous_attempt_id,checkpoint_id,previous_node_id,next_node_id,mode,requested_by,reason,creation_key,creation_fingerprint,created_at) VALUES(?,?,?,?,?,?,?,?,'checkpoint',?,'Synthetic recovery metadata',?,?,?)`, r.recovery, r.tenant, r.workspace, r.training, r.trainingAttempt, r.checkpoint, r.node, r.node, r.user, uuid.NewString(), fingerprint, r.heartbeat)
	migrationExec(t, s, `UPDATE training_jobs SET state='succeeded',current_attempt_id=?,accepted_artifact_id=?,attempt_number=1,finished_at=? WHERE id=?`, r.trainingAttempt, r.artifact, r.heartbeat, r.training)
	return r
}

func trainingSchemaReady(t *testing.T) (*mysqlstore.Store, Catalog, trainingSchemaRows) {
	t.Helper()
	s, c, old := datasetSchemaReady(t)
	return s, c, trainingSchemaSeed(t, s, old)
}

func trainingSchemaAssertRows(t *testing.T, s *mysqlstore.Store, r trainingSchemaRows) {
	t.Helper()
	for _, table := range trainingSchemaTables {
		var count int
		want := 1
		if table == "training_uploads" {
			want = 2
		}
		if e := s.Pool().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); e != nil || count != want {
			t.Fatal("complete actual training graph count", table, mysqlstore.SafeError(e))
		}
	}
	var raw, fingerprint []byte
	var stamp time.Time
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT configuration,creation_fingerprint,created_at FROM training_jobs WHERE id=?`, r.training).Scan(&raw, &fingerprint, &stamp); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value["label"] != "\u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629" || len(fingerprint) != 32 || fingerprint[0] != 255 || stamp.Location() != time.UTC || stamp.Nanosecond() != 123456000 {
		t.Fatal("training JSON/Unicode/binary/UTC microsecond round trip")
	}
	var id, deployment, state, node, gpu string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT id,deployment_id,state,reserved_node_id,reserved_gpu_uuid FROM gpu_leases WHERE deployment_id=?`, r.deployment).Scan(&id, &deployment, &state, &node, &gpu); e != nil || id != deployment || state != "held" || node != r.node || gpu != r.gpu {
		t.Fatal("migration lost existing inference physical reservation or deterministic identity", mysqlstore.SafeError(e))
	}
}

func trainingSchemaRefuses(t *testing.T, s *mysqlstore.Store, number uint16, statement string, args ...any) {
	t.Helper()
	before := trainingSchemaDataDigest(t, s)
	_, e := s.Pool().ExecContext(t.Context(), statement, args...)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) {
		t.Fatal("actual training refusal missing safe numeric category")
	}
	if safe.Number != number {
		t.Fatalf("TRAINING_REFUSAL_CATEGORY_MISMATCH expected_number=%d actual_number=%d state_hex=%x code=%s", number, safe.Number, safe.SQLState, safe.Error())
	}
	if trainingSchemaDataDigest(t, s) != before {
		t.Fatal("refused training statement changed persisted data or journal")
	}
}

func TestActualMySQLTrainingSchemaFresh(t *testing.T) {
	s, c, r := trainingSchemaReady(t)
	trainingSchemaAssertRows(t, s, r)
	before, events := trainingSchemaDataDigest(t, s), 0
	result, e := Run(t.Context(), s, c, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !result.Ready || events != 0 || trainingSchemaDataDigest(t, s) != before {
		t.Fatal("current training schema no-op changed accepted state", e)
	}
	migrationRun(t, s, c, Readiness)
}

func TestActualMySQLTrainingSchemaUpgrade(t *testing.T) {
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:59]
	migrationRun(t, s, prefix, Apply)
	old := datasetSchemaSeed(t, s, modelInferenceSeed(t, s, nodeSchemaSeedRows(t, s, nodeSchemaSeedOld(t, s))))
	before := trainingSchemaPrefixDigest(t, s)
	migrationRun(t, s, c, Apply)
	if trainingSchemaPrefixDigest(t, s) != before {
		t.Fatal("59-to85 changed old business rows or journal")
	}
	r := trainingSchemaSeed(t, s, old)
	trainingSchemaAssertRows(t, s, r)
	migrationRun(t, s, c, Readiness)
	t.Log("TRAINING_SCHEMA_POPULATED59_UPGRADE_PRESERVED old_business_journal_sha256=" + before)
}
