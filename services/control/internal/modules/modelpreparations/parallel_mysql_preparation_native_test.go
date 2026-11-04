package modelpreparations

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type parallelRetainedRead struct {
	dialect   transaction.Dialect
	statement transaction.Statement
	row       transaction.Row
	reads     int
}

func (q *parallelRetainedRead) Dialect() transaction.Dialect { return q.dialect }
func (q *parallelRetainedRead) QueryRow(_ context.Context, s transaction.Statement) transaction.Row {
	q.reads++
	q.statement = s
	return q.row
}
func (*parallelRetainedRead) Exec(context.Context, transaction.Statement) (int64, error) {
	panic("retained read attempted mutation")
}
func (*parallelRetainedRead) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	panic("retained read attempted additional query")
}

type parallelRetainedRow struct {
	raw     []byte
	attempt int
	expiry  int64
	err     error
}

func (r parallelRetainedRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 5 {
		return errors.New("unexpected retained projection")
	}
	expired, e := driver.Bool.ConvertValue(r.expiry)
	if e != nil {
		return e
	}
	*dest[0].(*string) = "running"
	*dest[1].(**string) = nil
	*dest[2].(*int) = r.attempt
	*dest[3].(*[]byte) = append([]byte(nil), r.raw...)
	*dest[4].(*bool) = expired.(bool)
	return nil
}

func parallelRetainedCommand(t *testing.T) wire.Command {
	t.Helper()
	id := "11111111-1111-4111-8111-111111111111"
	hash := strings.Repeat("a", 64)
	image := "sha256:" + strings.Repeat("b", 64)
	config := wire.DefaultConfiguration()
	configSHA, e := config.Digest()
	if e != nil {
		t.Fatal(e)
	}
	train := training.DefaultConfiguration()
	trainSHA, e := train.Digest()
	if e != nil {
		t.Fatal(e)
	}
	c := wire.Command{SchemaVersion: 1, Scope: wire.Scope{TenantID: id, WorkspaceID: "22222222-2222-4222-8222-222222222222", PreparationID: id}, AttemptID: id, AttemptNumber: 1, ModelID: id, ModelCatalogSHA256: wire.ApprovedModelCatalogSHA256, TrainingJobID: id, TrainingAttemptID: id, TrainingConfiguration: train, TrainingConfigurationSHA256: trainSHA, HeldoutID: id, HeldoutManifestSHA256: hash, GPUUUID: "GPU-" + id, Configuration: config, ConfigurationSHA256: configSHA,
		Recipe: wire.ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: wire.ImageRepository + "@sha256:" + hash, Identity: training.ExecutionIdentity{Profile: training.ProductionProfile, Python: training.PythonVersion, WorkerSourceSHA256: hash, ImageID: &image}, Resources: wire.DefaultResources(), DataLimits: dataset.Limits{MaximumBytes: 1048576, MaximumRows: 256, MaximumLineBytes: 16384, MaximumTextBytes: 4096, MaximumSourceBytes: 256, MaximumErrors: 10}, RecipeSHA256: wire.RecipeDigest(), ProcessorSHA256: wire.ProcessorDigest()},
		Inputs: wire.InputInventory{AdapterFiles: []dataset.Asset{{Path: "README.md", SizeBytes: 248, SHA256: hash}, {Path: "adapter_config.json", SizeBytes: 1092, SHA256: hash}, {Path: "adapter_model.safetensors", SizeBytes: 2308432, SHA256: hash}}, Heldout: dataset.Asset{Path: "heldout.jsonl", SizeBytes: 512, SHA256: hash}}}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	return c
}

// This borrowed-executor regression is not a native vendor execution receipt.
func TestParallelMySQLPreparationRetainedTaskSelectedDialect(t *testing.T) {
	c := parallelRetainedCommand(t)
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	n := nodes.Node{TenantID: c.Scope.TenantID, WorkspaceID: c.Scope.WorkspaceID, ID: "33333333-3333-4333-8333-333333333333"}
	task := "44444444-4444-4444-8444-444444444444"
	queries := map[transaction.Dialect]string{
		transaction.PostgreSQL: `SELECT state,error_code,attempt,payload,deadline_at<=clock_timestamp() OR state='running' AND COALESCE(lease_expires_at<=clock_timestamp(),true) FROM node_tasks WHERE tenant_id=$1 AND workspace_id=$2 AND node_id=$3 AND id=$4 AND operation=$5 FOR UPDATE`,
		transaction.MySQL:      `SELECT state,error_code,attempt,payload,deadline_at<=UTC_TIMESTAMP(6) OR state='running' AND COALESCE(lease_expires_at<=UTC_TIMESTAMP(6),true) FROM node_tasks WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=? AND operation=? FOR UPDATE`,
		transaction.Dameng:     `SELECT "STATE","ERROR_CODE","ATTEMPT","PAYLOAD",CASE WHEN "DEADLINE_AT"<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) OR ("STATE"='running' AND ("LEASE_EXPIRES_AT" IS NULL OR "LEASE_EXPIRES_AT"<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)))) THEN 1 ELSE 0 END FROM "NODE_TASKS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "NODE_ID"=? AND "ID"=? AND "OPERATION"=? FOR UPDATE`,
	}
	for dialect, text := range queries {
		t.Run(string(dialect), func(t *testing.T) {
			q := &parallelRetainedRead{dialect: dialect, row: parallelRetainedRow{raw: raw, attempt: 1, expiry: 1}}
			got, e := retainedPreparationTask(context.Background(), q, n, task)
			if e != nil || got.state != "running" || got.attempt != 1 || !got.expired || !wire.SameCommand(got.command, c) {
				t.Fatal("retained command/projection changed", e)
			}
			expected := transaction.SQL(dialect, text, n.TenantID, n.WorkspaceID, n.ID, task, wire.RunOperation)
			if q.reads != 1 || !reflect.DeepEqual(q.statement, expected) {
				t.Fatal("selected vendor SQL or scoped argument order changed")
			}
		})
	}
	t.Run("unsupported_refuses_vendor_fallback", func(t *testing.T) {
		q := &parallelRetainedRead{dialect: transaction.Dialect("unknown"), row: parallelRetainedRow{err: sql.ErrNoRows}}
		got, e := retainedPreparationTask(context.Background(), q, n, task)
		if !errors.Is(e, sql.ErrNoRows) || !reflect.DeepEqual(got, preparationReconciliationTask{}) || !reflect.DeepEqual(q.statement, transaction.SQL(q.dialect, "")) {
			t.Fatal("unsupported dialect selected a foreign statement")
		}
	})
	for name, row := range map[string]parallelRetainedRow{
		"native_error": {err: errors.New("native metadata retained")}, "cancelled": {err: context.Canceled}, "invalid_clob": {raw: []byte(`{"scope":{}}`)}, "foreign_tenant": {raw: raw}, "negative_attempt": {raw: raw, attempt: -1}, "extra_attempt": {raw: raw, attempt: 2}, "invalid_expiry_integer": {raw: raw, expiry: 2},
	} {
		t.Run(name, func(t *testing.T) {
			current := n
			if name == "foreign_tenant" {
				current.TenantID = "55555555-5555-4555-8555-555555555555"
			}
			q := &parallelRetainedRead{dialect: transaction.Dameng, row: row}
			got, e := retainedPreparationTask(context.Background(), q, current, task)
			if e == nil || !reflect.DeepEqual(got, preparationReconciliationTask{}) || q.reads != 1 {
				t.Fatal("failed retained read leaked command or retried", e)
			}
			if row.err != nil && !errors.Is(e, row.err) {
				t.Fatal("query error metadata replaced")
			}
		})
	}
}
