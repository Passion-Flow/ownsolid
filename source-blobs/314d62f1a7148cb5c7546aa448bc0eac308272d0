package migrate

import "testing"

func TestRestrictedRuntimeGrantPolicy(t *testing.T) {
	c, e := accountCatalog()
	if e != nil {
		t.Fatal(e)
	}
	g := requiredGrants(c)
	legacy, le := foundationCatalog()
	if le != nil || len(requiredGrants(legacy)) != 15 {
		t.Fatal("legacy grant table boundary changed")
	}
	if len(g) != 22 || len(g[journalName]) != 1 || !g[journalName]["SELECT"] {
		t.Fatal("closed table policy")
	}
	refs := map[string]bool{"TENANTS": true, "WORKSPACES": true, "USERS": true, "PASSWORD_RESET_TOKENS": true, "INVITATIONS": true, "SSO_CONNECTORS": true, "SSO_BINDINGS": true, "SSO_FLOWS": true, "PLATFORM_LICENSE_STATE": true, "PLATFORM_LICENSE_ARTIFACTS": true}
	for _, v := range c.tables {
		expected := 4
		if refs[v.Name] {
			expected = 5
		}
		if len(g[v.Name]) != expected || g[v.Name]["ALTER"] || g[v.Name]["REFERENCES"] != refs[v.Name] {
			t.Fatal("unexpected authority")
		}
	}
	for _, privilege := range []string{"CREATE SESSION", "INSERT TABLE", "GRANT TABLE", "SELECT ANY DICTIONARY"} {
		if !allowedRuntimeSystemPrivilege(privilege) {
			t.Fatal("native PUBLIC privilege refused")
		}
	}
	for _, privilege := range []string{"CREATE TABLE", "CREATE ANY TABLE", "ALTER TABLE", "SELECT ANY TABLE", "GRANT ANY TABLE", "ADMIN ANY ROLE", "CREATE USER", "EXECUTE ANY PROCEDURE", ""} {
		if allowedRuntimeSystemPrivilege(privilege) {
			t.Fatal("excess authority allowed")
		}
	}
	for _, role := range []string{"DBA", "RESOURCE", "SOI", "VTI", "APP_ROLE", ""} {
		if allowedRuntimeRole(role) {
			t.Fatal("unexpected role")
		}
	}
	if !allowedRuntimeRole("PUBLIC") || !allowedRuntimeRole("SVI") {
		t.Fatal("native base roles")
	}
}

func TestDomainRuntimeGrantPolicy(t *testing.T) {
	c, e := defaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	g := requiredGrants(c)
	if len(g) != 109 || len(g[journalName]) != 1 || !g[journalName]["SELECT"] {
		t.Fatalf("domain grants changed the closed table or read-only journal boundary: tables=%d", len(g))
	}
	refs := map[string]bool{
		"TENANTS": true, "WORKSPACES": true, "USERS": true,
		"PASSWORD_RESET_TOKENS": true, "INVITATIONS": true,
		"SSO_CONNECTORS": true, "SSO_BINDINGS": true, "SSO_FLOWS": true,
		"PLATFORM_LICENSE_STATE": true, "PLATFORM_LICENSE_ARTIFACTS": true,
		"ARTIFACT_OBJECTS": true, "MANAGED_NODES": true, "NODE_TASKS": true,
		"DATASETS": true, "DATASET_DRAFTS": true, "DATASET_ROWS": true,
		"DATASET_JOBS": true, "DATASET_JOB_ATTEMPTS": true, "DATASET_VERSIONS": true,
		"MODEL_VERSIONS": true, "MODEL_IMPORTS": true, "DEPLOYMENTS": true,
		"MODEL_ALIASES": true, "MODEL_ALIAS_HISTORY": true, "API_KEYS": true,
		"GATEWAY_INSTANCES": true, "GATEWAY_REQUESTS": true, "GATEWAY_RECOVERIES": true,
		"WORKFLOWS": true, "WORKFLOW_VERSIONS": true, "WORKFLOW_CONVERSATIONS": true,
		"WORKFLOW_RUNS": true, "PLUGIN_VERSIONS": true, "PLUGIN_GRANTS": true,
		"KNOWLEDGE_BASES": true, "KNOWLEDGE_GENERATIONS": true, "KNOWLEDGE_DOCUMENTS": true,
		"KNOWLEDGE_SOURCE_VERSIONS": true, "KNOWLEDGE_ATTEMPTS": true, "MIDDLEWARE_INSTANCES": true,
		"TASK_EVENT_OUTBOX": true, "TRAINING_JOBS": true, "TRAINING_ATTEMPTS": true,
		"TRAINING_CHECKPOINTS": true, "TRAINING_ARTIFACTS": true, "TRAINING_RECOVERIES": true,
		"TRAINING_UPLOADS": true, "MODEL_HOLDOUTS": true, "MODEL_PREPARATIONS": true,
		"MODEL_PREPARATION_ATTEMPTS": true, "MODEL_PREPARATION_RECOVERIES": true,
		"MODEL_PREPARATION_UPLOADS": true, "MODEL_PREPARATION_ARTIFACTS": true,
		"MODEL_PREPARATION_REVIEWS": true, "MODEL_DERIVATIONS": true, "IMAGE_REGISTRY_PINS": true,
		"IMAGE_REGISTRY_DEPLOYMENT_REFS": true, "PRIVATE_CLUSTER_CONNECTIONS": true,
		"PRIVATE_CLUSTER_RUNS": true,
		"MODEL_ALIAS_TARGET_VERSIONS": true,
	}
	var mismatched []string
	for _, table := range c.tables {
		want := 4
		if refs[table.Name] {
			want++
		}
		got := g[table.Name]
		if len(got) != want || !got["SELECT"] || !got["INSERT"] || !got["UPDATE"] || !got["DELETE"] || got["REFERENCES"] != refs[table.Name] {
			mismatched = append(mismatched, table.Name)
		}
	}
	if len(mismatched) != 0 {
		t.Fatalf("unexpected runtime authority for %v", mismatched)
	}
}
