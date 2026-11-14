package migrate

import (
	"encoding/json"
	"reflect"
	"strings"
)

// This is a reviewed compiled contract, not a configurable readiness switch.
// The witness binds the retained 459-step prefix followed by c54ac8d9's exact
// inference_cutovers_v2 contribution. Changing a historical byte requires a
// separately reviewed migration contract; a matching table count cannot admit it.
const controlServingCatalogSHA256 = "5638884b122cea0c2f414d143bb0f3a165b27bf647f2113b3ff1e619a62dc726"
const controlServingSteps = 476

// Business relations are independently enumerated, excluding the read-only
// migration journal. They cover the current 107 PostgreSQL relations and the
// native publication guard. No registration is performed by this inventory.
const controlServingRelations = `
API_KEYS ARTIFACT_OBJECTS ARTIFACT_OBJECT_GRANTS ARTIFACT_OBJECT_INGESTIONS ARTIFACT_OBJECT_PARTS
AUDIT_EVENTS AWS_DISCOVERED_RESOURCES AWS_IMPORT_RECEIPTS COMPONENT_LOG_SNAPSHOTS
DATASETS DATASET_DRAFTS DATASET_JOBS DATASET_JOB_ATTEMPTS DATASET_ROWS DATASET_ROW_HISTORY DATASET_VERSIONS
DEPLOYMENTS GATEWAY_INSTANCES GATEWAY_RECOVERIES GATEWAY_REQUESTS GPU_LEASES IDEMPOTENCY_RECORDS
IMAGE_REGISTRY_BLOBS IMAGE_REGISTRY_DEPLOYMENT_REFS IMAGE_REGISTRY_GRANTS IMAGE_REGISTRY_IMPORTS
IMAGE_REGISTRY_MUTATIONS IMAGE_REGISTRY_OFFLINE_REFS IMAGE_REGISTRY_PINS INFERENCE_NODES INSTALLATION_STATE INVITATIONS
KNOWLEDGE_ATTEMPTS KNOWLEDGE_BASES KNOWLEDGE_CHUNKS KNOWLEDGE_DOCUMENTS KNOWLEDGE_GENERATIONS
KNOWLEDGE_MUTATION_INTENTS KNOWLEDGE_SOURCE_VERSIONS MAIL_DELIVERIES MANAGED_COMPONENTS MANAGED_NODES
MIDDLEWARE_INSTANCES MIDDLEWARE_OPERATIONS MODEL_ALIASES MODEL_ALIAS_CUTOVER_INTENTS MODEL_ALIAS_HISTORY
MODEL_ALIAS_TARGET_VERSIONS MODEL_DERIVATIONS MODEL_HOLDOUTS MODEL_HOLDOUT_PROMPTS MODEL_HOLDOUT_SOURCES
MODEL_IMPORTS MODEL_IMPORT_CHUNKS MODEL_PREPARATIONS MODEL_PREPARATION_ARTIFACTS MODEL_PREPARATION_ATTEMPTS
MODEL_PREPARATION_EVENTS MODEL_PREPARATION_RECOVERIES MODEL_PREPARATION_REVIEWS MODEL_PREPARATION_UPLOADS
MODEL_PREPARATION_UPLOAD_CHUNKS MODEL_PUBLICATIONS MODEL_VERSIONS MONGODB_DATASET_IMPORTS NODE_JOIN_TOKENS NODE_TASKS
PASSWORD_CREDENTIALS PASSWORD_RESET_TOKENS PLATFORM_LICENSE_ARTIFACTS PLATFORM_LICENSE_STATE PLUGIN_GRANTS
PLUGIN_MUTATION_INTENTS PLUGIN_VERSIONS PRIVATE_CLUSTER_CONNECTIONS PRIVATE_CLUSTER_INTENTS PRIVATE_CLUSTER_RUNS
RATE_LIMIT_BUCKETS SESSIONS SSO_BINDINGS SSO_CONNECTORS SSO_FLOWS SSO_MUTATION_INTENTS TASK_EVENT_DEAD_LETTERS
TASK_EVENT_OUTBOX TASK_EVENT_RECEIPTS TENANTS TERMINAL_NOTIFICATIONS TRAINING_ARTIFACTS TRAINING_ATTEMPTS
TRAINING_CHECKPOINTS TRAINING_EVENTS TRAINING_JOBS TRAINING_RECOVERIES TRAINING_UPLOADS TRAINING_UPLOAD_CHUNKS
USERS USER_PREFERENCES WORKFLOWS WORKFLOW_CONVERSATIONS WORKFLOW_MUTATION_INTENTS WORKFLOW_NODE_TRACES
WORKFLOW_PLUGIN_REFERENCES WORKFLOW_RUNS WORKFLOW_VERSIONS WORKSPACES WORKSPACE_MEMBERSHIPS WORKSPACE_NOTES
`

// RequireControlServingCatalog verifies the complete compiled migration and
// restricted-grant graph without I/O. It proves no database state, installed
// schema, held lock or live authority. Serving must still independently run
// CheckRuntime against the admitted native session and its actual metadata.
func RequireControlServingCatalog() error {
	c, err := defaultCatalog()
	if err != nil {
		return err
	}
	return requireControlServingCatalog(c)
}

func requireControlServingCatalog(c catalog) error {
	bad := fail("DAMENG_CONTROL_SERVING_CATALOG_INCOMPLETE")
	relations := strings.Fields(controlServingRelations)
	if len(c.steps) != controlServingSteps || len(c.tables) != len(relations) {
		return bad
	}
	for n, s := range c.steps {
		if s.Number != n+1 || !identifier.MatchString(s.Object) || s.SQL == "" || len(s.SHA256) != 64 {
			return bad
		}
	}
	p := c.project(len(c.steps))
	if len(p.tables) != len(relations)+1 || p.tables[journalName].Name != journalName {
		return bad
	}
	seen := map[string]bool{}
	for _, name := range relations {
		if !identifier.MatchString(name) || seen[name] || name == journalName {
			return bad
		}
		seen[name] = true
		if t, exists := p.tables[name]; !exists || t.Name != name || validateShape(t, p.tables, nil) != nil {
			return bad
		}
	}
	references := map[string]bool{}
	tables := map[string]bool{}
	for _, t := range c.tables {
		if !seen[t.Name] || tables[t.Name] || !reflect.DeepEqual(t, p.tables[t.Name]) {
			return bad
		}
		tables[t.Name] = true
		for _, key := range t.Constraints {
			if key.Kind == "R" {
				if !seen[key.Reference] {
					return bad
				}
				references[key.Reference] = true
			}
		}
	}
	// Validate referenced targets before requiredGrants so a malformed graph
	// cannot index a nonexistent target map or manufacture broad authority.
	grants := requiredGrants(c)
	if len(grants) != len(seen)+1 || !reflect.DeepEqual(grants[journalName], map[string]bool{"SELECT": true}) {
		return bad
	}
	for name := range seen {
		allowed := map[string]bool{"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true}
		if references[name] {
			allowed["REFERENCES"] = true
		}
		if !reflect.DeepEqual(grants[name], allowed) {
			return bad
		}
	}
	actual, err := controlServingCatalogDigest(c, p, grants)
	if err != nil || actual != controlServingCatalogSHA256 {
		return bad
	}
	return nil
}

// JSON's deterministic map-key ordering is part of this versioned witness. All
// fields are included: legacy step hashes/SQL, ALTER before/after shapes, probes,
// trigger bodies, index direction, delete rules, final tables and exact grants.
// It is recomputed from this invocation's catalog; there is no readiness cache.
func controlServingCatalogDigest(c catalog, p projection, grants map[string]map[string]bool) (string, error) {
	bound := struct {
		Version         int
		Scope           string
		Journal         table
		Steps           []step
		Tables          []table
		Evolution       map[int]schemaChange
		DeleteRules     map[string]string
		Fragments       map[string]bool
		ProjectedTables map[string]table
		Triggers        map[string]trigger
		Grants          map[string]map[string]bool
	}{1, Scope, c.journal, c.steps, c.tables, c.evolution, c.deleteRules, c.fragments, p.tables, p.triggers, grants}
	raw, err := json.Marshal(bound)
	if err != nil {
		return "", fail("DAMENG_CONTROL_SERVING_CATALOG_INCOMPLETE")
	}
	return digest(string(raw)), nil
}
