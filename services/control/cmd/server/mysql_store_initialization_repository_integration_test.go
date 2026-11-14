//go:build repository_integration

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/storeinitialization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Each leaf requires a fresh actual isolated MySQL scope. Identity and tiny
// metadata are synthetic; every table, owner, lock, namespace and audit is real.
func TestActualMySQLStoreInitializationOperator(t *testing.T) {
	for _, leaf := range []string{"dataset_initialized_once", "training_initialized_once", "preparation_initialized_once", "model_directory_identity_audit", "unbootstrapped_zero_mutation", "inactive_tenant_zero_mutation", "foreign_configuration_zero_mutation", "explicit_namespace_audit_recovery", "retained_dataset_history_refuses"} {
		t.Run(leaf, func(t *testing.T) {
			x := serverCompositionSetup(t, leaf == "unbootstrapped_zero_mutation")
			if x.d.mysql == nil || x.f.Store.Dialect() != transaction.MySQL {
				t.Fatal("MYSQL_STORE_OPERATOR_ACTUAL_MYSQL_REQUIRED")
			}
			root := filepath.Join(x.root, "fresh-configured-store")
			if e := os.Mkdir(root, 0755); e != nil {
				t.Fatal(e)
			}
			uid, gid := os.Geteuid(), os.Getegid()
			if uid == 0 {
				uid = 65534
			}
			if gid == 0 {
				gid = 65534
			}
			kind, action := "dataset", "dataset.store_initialize_verified"
			command := "initialize-dataset-store"
			switch leaf {
			case "training_initialized_once":
				kind, action, command = "training", "training.store_initialize_verified", "initialize-training-store"
			case "preparation_initialized_once":
				kind, action, command = "preparation", "model.preparation_store_initialize_verified", "initialize-preparation-store"
			case "model_directory_identity_audit":
				kind, action, command = "model", "model.store_initialize_verified", storeinitialization.ConfiguredModelCommand
			}
			switch kind {
			case "model":
				x.c.Models.ArtifactRoot = root
			case "dataset":
				x.c.Datasets.ArtifactRoot = root
			case "training":
				x.c.Training.ArtifactRoot = root
			case "preparation":
				x.c.ModelPreparation.ArtifactRoot = root
			}
			op, e := x.d.mysqlOperator(x.ctx, x.c)
			if e != nil {
				t.Fatal("actual installation owner", e)
			}
			opts := storeinitialization.Options{Command: command, Root: root, UID: uid, GID: gid, ConfirmEmpty: true, Reason: "Synthetic explicit initialization operator"}
			run := func(o storeinitialization.Options) (string, error) {
				return storeinitialization.RunMySQL(x.ctx, op, x.c, o)
			}
			count := func() int {
				t.Helper()
				var n int
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("", "SELECT COUNT(*) FROM audit_events WHERE action=?", action)).Scan(&n); e != nil {
					t.Fatal(e)
				}
				return n
			}
			unchanged := func() {
				t.Helper()
				entries, e := os.ReadDir(root)
				info, se := os.Stat(root)
				if e != nil || se != nil || len(entries) != 0 || info.Mode().Perm() != 0755 || count() != 0 {
					t.Fatal("refusal mutated fresh root or audit")
				}
			}
			if leaf == "unbootstrapped_zero_mutation" || leaf == "inactive_tenant_zero_mutation" || leaf == "foreign_configuration_zero_mutation" || leaf == "retained_dataset_history_refuses" {
				switch leaf {
				case "inactive_tenant_zero_mutation":
					x.exec(t, "", "UPDATE tenants SET status='disabled' WHERE id=?", x.f.Tenant)
				case "foreign_configuration_zero_mutation":
					x.c.Server.AdminOrigin = "https://foreign.example.invalid"
				case "retained_dataset_history_refuses":
					x.exec(t, "", `INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,creation_key,creation_fingerprint) VALUES(?,?,?,'Synthetic retained dataset','synthetic retained dataset','Synthetic test metadata','Synthetic test license',true,?,?,?)`, uuid.NewString(), x.f.Tenant, x.f.Workspace, x.f.User, uuid.NewString(), bytes.Repeat([]byte{17}, 32))
				}
				if status, e := run(opts); e == nil || status != "" {
					t.Fatal("unsafe selected initialization projected success", status, e)
				}
				unchanged()
				return
			}
			check := opts
			check.Command, check.Reason, check.ConfirmEmpty = "check-"+kind+"-store-initialization", "", false
			if kind == "model" {
				check.Command = storeinitialization.CheckModelCommand
			}
			if leaf == "explicit_namespace_audit_recovery" {
				if e = datasets.InitializeStore(root, x.f.Tenant, uid, gid); e != nil {
					t.Fatal(e)
				}
				if status, e := run(check); e != nil || status != storeinitialization.NotRecorded || count() != 0 {
					t.Fatal("read-only initialization observation", status, e)
				}
				record := check
				record.Command, record.Reason = "record-dataset-store-initialization", "Synthetic explicit recovery after audit uncertainty"
				if status, e := run(record); e != nil || status != storeinitialization.Verified {
					t.Fatal("actual explicit audit record", status, e)
				}
				if status, e := run(record); e != nil || status != storeinitialization.Verified {
					t.Fatal("explicit record replay", status, e)
				}
			} else if status, e := run(opts); e != nil || status != storeinitialization.Verified {
				t.Fatal("actual selected initialization", status, e)
			}
			for i := 0; i < 2; i++ {
				if status, e := run(check); e != nil || status != storeinitialization.Verified {
					t.Fatal("actual readonly proof", status, e)
				}
			}
			if count() != 1 {
				t.Fatal("missing/duplicate owning initialization audit")
			}
			if kind == "model" {
				entries, e := os.ReadDir(root)
				if e != nil || len(entries) != 0 {
					t.Fatal("fake model namespace created")
				}
			}
			if e = os.Chmod(root, 0750); e != nil {
				t.Fatal(e)
			}
			if status, e := run(check); e == nil || status != "" || count() != 1 {
				t.Fatal("changed private root accepted/repaired", status, e)
			}
			info, e := os.Stat(root)
			if e != nil || info.Mode().Perm() != 0750 {
				t.Fatal("operator repaired changed root")
			}
		})
	}
}
