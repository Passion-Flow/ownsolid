package database

import (
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"strings"
	"testing"
)

func TestAccountMigrationCatalogIsIndependentAndComplete(t *testing.T) {
	account, e := orderedProfile("account")
	if e != nil || len(account) != 4 {
		t.Fatal(e)
	}
	platform, e := orderedProfile("platform")
	if e != nil || len(platform) < 4 {
		t.Fatal(e)
	}
	if account[0].Checksum != platform[0].Checksum || account[1].Checksum == platform[1].Checksum {
		t.Fatal("profile isolation lost")
	}
	var body strings.Builder
	for _, m := range account {
		body.Write(m.SQL)
	}
	for _, forbidden := range []string{"public.vector(", "CREATE TABLE sso_", "CREATE TABLE managed_nodes", "CREATE TABLE training_"} {
		if strings.Contains(body.String(), forbidden) {
			t.Fatal("future dependency", forbidden)
		}
	}
	if !strings.Contains(body.String(), "platform_license_artifacts") || !strings.Contains(body.String(), "account_local_session_only") {
		t.Fatal("security boundary omitted")
	}
	if _, e = orderedProfile("invalid"); e == nil {
		t.Fatal("unknown profile accepted")
	}
	if _, e = orderedProfile("account", "platform"); e == nil {
		t.Fatal("ambiguous profile accepted")
	}
}

func TestWorkspacesCatalogPreservesDeliveredAccountPrefix(t *testing.T) {
	a, e := orderedProfile("account")
	if e != nil {
		t.Fatal(e)
	}
	w, e := orderedProfile("workspaces")
	if e != nil || len(w) != 6 {
		t.Fatal(e)
	}
	for i, m := range a {
		if m.Checksum != w[i].Checksum || string(m.SQL) != string(w[i].SQL) {
			t.Fatal("account prefix changed")
		}
	}
	body := ""
	for _, m := range w {
		body += string(m.SQL)
	}
	if !strings.Contains(body, "CREATE TABLE workspace_notes") || !strings.Contains(body, "authority_id") {
		t.Fatal("M02 schema missing")
	}
	for _, v := range []string{"CREATE TABLE sso_", "CREATE TABLE managed_nodes", "public.vector(", "CREATE TABLE training_"} {
		if strings.Contains(body, v) {
			t.Fatal("future dependency", v)
		}
	}
}

func TestNodesCatalogPreservesDeliveredWorkspacePrefix(t *testing.T) {
	w, e := orderedProfile("workspaces")
	if e != nil {
		t.Fatal(e)
	}
	n, e := orderedProfile("nodes")
	if e != nil || len(n) != 8 {
		t.Fatal(e)
	}
	for i, m := range w {
		if m.Checksum != n[i].Checksum || string(m.SQL) != string(n[i].SQL) {
			t.Fatal("M02 immutable prefix changed")
		}
	}
	body := ""
	for _, m := range n {
		body += string(m.SQL)
	}
	for _, v := range []string{"public.vector(", "CREATE TABLE node_components", "CREATE TABLE sso_", "CREATE TABLE training_"} {
		if strings.Contains(body, v) {
			t.Fatal("future dependency", v)
		}
	}
}

func TestComponentsCatalogPreservesDeliveredNodesPrefix(t *testing.T) {
	n, e := orderedProfile("nodes")
	if e != nil {
		t.Fatal(e)
	}
	c, e := orderedProfile("components")
	if e != nil || len(c) != 9 {
		t.Fatal(e)
	}
	for i, m := range n {
		if m.Checksum != c[i].Checksum || string(m.SQL) != string(c[i].SQL) {
			t.Fatal("immutable M03 prefix changed")
		}
	}
	for _, s := range []string{"CREATE TABLE managed_components", "desired_version", "desired_config", "applied_config", "previous_version", "last_verified_at"} {
		if !strings.Contains(string(c[8].SQL), s) {
			t.Fatal("missing required repository field", s)
		}
	}
	for _, s := range []string{"CREATE TABLE component_log_snapshots", "systemd_maintenance", "node_exporter.configure", "CREATE TABLE training_", "public.vector("} {
		if strings.Contains(string(c[8].SQL), s) {
			t.Fatal("later authority", s)
		}
	}
}

func TestMaintenanceCatalogPreservesDeliveredComponentsPrefix(t *testing.T) {
	old, e := orderedProfile("components")
	if e != nil {
		t.Fatal(e)
	}
	next, e := orderedProfile("maintenance")
	if e != nil || len(next) != 10 {
		t.Fatal(e)
	}
	for i, m := range old {
		if m.Checksum != next[i].Checksum || string(m.SQL) != string(next[i].SQL) {
			t.Fatal("immutable M04 prefix changed")
		}
	}
	for _, v := range []string{"ADD COLUMN request_fingerprint", "CREATE TABLE managed_components", "CREATE TABLE training_", "docker_inference"} {
		if strings.Contains(string(next[9].SQL), v) {
			t.Fatal("unexpected replay/future owner", v)
		}
	}
}

func TestGatewayCatalogPreservesDeliveredModelsPrefix(t *testing.T) {
	old, e := orderedProfile("models")
	if e != nil {
		t.Fatal(e)
	}
	current, e := orderedProfile("gateway")
	if e != nil || len(current) != 12 {
		t.Fatal(e)
	}
	for i, m := range old {
		if m.Checksum != current[i].Checksum || string(m.SQL) != string(current[i].SQL) {
			t.Fatal("M06 prefix changed")
		}
	}
	body := string(current[11].SQL)
	if strings.Contains(body, "ADD COLUMN authority_id") || strings.Contains(body, "CREATE TABLE model_preparations") || strings.Contains(body, "cutover_intents") {
		t.Fatal("later authority or membership replacement")
	}
	for _, table := range []string{"gateway_instances", "model_aliases", "model_alias_history", "api_keys", "gateway_requests", "gateway_recoveries", "model_alias_target_versions"} {
		if !strings.Contains(body, "CREATE TABLE "+table+" (") {
			t.Fatal(table)
		}
	}
}

func TestDatasetsCatalogPreservesDeliveredGatewayPrefix(t *testing.T) {
	old, e := orderedProfile("gateway")
	if e != nil {
		t.Fatal(e)
	}
	current, e := orderedProfile("datasets")
	if e != nil || len(current) != 14 {
		t.Fatal(e)
	}
	for i, m := range old {
		if m.Checksum != current[i].Checksum || string(m.SQL) != string(current[i].SQL) {
			t.Fatal("M07 prefix changed")
		}
	}
	for i, path := range []string{"postgresql/000008_datasets.up.sql", "postgresql/000019_dataset_retirement_reason.up.sql"} {
		raw, e := migrations.Files.ReadFile(path)
		if e != nil || string(raw) != string(current[12+i].SQL) {
			t.Fatal("frozen migration bytes changed", e)
		}
	}
}
