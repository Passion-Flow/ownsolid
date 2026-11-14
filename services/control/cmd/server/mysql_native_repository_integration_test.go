//go:build repository_integration && mysql_native_integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqlbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type nativeCaseInput struct {
	Format                string                          `json:"format"`
	Leaf                  string                          `json:"leaf"`
	Root                  string                          `json:"root"`
	SourceConnectionFile  string                          `json:"source_connection_file"`
	TargetConnectionFile  string                          `json:"target_connection_file"`
	RuntimeConnectionFile string                          `json:"runtime_connection_file"`
	Policy                config.MySQLBackupConfiguration `json:"policy"`
}
type nativeCaseProof struct {
	Format            string              `json:"format"`
	Leaf              string              `json:"leaf"`
	SourceDatabase    string              `json:"source_database"`
	TargetDatabase    string              `json:"target_database"`
	BackupID          string              `json:"backup_id"`
	RestoreID         string              `json:"restore_id"`
	BackupAuditCount  int                 `json:"backup_audit_count"`
	RestoreAuditCount int                 `json:"restore_audit_count"`
	RoleStatus        string              `json:"role_status,omitempty"`
	Qualification     string              `json:"qualification"`
	Refusal           *nativeRefusalProof `json:"refusal,omitempty"`
}

func nativeOwnedWrite(t *testing.T, path string, value any) {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestActualMySQLNativeOperator(t *testing.T) {
	leaves := []string{"actual_empty_four_store_roundtrip", "false_isolated_zero_mutation", "populated_target_sql_refused", "populated_target_store_refused", "changed_sql_archive_refused", "changed_store_archive_refused", "missing_backup_completion_refused", "actual_committed_context_reconcile", "later_data_cannot_replace_postimage", "source_scope_zero_mutation", "actual_restored_runtime_role", "extra_runtime_ddl_grant_refused"}
	for _, leaf := range leaves {
		t.Run(leaf, func(t *testing.T) {
			// Test identities/metadata and empty real namespaces are synthetic. Every SQL
			// table, TLS connector, native process, lock, row digest, store and audit is real.
			var input nativeCaseInput
			if _, e := mysqloperator.ReadDocument(os.Getenv("PROJECT_ONE_MYSQL_NATIVE_CASE_INPUT"), config.MaximumProtectedConfigurationBytes, &input); e != nil {
				t.Fatal("explicit protected native input", e)
			}
			if input.Format != "projectone.mysql.native-case-input.v1" || input.Leaf != leaf || input.Policy.Validate() != nil || os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "mysql" || input.SourceConnectionFile != os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION") {
				t.Fatal("NATIVE_ACTUAL_EXACT_INPUT_REQUIRED")
			}
			root, e := filepath.EvalSymlinks(input.Root)
			if e != nil || root != input.Root || !filepath.IsAbs(root) {
				t.Fatal("NATIVE_PRIVATE_OUTPUT_REQUIRED")
			}
			info, e := os.Stat(root)
			if e != nil || info.Mode() != os.ModeDir|0700 {
				t.Fatal("NATIVE_PRIVATE_OUTPUT_REQUIRED")
			}
			if os.Geteuid() == 0 || os.Getegid() == 0 {
				t.Fatal("NATIVE_EXPLICIT_NONROOT_OWNER_REQUIRED")
			}
			x := serverCompositionSetup(t, false)
			bounded, cancel := context.WithTimeout(t.Context(), time.Duration(input.Policy.OperationSeconds)*time.Second)
			defer cancel()
			x.ctx = bounded
			if x.d.mysql == nil {
				t.Fatal("NATIVE_ACTUAL_MYSQL_REQUIRED")
			}
			for _, item := range []struct {
				kind string
				dest *string
			}{{"models", &x.c.Models.ArtifactRoot}, {"datasets", &x.c.Datasets.ArtifactRoot}, {"training", &x.c.Training.ArtifactRoot}, {"preparations", &x.c.ModelPreparation.ArtifactRoot}} {
				*item.dest = filepath.Join(root, "source-"+item.kind)
				if e = os.Mkdir(*item.dest, 0700); e != nil {
					t.Fatal(e)
				}
			}
			if e = os.Mkdir(input.Policy.ScratchRoot, 0700); e != nil {
				t.Fatal(e)
			}
			x.c.Database.MySQLBackup = &input.Policy
			// The original SQL fixture does not create workspace membership. This actual
			// current owner row is explicit synthetic identity setup, not auth acceptance.
			x.exec(t, "", `INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) SELECT ?,?,?,'owner' WHERE NOT EXISTS(SELECT 1 FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?)`, x.f.Tenant, x.f.Workspace, x.f.User, x.f.Tenant, x.f.Workspace, x.f.User)
			if e = models.InitializeStore(x.c.Models.ArtifactRoot, os.Geteuid(), os.Getegid()); e != nil {
				t.Fatal(e)
			}
			for _, item := range []struct {
				path       string
				initialize func(string, string, int, int) error
			}{
				{x.c.Datasets.ArtifactRoot, datasets.InitializeStore}, {x.c.Training.ArtifactRoot, training.InitializeStore}, {x.c.ModelPreparation.ArtifactRoot, modelpreparations.InitializeStore},
			} {
				if e = item.initialize(item.path, x.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
					t.Fatal(e)
				}
			}
			// Observe the actual Go process and initialized root before native
			// backup. These public predicates never include credential bytes.
			resolved, resolveError := filepath.EvalSymlinks(x.c.Datasets.ArtifactRoot)
			info, statError := os.Lstat(x.c.Datasets.ArtifactRoot)
			facts := map[string]any{"path_absolute": filepath.IsAbs(x.c.Datasets.ArtifactRoot), "path_clean": filepath.Clean(x.c.Datasets.ArtifactRoot) == x.c.Datasets.ArtifactRoot, "resolution_ok": resolveError == nil, "resolution_equal": resolved == x.c.Datasets.ArtifactRoot, "stat_ok": statError == nil, "tenant_valid": datawire.ValidID(x.f.Tenant), "minimum_free_bytes": x.c.Policy().Datasets.MinimumFreeBytes, "euid": os.Geteuid()}
			if statError == nil {
				facts["directory"] = info.IsDir()
				facts["permission_bits"] = uint32(info.Mode().Perm())
				owner, ok := info.Sys().(*syscall.Stat_t)
				facts["unix_stat"] = ok
				if ok {
					facts["uid"] = owner.Uid
					facts["uid_matches"] = owner.Uid == uint32(os.Geteuid())
				}
			}
			diagnostic, e := json.Marshal(facts)
			if e != nil {
				t.Fatal(e)
			}
			t.Log("NATIVE_DATASET_PREFLIGHT", string(diagnostic))
			if _, e = datasets.VerifyInitializedStore(x.c.Datasets.ArtifactRoot, x.f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
				t.Fatal("actual initialized dataset readback", e)
			}
			preflightStore, e := datasets.OpenStore(x.c.Datasets.ArtifactRoot, x.f.Tenant, x.c.Policy().Datasets.MinimumFreeBytes)
			if e != nil {
				t.Fatal("actual dataset store preflight", e)
			}
			if e = preflightStore.Close(); e != nil {
				t.Fatal("actual dataset store preflight close", e)
			}
			source := x.d.identity(x.c)
			var sourceAuthEpoch int64
			if e = x.f.Store.QueryRow(bounded, transaction.SQL(transaction.MySQL, "SELECT auth_epoch FROM users WHERE id=?", x.f.User)).Scan(&sourceAuthEpoch); e != nil || sourceAuthEpoch < 1 {
				t.Fatal("actual source account epoch", sourceAuthEpoch, e)
			}
			sourceOperator, e := x.d.mysqlOperator(bounded, x.c)
			if e != nil {
				t.Fatal("actual source operator", e)
			}
			archive := filepath.Join(root, "backup")
			manifest, e := mysqlbackup.Backup(bounded, sourceOperator, x.c, source, archive, "Synthetic explicit native backup", x.d.catalog.Steps())
			if e != nil {
				t.Fatal("actual native backup", e)
			}
			targetConfig := x.c
			targetConfig.Database.MySQLConfigurationFile = input.TargetConnectionFile
			targetConfig.Models.ArtifactRoot = filepath.Join(root, "target-models")
			targetConfig.Datasets.ArtifactRoot = filepath.Join(root, "target-datasets")
			targetConfig.Training.ArtifactRoot = filepath.Join(root, "target-training")
			targetConfig.ModelPreparation.ArtifactRoot = filepath.Join(root, "target-preparations")
			for _, path := range []string{targetConfig.Models.ArtifactRoot, targetConfig.Datasets.ArtifactRoot, targetConfig.Training.ArtifactRoot, targetConfig.ModelPreparation.ArtifactRoot} {
				if e = os.Mkdir(path, 0700); e != nil {
					t.Fatal(e)
				}
			}
			target, e := openSelectedDatabase(bounded, targetConfig, mysqlbackup.RestoreCommand)
			if e != nil {
				t.Fatal("actual fresh target", e)
			}
			t.Cleanup(func() {
				if e := target.Close(); e != nil {
					t.Error("actual target close", e)
				}
			})
			fresh, e := target.mysqlFresh(bounded, targetConfig)
			if e != nil {
				t.Fatal(e)
			}
			targetIdentity := target.identity(targetConfig)
			contextPath := filepath.Join(root, "restore.json")
			sourceSettings, e := mysqlstore.Load(input.SourceConnectionFile, "")
			if e != nil {
				t.Fatal(e)
			}
			targetSettings, e := mysqlstore.Load(input.TargetConnectionFile, "")
			if e != nil || targetSettings.Database == sourceSettings.Database || targetSettings.User != sourceSettings.User {
				t.Fatal("actual distinct database/same principal required", e)
			}
			result := nativeCaseProof{Format: "projectone.mysql.native-case-proof.v1", Leaf: leaf, SourceDatabase: sourceSettings.Database, TargetDatabase: targetSettings.Database, BackupID: manifest.BackupID, Qualification: "Actual empty four-store native MySQL fixture only; synthetic account metadata. No nonempty trained/prepared artifact, wire-loss, physical removal, production or native hardware qualification."}
			sourceCount := func() int {
				t.Helper()
				var n int
				if e := x.f.Store.QueryRow(bounded, transaction.SQL(transaction.MySQL, "SELECT COUNT(*) FROM audit_events WHERE action='operator.mysql_backup_exported' AND request_id=?", manifest.BackupID)).Scan(&n); e != nil {
					t.Fatal(e)
				}
				return n
			}
			targetCount := func() int {
				t.Helper()
				var n int
				if e := target.store.QueryRow(bounded, transaction.SQL(transaction.MySQL, "SELECT COUNT(*) FROM audit_events WHERE action='operator.mysql_restore_verified'")).Scan(&n); e != nil {
					t.Fatal(e)
				}
				return n
			}
			save := func() {
				t.Helper()
				result.BackupAuditCount = sourceCount()
				if result.BackupAuditCount != 1 {
					t.Fatal("source audit missing/duplicated")
				}
				nativeOwnedWrite(t, filepath.Join(root, "proof.json"), result)
			}
			refuse := false
			isolated := true
			selectedFresh := fresh
			selectedConfig := targetConfig
			selectedIdentity := targetIdentity
			switch leaf {
			case "false_isolated_zero_mutation":
				isolated = false
				refuse = true
			case "populated_target_sql_refused":
				if _, e = target.store.Exec(bounded, transaction.SQL(transaction.MySQL, "CREATE TABLE retained_native_probe(id INT PRIMARY KEY,payload VARCHAR(100) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin")); e != nil {
					t.Fatal(e)
				}
				if _, e = target.store.Exec(bounded, transaction.SQL(transaction.MySQL, "INSERT INTO retained_native_probe(id,payload) VALUES(?,?),(?,?)", 17, "Synthetic retained alpha", 91, "Synthetic retained caf\u00e9 \u4e16\u754c")); e != nil {
					t.Fatal(e)
				}
				refuse = true
			case "populated_target_store_refused":
				if e = os.WriteFile(filepath.Join(targetConfig.Datasets.ArtifactRoot, "retained"), []byte("Synthetic retained bytes"), 0600); e != nil {
					t.Fatal(e)
				}
				if e = os.Mkdir(filepath.Join(targetConfig.Datasets.ArtifactRoot, "retained-directory"), 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(targetConfig.Datasets.ArtifactRoot, "retained-directory", "payload.jsonl"), []byte("Synthetic nested retained bytes caf\u00e9 \u4e16\u754c\n"), 0600); e != nil {
					t.Fatal(e)
				}
				refuse = true
			case "changed_sql_archive_refused":
				file, e := os.OpenFile(filepath.Join(archive, "database.sql"), os.O_APPEND|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				_, e = file.WriteString("\n-- synthetic changed original archive\n")
				closeError := file.Close()
				if e != nil || closeError != nil {
					t.Fatal(e, closeError)
				}
				refuse = true
			case "changed_store_archive_refused":
				if e = os.WriteFile(filepath.Join(archive, "files", "models", "retained-extra"), []byte("Synthetic changed archive"), 0600); e != nil {
					t.Fatal(e)
				}
				refuse = true
			case "missing_backup_completion_refused":
				if e = os.Rename(filepath.Join(archive, "complete.json"), filepath.Join(root, "original-backup-complete.json")); e != nil {
					t.Fatal(e)
				}
				refuse = true
			case "source_scope_zero_mutation":
				selectedFresh, e = x.d.mysqlFresh(bounded, x.c)
				if e != nil {
					t.Fatal(e)
				}
				selectedConfig = x.c
				selectedIdentity = source
				refuse = true
			}
			var refusalRun *nativeRefusalRun
			if refuse {
				refusalRun = nativeBeginRefusal(t, nativeRefusalProbe{Context: bounded, Source: sourceOperator, Target: target.mysql.Pool(), SourceConfig: x.c, TargetConfig: targetConfig, Root: root, Result: &result})
			}
			restored, e := mysqlbackup.Restore(bounded, selectedFresh, selectedConfig, selectedIdentity, archive, contextPath, "Synthetic explicit isolated native restore", isolated)
			if refuse {
				if e == nil || !reflect.DeepEqual(restored, mysqlbackup.RestoredContext{}) {
					t.Fatal("unsafe native restore projected context", e)
				}
				result.Refusal = refusalRun.finish(t, e)
				result.Format = "projectone.mysql.native-case-proof.v2"
				for _, suffix := range []string{"", ".intent", ".files", ".native.stderr"} {
					if _, e = os.Lstat(contextPath + suffix); !os.IsNotExist(e) {
						t.Fatal("refused restore created an effect record")
					}
				}
				if leaf != "populated_target_sql_refused" {
					if e = fresh.RequireEmpty(bounded); e != nil {
						t.Fatal("refused restore mutated target SQL", e)
					}
				}
				save()
				return
			}
			if e != nil {
				t.Fatal("actual native isolated restore", e)
			}
			result.RestoreID = restored.RestoreID
			result.RestoreAuditCount = targetCount()
			if result.RestoreAuditCount != 1 {
				t.Fatal("missing/duplicated restore audit")
			}
			if verified, e := mysqlbackup.VerifyRestoredContext(bounded, fresh, targetConfig, targetIdentity, contextPath, true); e != nil || verified != restored {
				t.Fatal("actual complete restored proof", e)
			}
			var authEpoch int64
			if e = target.store.QueryRow(bounded, transaction.SQL(transaction.MySQL, "SELECT auth_epoch FROM users WHERE id=?", x.f.User)).Scan(&authEpoch); e != nil || authEpoch != sourceAuthEpoch+1 {
				t.Fatal("actual original account invalidation", authEpoch, e)
			}
			t.Logf("NATIVE_ACCOUNT_EPOCH source=%d restored=%d", sourceAuthEpoch, authEpoch)
			if leaf == "actual_committed_context_reconcile" || leaf == "later_data_cannot_replace_postimage" {
				if e = os.Rename(contextPath, filepath.Join(root, "original-committed-restore.json")); e != nil {
					t.Fatal(e)
				}
				if leaf == "later_data_cannot_replace_postimage" {
					if _, e = target.store.Exec(bounded, transaction.SQL(transaction.MySQL, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", x.f.User)); e != nil {
						t.Fatal(e)
					}
					owner, e := target.mysqlOperator(bounded, targetConfig)
					if e != nil {
						t.Fatal("native postimage owner unavailable")
					}
					refusal := nativeBeginRefusal(t, nativeRefusalProbe{Context: bounded, Source: sourceOperator, Target: target.mysql.Pool(), TargetOwner: owner, SourceConfig: x.c, TargetConfig: targetConfig, Root: root, Result: &result})
					proof, refusalError := mysqlbackup.ReconcileRestore(bounded, fresh, targetConfig, targetIdentity, contextPath, true)
					if refusalError == nil || !reflect.DeepEqual(proof, mysqlbackup.RestoredContext{}) {
						t.Fatal("later data manufactured original post-image")
					}
					result.Refusal = refusal.finish(t, refusalError)
					result.Format = "projectone.mysql.native-case-proof.v2"
					if _, e = os.Lstat(contextPath); !os.IsNotExist(e) {
						t.Fatal("drift published context")
					}
				} else if proof, e := mysqlbackup.ReconcileRestore(bounded, fresh, targetConfig, targetIdentity, contextPath, true); e != nil || proof != restored {
					t.Fatal("actual committed audit publication recovery", e)
				}
				if targetCount() != 1 {
					t.Fatal("reconcile rewrote restore audit")
				}
			}
			if leaf == "actual_restored_runtime_role" || leaf == "extra_runtime_ddl_grant_refused" {
				runtimeConfig := targetConfig
				// This empty-store role fixture proves SQL DML authority, not AI worker
				// execution. Keep a genuinely disabled worker configuration instead of
				// inventing processor paths or training/preparation image identities.
				runtimeConfig.Models.ArtifactRoot = ""
				runtimeConfig.Datasets = config.DatasetConfiguration{}
				runtimeConfig.Training = config.TrainingConfiguration{}
				runtimeConfig.ModelPreparation = config.ModelPreparationConfiguration{}
				runtimeConfig.Database.MySQLConfigurationFile = input.RuntimeConnectionFile
				runtimeSettings, e := mysqlstore.Load(input.RuntimeConnectionFile, "")
				if e != nil {
					t.Fatal(e)
				}
				runtimePolicy, runtimeScope := nativeRuntimePolicy(t, bounded, target.mysql.Pool(), root, result.SourceDatabase, result.TargetDatabase, runtimeSettings.User)
				runtimeConfig.Database.MySQLRuntimeAccess = &runtimePolicy
				raw, e := yaml.Marshal(runtimeConfig)
				if e != nil {
					t.Fatal(e)
				}
				path := filepath.Join(root, "runtime.yaml")
				if e = os.WriteFile(path, raw, 0600); e != nil {
					t.Fatal(e)
				}
				runtime, e := databaseaccess.LoadMySQLRuntime(path)
				if e != nil {
					t.Fatal("actual protected runtime input", e)
				}
				operator, e := target.mysqlOperator(bounded, targetConfig)
				if e != nil {
					t.Fatal(e)
				}
				status, e := databaseaccess.RestoredMySQLAccess(bounded, operator, fresh, targetConfig, targetIdentity, runtime, contextPath, "prepare-restored-runtime-access")
				if e != nil || status != databaseaccess.Verified {
					t.Fatal("actual restored runtime account", status, e)
				}
				result.RoleStatus = status
				if leaf == "extra_runtime_ddl_grant_refused" {
					// Both identifiers came from the tool's strict isolated m20/username grammar.
					if !regexp.MustCompile(`^m20n_[a-f0-9]{18}r$`).MatchString(runtimeSettings.User) || !regexp.MustCompile(`^m20_repository_[a-f0-9]{18}_restore_test$`).MatchString(runtimeSettings.Database) {
						t.Fatal("owned runtime scope required")
					}
					if _, e = target.mysql.Pool().ExecContext(bounded, "GRANT CREATE ON `"+strings.ReplaceAll(runtimeSettings.Database, "_", `\_`)+"`.* TO '"+runtimeSettings.User+"'@'"+runtimePolicy.AccountHost+"'"); e != nil {
						t.Fatal(e)
					}
					refusal := nativeBeginRefusal(t, nativeRefusalProbe{Context: bounded, Source: sourceOperator, Target: target.mysql.Pool(), TargetOwner: operator, SourceConfig: x.c, TargetConfig: targetConfig, Root: root, Result: &result, Runtime: &runtimeScope})
					if status, e = databaseaccess.RestoredMySQLAccess(bounded, operator, fresh, targetConfig, targetIdentity, runtime, contextPath, databaseaccess.CheckRestoredMySQLCommand); e == nil || status != "" {
						t.Fatal("extra runtime DDL authority accepted", status, e)
					}
					result.Refusal = refusal.finish(t, e)
					result.Format = "projectone.mysql.native-case-proof.v2"
					result.RoleStatus = "refused_extra_ddl"
				} else {
					if _, e = target.store.Exec(bounded, transaction.SQL(transaction.MySQL, "UPDATE users SET display_name='Synthetic legitimate later use' WHERE id=?", x.f.User)); e != nil {
						t.Fatal(e)
					}
					if status, e = databaseaccess.RestoredMySQLAccess(bounded, operator, fresh, targetConfig, targetIdentity, runtime, contextPath, databaseaccess.CheckRestoredMySQLCommand); e != nil || status != databaseaccess.Verified {
						t.Fatal("actual current role after business use", status, e)
					}
				}
			}
			save()
		})
	}
}
