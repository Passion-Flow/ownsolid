package plugins

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func (s *Service) currentTool(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace, version string) (workspaces.Workspace, versionRecord, grantRecord, error) {
	var w workspaces.Workspace
	var v versionRecord
	var g grantRecord
	if e := s.executor(ctx, tx); e != nil {
		return w, v, g, e
	}
	if !s.available() || s.Models == nil {
		return w, v, g, failure("PLUGIN_UNAVAILABLE")
	}
	if s.Models.Identity != s.Identity || s.Models.Workspaces != s.Workspaces {
		return w, v, g, failure("PLUGIN_STORAGE_MISMATCH")
	}
	if !validID(workspace) || !validID(version) || a.Me.Audience != "portal" && !admin(a) {
		return w, v, g, failure("ACCESS_DENIED")
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, tx, a); e != nil {
		return w, v, g, e
	}
	var e error
	w, e = s.Workspaces.RecheckScopeWithin(ctx, tx, a, workspace)
	if e != nil {
		return w, v, g, e
	}
	if w.Status != "active" || w.Role == nil {
		return w, v, g, failure("ACCESS_DENIED")
	}
	// Check scoped presence before exposing package state/trust. Do not retain
	// a grant lock here: uninstall and invocation still lock version -> grant.
	var present bool
	if e = tx.QueryRow(ctx, pluginSQL(tx, "SELECT EXISTS(SELECT 1 FROM plugin_grants WHERE tenant_id=$1 AND workspace_id=$2 AND plugin_version_id=$3)", "SELECT EXISTS(SELECT 1 FROM plugin_grants WHERE tenant_id=? AND workspace_id=? AND plugin_version_id=?)", nativePluginGrantExists, w.TenantID, w.ID, version)).Scan(&present); e != nil {
		return w, v, g, e
	}
	if !present {
		return w, v, g, failure("PLUGIN_GRANT_NOT_FOUND")
	}
	v, e = scanVersion(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+versionFields+" FROM plugin_versions WHERE tenant_id=$1 AND id=$2 FOR SHARE", "SELECT "+mysqlVersionFields+" FROM plugin_versions WHERE tenant_id=? AND id=? FOR SHARE", nativePluginVersionLocked, w.TenantID, version)))
	if e != nil {
		return w, v, g, e
	}
	if v.State != InstalledState {
		return w, v, g, failure("PLUGIN_NOT_INSTALLED")
	}
	if _, e = s.verified(v); e != nil {
		return w, v, g, e
	}
	g, e = scanGrant(tx.QueryRow(ctx, pluginSQL(tx, "SELECT "+grantFields+" FROM plugin_grants g WHERE g.tenant_id=$1 AND g.workspace_id=$2 AND g.plugin_version_id=$3 FOR SHARE", "SELECT "+mysqlGrantFields+" FROM plugin_grants g WHERE g.tenant_id=? AND g.workspace_id=? AND g.plugin_version_id=? FOR SHARE", nativePluginGrantVersionLocked, w.TenantID, w.ID, version)))
	if e != nil {
		return w, v, g, e
	}
	if !g.Enabled {
		return w, v, g, failure("PLUGIN_GRANT_REVOKED")
	}
	return w, v, g, nil
}

// CheckToolWithin validates a graph reference with the invoking caller,
// never with installer/publisher authority and never by trusting graph results.
func (s *Service) CheckToolWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace, version string) error {
	_, _, _, e := s.currentTool(ctx, tx, a, workspace, version)
	return e
}
func (s *Service) ExecuteModelListWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace, version, run, question string) (*CompletedModelList, error) {
	if !validID(run) {
		return nil, failure("VALIDATION_FAILED")
	}
	w, v, g, e := s.currentTool(ctx, tx, a, workspace, version)
	if e != nil {
		return nil, e
	}
	if s.Models.Identity != s.Identity || s.Models.Workspaces != s.Workspaces {
		return nil, failure("PLUGIN_STORAGE_MISMATCH")
	}
	models, e := s.Models.ReadyReferencesWithin(ctx, tx, a, w.ID, s.policy().MaximumModels)
	if e != nil {
		return nil, e
	}
	result := ModelList{Items: []ModelReference{}}
	for _, m := range models {
		result.Items = append(result.Items, ModelReference{ID: m.ID, Name: m.Name, Kind: m.Kind, ModelKey: m.ModelKey, CatalogDigest: m.CatalogDigest})
	}
	raw, e := json.Marshal(result)
	if e != nil || len(raw) > s.policy().ResultBytes {
		return nil, failure("PLUGIN_RESULT_LIMIT_EXCEEDED")
	}
	proof := storedProof{SchemaVersion: 1, WorkspaceID: w.ID, VersionID: v.ID, CapsuleSHA256: v.CapsuleSHA256, GrantID: g.ID, GrantAuthority: g.authority, ModelReferences: append([]ModelReference{}, result.Items...)}
	encoded, e := json.Marshal(proof)
	if e != nil || len(encoded) > 65536 {
		return nil, failure("PLUGIN_RESULT_LIMIT_EXCEEDED")
	}
	if e = s.audit(ctx, tx, a, w.TenantID, w.ID, "plugin.invoked", "plugin_version", v.ID, "Current-caller read-only model directory", run); e != nil {
		return nil, e
	}
	return &CompletedModelList{service: s, actor: a.Me.User.ID, audience: a.Me.Audience, session: a.SessionID, run: run, question: question, tokenFingerprint: sha256.Sum256(a.TokenHash), proof: proof, result: result}, nil
}
func (s *Service) RecheckCompletedModelListWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace, version, run, question string, completed *CompletedModelList) error {
	if completed == nil || completed.service != s || completed.actor != a.Me.User.ID || completed.audience != a.Me.Audience || completed.session != a.SessionID || completed.run != run || completed.question != question {
		return failure("PLUGIN_AUTHORITY_INVALID")
	}
	fingerprint := sha256.Sum256(a.TokenHash)
	if subtle.ConstantTimeCompare(completed.tokenFingerprint[:], fingerprint[:]) != 1 {
		return failure("PLUGIN_AUTHORITY_INVALID")
	}
	if completed.proof.WorkspaceID != workspace || completed.proof.VersionID != version {
		return failure("PLUGIN_AUTHORITY_INVALID")
	}
	return s.recheckProof(ctx, tx, a, workspace, version, completed.proof, completed.result, true)
}
func (s *Service) recheckProof(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace, version string, proof storedProof, result ModelList, execution bool) error {
	w, v, g, e := s.currentTool(ctx, tx, a, workspace, version)
	if e != nil {
		return e
	}
	if proof.SchemaVersion != 1 || proof.WorkspaceID != workspace || proof.VersionID != version || proof.CapsuleSHA256 != v.CapsuleSHA256 || proof.GrantID != g.ID || execution && proof.GrantAuthority != g.authority || len(proof.ModelReferences) > 100 || len(proof.ModelReferences) != len(result.Items) {
		return failure("PLUGIN_AUTHORITY_CHANGED")
	}
	seen := map[string]bool{}
	for i, ref := range proof.ModelReferences {
		if !validID(ref.ID) || seen[ref.ID] || ref != result.Items[i] {
			return failure("PLUGIN_RESULT_INVALID")
		}
		seen[ref.ID] = true
		if e = s.Models.CheckToolReferenceWithin(ctx, tx, w.TenantID, w.ID, ref.ID, ref.Name, ref.Kind, ref.ModelKey, ref.CatalogDigest); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) RecheckStoredModelListWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace, version string, proofRaw, resultRaw []byte) error {
	if e := s.executor(ctx, tx); e != nil {
		return e
	}
	if len(proofRaw) > 65536 || len(resultRaw) > s.policy().ResultBytes {
		return failure("PLUGIN_RESULT_LIMIT_EXCEEDED")
	}
	var proof storedProof
	var result ModelList
	if strict.DecodeStrict(proofRaw, &proof) != nil || strict.DecodeStrict(resultRaw, &result) != nil || proof.ModelReferences == nil || result.Items == nil {
		return failure("PLUGIN_RESULT_INVALID")
	}
	return s.recheckProof(ctx, tx, a, workspace, version, proof, result, false)
}

// RecordPublicationWithin freezes only the exact declaration/grant
// provenance. Invocation still uses the future caller's current authority.
func (s *Service) RecordPublicationWithin(ctx context.Context, tx transaction.Executor, a identity.Auth, workspace, version, workflow, publication string) error {
	w, v, g, e := s.currentTool(ctx, tx, a, workspace, version)
	if e != nil {
		return e
	}
	e = pluginExecOne(ctx, tx, pluginSQL(tx, `INSERT INTO workflow_plugin_references(tenant_id,workspace_id,workflow_id,workflow_version_id,plugin_version_id,grant_id,publication_grant_authority) VALUES($1,$2,$3,$4,$5,$6,$7)`, `INSERT INTO workflow_plugin_references(tenant_id,workspace_id,workflow_id,workflow_version_id,plugin_version_id,grant_id,publication_grant_authority) VALUES(?,?,?,?,?,?,?)`, nativePluginPublication, w.TenantID, w.ID, workflow, publication, v.ID, g.ID, g.authority))
	return e
}
