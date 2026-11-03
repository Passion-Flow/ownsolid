package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

// CheckAccountWithin binds a borrowed restore transaction to the exact account
// ledger before it may omit platform-only recovery owners. A selector alone
// cannot turn a platform restore into an account restore.
func CheckAccountWithin(ctx context.Context, q transaction.Executor) error {
	return CheckAccountProfileWithin(ctx, q, "account")
}

// CheckAccountProfileWithin verifies the exact minimal ledger before restore.
func CheckAccountProfileWithin(ctx context.Context, q transaction.Executor, profile string) error {
	profile = schemaProfile(profile)
	if profile == MVPPostgreSQLCPUV1Profile {
		return CheckMVPPostgreSQLCPUV1Within(ctx, q)
	}
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL {
		return errors.New("SCHEMA_INCOMPATIBLE")
	}
	if profile != "account" && profile != "workspaces" && profile != "nodes" && profile != "components" && profile != "maintenance" && profile != "models" && profile != "gateway" && profile != "datasets" && profile != "training" && profile != "lifecycle" && profile != "sso" && profile != "knowledge" && profile != "workflows" && profile != "plugins" && profile != "portal" && profile != "localization" {
		return errors.New("SCHEMA_PROFILE_INVALID")
	}
	list, e := orderedProfile(profile)
	if e != nil {
		return e
	}
	if profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization") {
		if e = CheckKnowledgeVectorWithin(ctx, q); e != nil {
			return e
		}
	}
	rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT version,checksum FROM schema_migrations ORDER BY version"))
	if e != nil {
		return errors.New("SCHEMA_UNAVAILABLE")
	}
	defer rows.Close()
	e = checkMigrationRows(rows, list)
	rows.Close()
	if e != nil {
		return e
	}
	if profile == "localization" {
		return CheckLocalizationCatalogWithin(ctx, q)
	}
	if profile == "portal" {
		return CheckPortalCatalogWithin(ctx, q)
	}
	if profile == "plugins" {
		return CheckPluginsCatalogWithin(ctx, q)
	}
	if profile == "workflows" {
		return CheckWorkflowsCatalogWithin(ctx, q)
	}
	return nil
}

// A distinct checksummed ledger cannot be silently opened as the platform schema.
// Historical platform migration bytes and all alternative engines stay unchanged.
func orderedProfile(profiles ...string) ([]migration, error) {
	profile := "platform"
	if len(profiles) > 1 {
		return nil, errors.New("SCHEMA_PROFILE_INVALID")
	}
	if len(profiles) == 1 && profiles[0] != "" {
		profile = profiles[0]
	}
	profile = schemaProfile(profile)
	if profile == MVPPostgreSQLCPUV1Profile {
		return orderedMVPPostgreSQLCPUV1()
	}
	if profile == "platform" {
		return ordered()
	}
	if profile != "account" && profile != "workspaces" && profile != "nodes" && profile != "components" && profile != "maintenance" && profile != "models" && profile != "gateway" && profile != "datasets" && profile != "training" && profile != "lifecycle" && profile != "sso" && profile != "knowledge" && profile != "workflows" && profile != "plugins" && profile != "portal" && profile != "localization" {
		return nil, errors.New("SCHEMA_PROFILE_INVALID")
	}
	paths := []string{"postgresql/000001_identity.up.sql", "account/000002_local_session_source.up.sql", "postgresql/000017_licensing.up.sql", "postgresql/000018_audit_operation_lookup.up.sql"}
	if profile == "workspaces" || profile == "nodes" || profile == "components" || profile == "maintenance" || profile == "models" || profile == "gateway" || profile == "datasets" || profile == "training" || profile == "lifecycle" || profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "postgresql/000002_workspaces.up.sql", "account/000006_workspace_authority.up.sql")
	}
	if profile == "nodes" || profile == "components" || profile == "maintenance" || profile == "models" || profile == "gateway" || profile == "datasets" || profile == "training" || profile == "lifecycle" || profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "postgresql/000003_nodes.up.sql", "account/000008_node_protocol.up.sql")
	}
	if profile == "components" || profile == "maintenance" || profile == "models" || profile == "gateway" || profile == "datasets" || profile == "training" || profile == "lifecycle" || profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "account/000009_components.up.sql")
	}
	if profile == "maintenance" || profile == "models" || profile == "gateway" || profile == "datasets" || profile == "training" || profile == "lifecycle" || profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "account/000010_maintenance.up.sql")
	}
	if profile == "models" || profile == "gateway" || profile == "datasets" || profile == "training" || profile == "lifecycle" || profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "account/000011_models.up.sql")
	}
	if profile == "gateway" || profile == "datasets" || profile == "training" || profile == "lifecycle" || profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "account/000012_gateway.up.sql")
	}
	if profile == "datasets" || profile == "training" || profile == "lifecycle" || profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "postgresql/000008_datasets.up.sql", "postgresql/000019_dataset_retirement_reason.up.sql")
	}
	if profile == "training" || profile == "lifecycle" || profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "account/000015_training.up.sql")
	}
	if profile == "lifecycle" || profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "account/000016_lifecycle.up.sql")
	}
	if profile == "sso" || (profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization")) {
		paths = append(paths, "account/000017_sso.up.sql")
	}
	if profile == "knowledge" || profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization") {
		paths = append(paths, "account/000018_knowledge.up.sql")
	}
	if profile == "workflows" || profile == "plugins" || (profile == "portal" || profile == "localization") {
		paths = append(paths, "account/000019_workflows.up.sql")
	}
	if profile == "plugins" || (profile == "portal" || profile == "localization") {
		paths = append(paths, "account/000020_plugins.up.sql")
	}
	if profile == "portal" || profile == "localization" {
		paths = append(paths, "account/000021_portal.up.sql")
	}
	if profile == "localization" {
		paths = append(paths, "account/000022_localization.up.sql")
	}
	result := make([]migration, 0, len(paths))
	for i, path := range paths {
		raw, e := migrations.Files.ReadFile(path)
		if e != nil {
			return nil, e
		}
		h := sha256.Sum256(raw)
		result = append(result, migration{i + 1, hex.EncodeToString(h[:]), raw})
	}
	return result, nil
}

// The release projection adds no SQL state. Its explicit bounded graph shares
// the immutable localization catalog; it never selects the platform ledger.
func schemaProfile(profile string) string {
	if profile == "releases" || profile == "releases-cache-v1" {
		return "localization"
	}
	return profile
}
func schemaProfiles(profiles []string) []string {
	if len(profiles) == 1 {
		return []string{schemaProfile(profiles[0])}
	}
	return profiles
}
