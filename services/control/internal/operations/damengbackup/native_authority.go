//go:build cgo && odbc_manager

package damengbackup

import (
	"context"
	"reflect"

	"github.com/google/uuid"
	accountidentity "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type AuthorityIntent struct {
	FormatVersion          int            `json:"format_version"`
	RestoreID              string         `json:"restore_id"`
	Reason                 string         `json:"reason"`
	TenantID               string         `json:"tenant_id"`
	DatabaseRoot           string         `json:"database_root"`
	DatabaseCompleteSHA256 string         `json:"database_complete_sha256"`
	FilesRoot              string         `json:"files_root"`
	FilesCompleteSHA256    string         `json:"files_complete_sha256"`
	ConfigurationSHA256    string         `json:"configuration_sha256"`
	TargetServerID         string         `json:"target_server_id"`
	TargetEndpointSHA256   string         `json:"target_endpoint_sha256"`
	PrepareCustodyID       string         `json:"prepare_custody_id"`
	Targets                []StoreBinding `json:"targets"`
}
type AuthorityPrepared struct {
	FormatVersion       int    `json:"format_version"`
	IntentSHA256        string `json:"intent_sha256"`
	PostRowsSHA256      string `json:"post_rows_sha256"`
	CatalogSHA256       string `json:"catalog_sha256"`
	AuditMetadataSHA256 string `json:"audit_metadata_sha256"`
}
type RestoredAuthority struct {
	FormatVersion        int    `json:"format_version"`
	RestoreID            string `json:"restore_id"`
	IntentSHA256         string `json:"intent_sha256"`
	PreparedSHA256       string `json:"prepared_sha256"`
	PostRowsSHA256       string `json:"post_rows_sha256"`
	TargetServerID       string `json:"target_server_id"`
	TargetEndpointSHA256 string `json:"target_endpoint_sha256"`
	// This receipt establishes invalidation/audit only. Runtime access, matched
	// secret/configuration closure and physical workload cleanup stay separate.
	Status string `json:"status"`
}

const nativeRestoreAction = "operator.dameng_data_restored"

func nativeAuthorityMetadata(i AuthorityIntent, p AuthorityPrepared) any {
	return struct {
		Format                 string `json:"format"`
		RestoreID              string `json:"restore_id"`
		IntentSHA256           string `json:"intent_sha256"`
		DatabaseCompleteSHA256 string `json:"database_complete_sha256"`
		FilesCompleteSHA256    string `json:"files_complete_sha256"`
		TargetServerID         string `json:"target_server_id"`
		TargetEndpointSHA256   string `json:"target_endpoint_sha256"`
		ConfigurationSHA256    string `json:"configuration_sha256"`
		PostRowsSHA256         string `json:"post_rows_sha256"`
		CatalogSHA256          string `json:"catalog_sha256"`
	}{"projectone.dameng.restore-authority.v1", i.RestoreID, fingerprint(i), i.DatabaseCompleteSHA256, i.FilesCompleteSHA256, i.TargetServerID, i.TargetEndpointSHA256, i.ConfigurationSHA256, p.PostRowsSHA256, p.CatalogSHA256}
}

// PrepareRestoredAuthority executes the existing all-domain recovery owner and
// its bound audit in the caller's kernel-owned transaction. It never commits,
// publishes a final receipt, grants access or removes an uncertain original.
// After WithNativeCustody returns success, reopen custody and call the read-only
// VerifyCommittedAuthority. A lost COMMIT is inspected there, never replayed.
func PrepareRestoredAuthority(ctx context.Context, c *NativeCustody, accounts *accountidentity.Service, configuration config.Config, databaseRoot, filesRoot, output, restoreID string) (prepared AuthorityPrepared, result error) {
	if c == nil || c.mode != FreshRestoreMaintenance || c.transaction == nil || validatePlatformRoots(configuration, databaseRoot, filesRoot, output) != nil {
		return prepared, fail("RESTORE_AUTHORITY_CONTEXT_REQUIRED")
	}
	id, e := uuid.Parse(restoreID)
	if e != nil || id == uuid.Nil || id.String() != restoreID {
		return prepared, fail("RESTORE_ID_INVALID")
	}
	if e = c.platformIdentity(ctx, accounts, configuration); e != nil {
		return prepared, e
	}
	database, databaseSHA, e := completeAt(ctx, c.native, databaseRoot)
	if e != nil {
		return prepared, e
	}
	databaseDir, e := openPrivateRoot(databaseRoot)
	if e != nil {
		return prepared, e
	}
	var databaseIntent Intent
	_, e = readDocument(databaseDir, "intent.json", &databaseIntent)
	databaseDir.Close()
	if e != nil || databaseIntent.Kind != "restore" || databaseIntent.Root != databaseRoot || databaseIntent.Original.ServerID != c.serverID {
		return prepared, fail("ORIGINAL_NATIVE_RESTORE_REQUIRED")
	}
	current, e := c.Inspect(ctx)
	if e != nil || current != database.Database {
		return prepared, fail("RESTORE_CHANGED_BEFORE_AUTHORITY")
	}
	filesDir, e := openPrivateRoot(filesRoot)
	if e != nil {
		return prepared, e
	}
	var files RestoredStores
	rawFiles, e := readDocument(filesDir, "files.complete.json", &files)
	filesDir.Close()
	if e != nil || files.FormatVersion != 1 || files.DatabaseCompleteSHA256 != databaseSHA || files.ConfigurationSHA256 != nativePlatformConfigurationSHA256(configuration) || len(files.Targets) != 4 {
		return prepared, fail("ORIGINAL_STORE_RESTORE_REQUIRED")
	}
	trees, e := c.storeReadback(ctx, platformRoots(configuration))
	if e != nil || !reflect.DeepEqual(trees, files.Targets) {
		return prepared, fail("RESTORED_STORES_CHANGED")
	}
	owners, e := openNativeOwners(ctx, c, accounts, configuration)
	if e != nil {
		return prepared, e
	}
	if e = owners.Verify(ctx); e != nil {
		owners.Close()
		return prepared, e
	}
	if e = owners.Close(); e != nil {
		return prepared, fail("DOMAIN_STORE_CLOSE_UNCERTAIN")
	}
	root, e := newRoot(output)
	if e != nil {
		return prepared, e
	}
	defer root.Close()
	intent := AuthorityIntent{1, restoreID, databaseIntent.Reason, c.installation, databaseRoot, databaseSHA, filesRoot, sha(rawFiles), nativePlatformConfigurationSHA256(configuration), c.serverID, c.endpoint, c.freshMarker, files.Targets}
	if e = writeNew(root, "authority.intent.json", jsonBytes(intent)); e != nil {
		return prepared, e
	}
	q := transaction.DamengTransaction(c.transaction, c.kernel)
	var count int64
	if q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT COUNT(*) FROM "AUDIT_EVENTS" WHERE "ID"=? OR "REQUEST_ID"=?`, restoreID, restoreID)).Scan(&count) != nil || count != 0 {
		return prepared, fail("RESTORE_AUDIT_CONFLICT")
	}
	if e = recovery.InvalidateRestoredWithin(ctx, accounts, q, intent.Reason); e != nil {
		return prepared, e
	}
	post, e := c.inspect(ctx, restoreID)
	if e != nil {
		return prepared, e
	}
	prepared = AuthorityPrepared{FormatVersion: 1, IntentSHA256: fingerprint(intent), PostRowsSHA256: post.RowsSHA256, CatalogSHA256: post.CatalogSHA256}
	metadata := nativeAuthorityMetadata(intent, prepared)
	prepared.AuditMetadataSHA256 = fingerprint(metadata)
	affected, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO "AUDIT_EVENTS"("ID","OCCURRED_AT","TENANT_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REASON","REQUEST_ID","METADATA") VALUES(?,CURRENT_TIMESTAMP,?,'operator',?,'success','dameng_restore',?,?,?,?)`, restoreID, c.installation, nativeRestoreAction, restoreID, intent.Reason, restoreID, string(jsonBytes(metadata))))
	if e != nil || affected != 1 {
		return AuthorityPrepared{}, fail("RESTORE_AUDIT_WRITE_FAILED")
	}
	if e = c.CheckFence(ctx); e != nil {
		return AuthorityPrepared{}, e
	}
	if e = writeNew(root, "authority.prepared.json", jsonBytes(prepared)); e != nil {
		return AuthorityPrepared{}, e
	}
	return prepared, nil
}

// VerifyCommittedAuthority only reads the original candidate, actual committed
// audit, complete post-image and four target stores. An absent/different audit
// remains unknown even when the caller suspects rollback; it cannot repeat
// invalidation or manufacture an audit. Call in newly acquired custody after the
// original transaction has returned, not inside Prepare's transaction.
func VerifyCommittedAuthority(ctx context.Context, c *NativeCustody, accounts *accountidentity.Service, configuration config.Config, output string) (RestoredAuthority, error) {
	var result RestoredAuthority
	if c == nil || c.mode != FreshRestoreMaintenance {
		return result, fail("RESTORE_AUTHORITY_CONTEXT_REQUIRED")
	}
	if e := c.platformIdentity(ctx, accounts, configuration); e != nil {
		return result, e
	}
	root, e := openPrivateRoot(output)
	if e != nil {
		return result, e
	}
	defer root.Close()
	var intent AuthorityIntent
	rawIntent, e := readDocument(root, "authority.intent.json", &intent)
	if e != nil || intent.FormatVersion != 1 || intent.ConfigurationSHA256 != nativePlatformConfigurationSHA256(configuration) || intent.TargetServerID != c.serverID || intent.TargetEndpointSHA256 != c.endpoint || intent.TenantID != c.installation {
		return result, fail("ORIGINAL_AUTHORITY_INTENT_MISMATCH")
	}
	loan, e := uuid.Parse(intent.PrepareCustodyID)
	if e != nil || loan == uuid.Nil || loan.String() != intent.PrepareCustodyID || intent.PrepareCustodyID == c.freshMarker {
		return result, fail("INDEPENDENT_COMMITTED_READBACK_REQUIRED")
	}
	var prepared AuthorityPrepared
	rawPrepared, e := readDocument(root, "authority.prepared.json", &prepared)
	if e != nil || prepared.FormatVersion != 1 || prepared.IntentSHA256 != sha(rawIntent) || !digest.MatchString(prepared.PostRowsSHA256) || !digest.MatchString(prepared.CatalogSHA256) {
		return result, fail("ORIGINAL_AUTHORITY_CANDIDATE_REQUIRED")
	}
	id, e := uuid.Parse(intent.RestoreID)
	if e != nil || id == uuid.Nil || id.String() != intent.RestoreID {
		return result, fail("RESTORE_ID_INVALID")
	}
	metadata := nativeAuthorityMetadata(intent, prepared)
	if fingerprint(metadata) != prepared.AuditMetadataSHA256 {
		return result, fail("RESTORE_AUDIT_METADATA_CHANGED")
	}
	var tenant, audience, action, outcome, targetKind, target, reason, request, actualMetadata string
	if c.query.QueryRowContext(ctx, `SELECT "TENANT_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REASON","REQUEST_ID","METADATA" FROM "AUDIT_EVENTS" WHERE "ID"=?`, intent.RestoreID).Scan(&tenant, &audience, &action, &outcome, &targetKind, &target, &reason, &request, &actualMetadata) != nil {
		return result, fail("RESTORE_AUTHORITY_OUTCOME_UNKNOWN")
	}
	if tenant != intent.TenantID || audience != "operator" || action != nativeRestoreAction || outcome != "success" || targetKind != "dameng_restore" || target != intent.RestoreID || reason != intent.Reason || request != intent.RestoreID || actualMetadata != string(jsonBytes(metadata)) {
		return result, fail("RESTORE_AUDIT_CONFLICT")
	}
	_, databaseSHA, e := completeAt(ctx, c.native, intent.DatabaseRoot)
	if e != nil || databaseSHA != intent.DatabaseCompleteSHA256 {
		return result, fail("ORIGINAL_DATABASE_PROOF_CHANGED")
	}
	fileRoot, e := openPrivateRoot(intent.FilesRoot)
	if e != nil {
		return result, e
	}
	var files RestoredStores
	rawFiles, e := readDocument(fileRoot, "files.complete.json", &files)
	fileRoot.Close()
	if e != nil || sha(rawFiles) != intent.FilesCompleteSHA256 || !reflect.DeepEqual(files.Targets, intent.Targets) {
		return result, fail("ORIGINAL_FILES_PROOF_CHANGED")
	}
	trees, e := c.storeReadback(ctx, platformRoots(configuration))
	if e != nil || !reflect.DeepEqual(trees, intent.Targets) {
		return result, fail("RESTORED_STORES_CHANGED")
	}
	post, e := c.inspect(ctx, intent.RestoreID)
	if e != nil || post.RowsSHA256 != prepared.PostRowsSHA256 || post.CatalogSHA256 != prepared.CatalogSHA256 {
		return result, fail("RESTORE_POST_IMAGE_CHANGED")
	}
	if e = c.CheckFence(ctx); e != nil {
		return result, e
	}
	result = RestoredAuthority{1, intent.RestoreID, sha(rawIntent), sha(rawPrepared), post.RowsSHA256, c.serverID, c.endpoint, "authority_invalidated_audit_verified_runtime_access_pending"}
	if _, e = root.Lstat("authority.complete.json"); e == nil {
		var original RestoredAuthority
		if _, e = readDocument(root, "authority.complete.json", &original); e != nil || original != result {
			return RestoredAuthority{}, fail("AUTHORITY_COMPLETION_CHANGED")
		}
		return original, nil
	}
	if e = writeNew(root, "authority.complete.json", jsonBytes(result)); e != nil {
		return RestoredAuthority{}, e
	}
	return result, nil
}
