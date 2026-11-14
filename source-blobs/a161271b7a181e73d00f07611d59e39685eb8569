//go:build repository_integration && mysql_native_integration

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqlbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const nativeRuntimeScopeName = "runtime-account-scope.json"
const nativeRefusalBeforeName = "refusal-before.json"
const nativeRefusalAfterName = "refusal-after.json"
const nativeRefusalStateManifestUnits = 4

type nativeRuntimeScope struct {
	Format         string `json:"format"`
	SourceDatabase string `json:"source_database"`
	TargetDatabase string `json:"target_database"`
	RuntimeUser    string `json:"runtime_user"`
	AccountHost    string `json:"account_host"`
}

func nativeRuntimePolicy(t *testing.T, ctx context.Context, pool *sql.DB, root, source, target, user string) (config.MySQLRuntimeAccessConfiguration, nativeRuntimeScope) {
	t.Helper()
	var host string
	if pool.QueryRowContext(ctx, "SELECT SUBSTRING_INDEX(USER(),'@',-1)").Scan(&host) != nil {
		t.Fatal("native runtime connector peer unavailable")
	}
	policy := config.MySQLRuntimeAccessConfiguration{AccountHost: host, IntentFile: filepath.Join(root, "runtime-intent.json")}
	match := regexp.MustCompile(`^m20_repository_([a-f0-9]{18})_test$`).FindStringSubmatch(source)
	if policy.Validate() != nil || len(match) != 2 || target != "m20_repository_"+match[1]+"_restore_test" || user != "m20n_"+match[1]+"r" {
		t.Fatal("native runtime exact owned peer scope required")
	}
	value := nativeRuntimeScope{"projectone.mysql.native-runtime-scope.v1", source, target, user, host}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var actual nativeRuntimeScope
	if mysqloperator.WriteDocument(filepath.Join(root, nativeRuntimeScopeName), append(raw, '\n'), &actual) != nil || actual != value {
		t.Fatal("native runtime scope must persist before account effects")
	}
	return policy, value
}

type nativeSchemaQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type nativeRefusalObject struct {
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	DefinitionSHA256 string `json:"definition_sha256"`
}
type nativeRefusalRow struct {
	ID      int    `json:"id"`
	Payload string `json:"payload"`
}
type nativeRefusalStore struct {
	Kind       string                   `json:"kind"`
	Tree       mysqlbackup.TreeReadback `json:"tree"`
	Device     uint64                   `json:"device"`
	Inode      uint64                   `json:"inode"`
	UID        uint32                   `json:"uid"`
	GID        uint32                   `json:"gid"`
	Mode       uint32                   `json:"mode"`
	ModifiedNS int64                    `json:"modified_ns"`
}
type nativeRefusalRecord struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}
type nativeRefusalState struct {
	Format           string                        `json:"format"`
	Leaf             string                        `json:"leaf"`
	Phase            string                        `json:"phase"`
	SourceDatabase   string                        `json:"source_database"`
	TargetDatabase   string                        `json:"target_database"`
	SourceData       mysqlbackup.DatabaseReadback  `json:"source_data"`
	TargetData       *mysqlbackup.DatabaseReadback `json:"target_data,omitempty"`
	SourceObjects    []nativeRefusalObject         `json:"source_objects"`
	TargetObjects    []nativeRefusalObject         `json:"target_objects"`
	SentinelCreate   string                        `json:"sentinel_create"`
	SentinelRows     []nativeRefusalRow            `json:"sentinel_rows"`
	SourceStores     []nativeRefusalStore          `json:"source_stores"`
	TargetStores     []nativeRefusalStore          `json:"target_stores"`
	BackupTree       mysqlbackup.TreeReadback      `json:"backup_tree"`
	ScratchDirectory string                        `json:"scratch_directory"`
	ScratchTree      mysqlbackup.TreeReadback      `json:"scratch_tree"`
	Records          []nativeRefusalRecord         `json:"records"`
	RuntimeScope     *nativeRuntimeScope           `json:"runtime_scope,omitempty"`
	RuntimeGrants    []string                      `json:"runtime_grants"`
}
type nativeRefusalProof struct {
	Format       string `json:"format"`
	Phase        string `json:"phase"`
	ErrorCode    string `json:"error_code"`
	BeforeFile   string `json:"before_file"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterFile    string `json:"after_file"`
	AfterSHA256  string `json:"after_sha256"`
}
type nativeRefusalProbe struct {
	Context                    context.Context
	Source                     *mysqloperator.Context
	Target                     *sql.DB
	TargetOwner                *mysqloperator.Context
	SourceConfig, TargetConfig config.Config
	Root                       string
	Result                     *nativeCaseProof
	Runtime                    *nativeRuntimeScope
}
type nativeRefusalRun struct {
	Probe                         nativeRefusalProbe
	Before                        nativeRefusalState
	BeforeSHA256, Phase, Expected string
}

func nativeRefusalContract(leaf string) (string, string) {
	switch leaf {
	case "false_isolated_zero_mutation":
		return "admission", "MYSQL_RESTORE_EXPLICIT_ISOLATED_TARGET_REQUIRED"
	case "populated_target_sql_refused", "source_scope_zero_mutation":
		return "admission", "MYSQL_RESTORE_FRESH_DATABASE_REQUIRED"
	case "populated_target_store_refused":
		return "admission", "MYSQL_RESTORE_FRESH_STORES_REQUIRED"
	case "changed_sql_archive_refused":
		return "admission", "MYSQL_BACKUP_ARCHIVE_CHANGED"
	case "changed_store_archive_refused":
		return "admission", "MYSQL_BACKUP_STORE_READBACK_CHANGED"
	case "missing_backup_completion_refused":
		return "admission", "MYSQL_BACKUP_COMPLETION_REQUIRED"
	case "later_data_cannot_replace_postimage":
		return "postimage", "MYSQL_RESTORE_POST_IMAGE_CHANGED"
	case "extra_runtime_ddl_grant_refused":
		return "privileges", "MYSQL_RUNTIME_ACCOUNT_OR_PRIVILEGE_CONFLICT"
	default:
		return "", ""
	}
}

func nativeSchemaObjects(t *testing.T, ctx context.Context, q nativeSchemaQuery) []nativeRefusalObject {
	t.Helper()
	rows, err := q.QueryContext(ctx, `SELECT object_kind,object_name FROM (SELECT CASE TABLE_TYPE WHEN 'BASE TABLE' THEN 'table' WHEN 'VIEW' THEN 'view' ELSE 'unsupported' END object_kind,TABLE_NAME object_name FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() UNION ALL SELECT 'trigger',TRIGGER_NAME FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE() UNION ALL SELECT LOWER(ROUTINE_TYPE),ROUTINE_NAME FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA=DATABASE() UNION ALL SELECT 'event',EVENT_NAME FROM information_schema.EVENTS WHERE EVENT_SCHEMA=DATABASE()) owned_objects ORDER BY BINARY object_kind,BINARY object_name`)
	if err != nil {
		t.Fatal("native refusal schema inventory unavailable")
	}
	result := []nativeRefusalObject{}
	for rows.Next() {
		var item nativeRefusalObject
		if rows.Scan(&item.Kind, &item.Name) != nil || !regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(item.Name) {
			rows.Close()
			t.Fatal("native refusal schema object invalid")
		}
		result = append(result, item)
	}
	if rows.Err() != nil || rows.Close() != nil {
		t.Fatal("native refusal schema inventory incomplete")
	}
	commands := map[string]string{"table": "TABLE", "view": "VIEW", "trigger": "TRIGGER", "procedure": "PROCEDURE", "function": "FUNCTION", "event": "EVENT"}
	for i := range result {
		kind, ok := commands[result[i].Kind]
		if !ok {
			t.Fatal("native refusal unsupported schema object")
		}
		row, err := q.QueryContext(ctx, "SHOW CREATE "+kind+" `"+result[i].Name+"`")
		if err != nil {
			t.Fatal("native refusal actual schema definition unavailable")
		}
		columns, err := row.Columns()
		if err != nil || len(columns) == 0 || len(columns) > 32 || !row.Next() {
			row.Close()
			t.Fatal("native refusal schema definition shape invalid")
		}
		values := make([]sql.RawBytes, len(columns))
		destinations := make([]any, len(columns))
		for j := range values {
			destinations[j] = &values[j]
		}
		if row.Scan(destinations...) != nil {
			row.Close()
			t.Fatal("native refusal schema definition scan failed")
		}
		cells := make([]any, len(values))
		for j, value := range values {
			if value != nil {
				cells[j] = string(value)
			}
		}
		raw, err := json.Marshal(struct {
			Columns []string
			Values  []any
		}{columns, cells})
		if err != nil || row.Next() || row.Err() != nil || row.Close() != nil {
			t.Fatal("native refusal schema definition incomplete")
		}
		result[i].DefinitionSHA256 = mysqloperator.SHA256(raw)
	}
	return result
}

func nativeRefusalStores(t *testing.T, ctx context.Context, c config.Config, p config.MySQLBackupConfiguration) []nativeRefusalStore {
	t.Helper()
	result := []nativeRefusalStore{}
	paths := []string{c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot}
	for i, kind := range []string{"models", "datasets", "training", "preparations"} {
		info, err := os.Lstat(paths[i])
		if err != nil {
			t.Fatal("native refusal root unavailable")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || owner.Uid != uint32(os.Geteuid()) {
			t.Fatal("native refusal private root invalid")
		}
		tree, err := mysqlbackup.TreeDigest(ctx, paths[i], p.MaximumStoreBytes, p.MaximumStoreFiles, p.MaximumManifestBytes)
		if err != nil {
			t.Fatal("native refusal actual store readback failed")
		}
		result = append(result, nativeRefusalStore{kind, tree, uint64(owner.Dev), uint64(owner.Ino), owner.Uid, owner.Gid, uint32(info.Mode().Perm()), info.ModTime().UnixNano()})
	}
	return result
}

func (p nativeRefusalProbe) capture(t *testing.T, phase string) nativeRefusalState {
	t.Helper()
	policy := *p.SourceConfig.Database.MySQLBackup
	state := nativeRefusalState{Format: "projectone.mysql.native-refusal-state.v1", Leaf: p.Result.Leaf, Phase: phase, SourceDatabase: p.Result.SourceDatabase, TargetDatabase: p.Result.TargetDatabase, SentinelRows: []nativeRefusalRow{}, RuntimeGrants: []string{}, Records: []nativeRefusalRecord{}, RuntimeScope: p.Runtime}
	var err error
	state.SourceData, err = mysqlbackup.ReadDatabase(p.Context, p.Source, policy, false)
	if err != nil {
		t.Fatal("native refusal full source data unavailable")
	}
	lease, err := p.Source.Lease(p.Context)
	if err != nil {
		t.Fatal("native refusal source connection unavailable")
	}
	state.SourceObjects = nativeSchemaObjects(t, p.Context, lease)
	state.TargetObjects = nativeSchemaObjects(t, p.Context, p.Target)
	if p.TargetOwner != nil {
		value, err := mysqlbackup.ReadDatabase(p.Context, p.TargetOwner, policy, false)
		if err != nil {
			t.Fatal("native refusal full target data unavailable")
		}
		state.TargetData = &value
	}
	for _, item := range state.TargetObjects {
		if item.Kind == "table" && item.Name == "retained_native_probe" {
			var name string
			if p.Target.QueryRowContext(p.Context, "SHOW CREATE TABLE retained_native_probe").Scan(&name, &state.SentinelCreate) != nil {
				t.Fatal("native sentinel schema unavailable")
			}
			rows, err := p.Target.QueryContext(p.Context, "SELECT id,payload FROM retained_native_probe ORDER BY id")
			if err != nil {
				t.Fatal("native sentinel rows unavailable")
			}
			for rows.Next() {
				var row nativeRefusalRow
				if rows.Scan(&row.ID, &row.Payload) != nil {
					rows.Close()
					t.Fatal("native sentinel row invalid")
				}
				state.SentinelRows = append(state.SentinelRows, row)
			}
			if rows.Err() != nil || rows.Close() != nil {
				t.Fatal("native sentinel rows incomplete")
			}
		}
	}
	state.SourceStores = nativeRefusalStores(t, p.Context, p.SourceConfig, policy)
	state.TargetStores = nativeRefusalStores(t, p.Context, p.TargetConfig, policy)
	state.BackupTree, err = mysqlbackup.TreeDigest(p.Context, filepath.Join(p.Root, "backup"), policy.MaximumStoreBytes+policy.MaximumSQLBytes, policy.MaximumStoreFiles, policy.MaximumManifestBytes)
	if err != nil {
		t.Fatal("native refusal backup readback unavailable")
	}
	state.ScratchDirectory, err = filepath.Rel(p.Root, policy.ScratchRoot)
	if err != nil || state.ScratchDirectory == "." || filepath.IsAbs(state.ScratchDirectory) || strings.HasPrefix(state.ScratchDirectory, "..") {
		t.Fatal("native refusal exact scratch scope required")
	}
	state.ScratchTree, err = mysqlbackup.TreeDigest(p.Context, policy.ScratchRoot, policy.MaximumStoreBytes, policy.MaximumStoreFiles, policy.MaximumManifestBytes)
	if err != nil {
		t.Fatal("native refusal scratch readback unavailable")
	}
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		t.Fatal("native refusal records unavailable")
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == nativeRefusalBeforeName || entry.Name() == nativeRefusalAfterName || entry.Name() == "proof.json" {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > policy.MaximumManifestBytes*nativeRefusalStateManifestUnits {
			t.Fatal("native refusal record shape invalid")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
			t.Fatal("native refusal private record owner invalid")
		}
		raw, err := os.ReadFile(filepath.Join(p.Root, entry.Name()))
		if err != nil || int64(len(raw)) != info.Size() {
			t.Fatal("native refusal record read failed")
		}
		state.Records = append(state.Records, nativeRefusalRecord{entry.Name(), info.Size(), mysqloperator.SHA256(raw), uint32(info.Mode().Perm())})
	}
	if p.Runtime != nil {
		check := config.MySQLRuntimeAccessConfiguration{AccountHost: p.Runtime.AccountHost, IntentFile: filepath.Join(p.Root, "runtime-intent.json")}
		if check.Validate() != nil || !regexp.MustCompile(`^m20n_[a-f0-9]{18}r$`).MatchString(p.Runtime.RuntimeUser) {
			t.Fatal("native refusal runtime scope invalid")
		}
		rows, err := p.Target.QueryContext(p.Context, "SHOW GRANTS FOR '"+p.Runtime.RuntimeUser+"'@'"+p.Runtime.AccountHost+"'")
		if err != nil {
			t.Fatal("native refusal grant inventory unavailable")
		}
		for rows.Next() {
			var grant string
			if rows.Scan(&grant) != nil {
				rows.Close()
				t.Fatal("native refusal grant scan failed")
			}
			state.RuntimeGrants = append(state.RuntimeGrants, grant)
		}
		if rows.Err() != nil || rows.Close() != nil {
			t.Fatal("native refusal grant inventory incomplete")
		}
		sort.Strings(state.RuntimeGrants)
	}
	return state
}

func nativeWriteRefusalState(t *testing.T, root, name string, value nativeRefusalState, maximum int64) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil || int64(len(raw))+1 > maximum {
		t.Fatal("native refusal state metadata limit reached")
	}
	raw = append(raw, '\n')
	directory, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal("native refusal private output unavailable")
	}
	defer directory.Close()
	file, err := directory.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		t.Fatal("native refusal exclusive state creation failed")
	}
	n, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || n != len(raw) {
		t.Fatal("native refusal state durability failed")
	}
	parent, err := directory.Open(".")
	if err != nil {
		t.Fatal("native refusal state parent unavailable")
	}
	syncErr = parent.Sync()
	closeErr = parent.Close()
	if syncErr != nil || closeErr != nil {
		t.Fatal("native refusal state directory sync failed")
	}
	read, err := directory.Open(name)
	if err != nil {
		t.Fatal("native refusal state readback unavailable")
	}
	actual, readErr := io.ReadAll(io.LimitReader(read, maximum+1))
	closeErr = read.Close()
	if readErr != nil || closeErr != nil || !reflect.DeepEqual(actual, raw) {
		t.Fatal("native refusal durable bytes changed")
	}
	return mysqloperator.SHA256(raw)
}

func nativeBeginRefusal(t *testing.T, probe nativeRefusalProbe) *nativeRefusalRun {
	t.Helper()
	phase, expected := nativeRefusalContract(probe.Result.Leaf)
	if phase == "" {
		t.Fatal("native exact refusal contract required")
	}
	before := probe.capture(t, phase)
	hash := nativeWriteRefusalState(t, probe.Root, nativeRefusalBeforeName, before, probe.SourceConfig.Database.MySQLBackup.MaximumManifestBytes*nativeRefusalStateManifestUnits)
	return &nativeRefusalRun{probe, before, hash, phase, expected}
}

func (r *nativeRefusalRun) finish(t *testing.T, cause error) *nativeRefusalProof {
	t.Helper()
	after := r.Probe.capture(t, r.Phase)
	hash := nativeWriteRefusalState(t, r.Probe.Root, nativeRefusalAfterName, after, r.Probe.SourceConfig.Database.MySQLBackup.MaximumManifestBytes*nativeRefusalStateManifestUnits)
	code := "UNEXPECTED_ERROR"
	if cause != nil && regexp.MustCompile(`^[A-Z0-9_]+$`).MatchString(cause.Error()) {
		code = cause.Error()
	}
	if code != r.Expected || !reflect.DeepEqual(after, r.Before) || hash != r.BeforeSHA256 {
		t.Fatal("native refusal exact cause or preserved state failed", r.Phase, code)
	}
	return &nativeRefusalProof{"projectone.mysql.native-refusal-integrity.v1", r.Phase, code, nativeRefusalBeforeName, r.BeforeSHA256, nativeRefusalAfterName, hash}
}
