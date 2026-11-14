package models

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

const (
	derivedStageState        = "staged"
	derivedReadyState        = "ready"
	derivedRetiredState      = "deleted"
	preparationReviewState   = "awaiting_review"
	preparationApprovedState = "approved"
	preparationOutputState   = "prepared"
)

// The owning review transaction retains authority, audit and final projection.
// A private verified-file token never implies human approval or model quality.
func (s *Service) registrationScope(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, p PreparedDerivedFiles, review, name string) (wire.DerivedCatalog, error) {
	var zero wire.DerivedCatalog
	if s == nil || ctx == nil || q == nil {
		return zero, fail("STORAGE_UNAVAILABLE")
	}
	c := p.Catalog()
	if !admin(a) || !validID(review) || c.Scope.TenantID != w.TenantID || c.Scope.WorkspaceID != w.ID {
		return zero, fail("ACCESS_DENIED")
	}
	normalized, e := security.Name(name)
	if e != nil || normalized != name {
		return zero, fail("VALIDATION_FAILED")
	}
	if s.Identity == nil || s.Workspaces == nil {
		return zero, fail("MODEL_DERIVED_REGISTRATION_UNAVAILABLE")
	}
	if e = s.accessExecutor(ctx, q, w.TenantID, w.ID, c.ModelID); e != nil {
		return zero, e
	}
	if e = s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
		return zero, e
	}
	current, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, w.ID)
	if e != nil {
		return zero, e
	}
	if current.TenantID != w.TenantID || current.Status != "active" {
		return zero, fail("ACCESS_DENIED")
	}
	if e = s.verifyPreparedDerivedToken(p); e != nil {
		return zero, e
	}
	// Seal attributes do not establish directory immutability or receipt bytes.
	// Check bounded metadata here; complete model hashing precedes this transaction.
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return zero, e
	}
	e = verifyDerivedMetadata(root, c)
	closeError := root.Close()
	if e != nil {
		return zero, e
	}
	if closeError != nil {
		return zero, derivedFailure()
	}
	if e = s.ReadyWithin(ctx, q, w.TenantID, w.ID, c.BaseModelID); e != nil {
		return zero, e
	}
	return c, nil
}

func (s *Service) registrationPreparation(ctx context.Context, q transaction.Executor, c wire.DerivedCatalog) error {
	var state, attempt, base, output string
	var report []byte
	var stopped bool
	e := q.QueryRow(ctx, modelSQL(q,
		`SELECT p.state,p.current_attempt_id::text,p.model_id::text,f.report_bytes,a.state,a.physical_stopped_at IS NOT NULL FROM model_preparations p JOIN model_preparation_artifacts f ON f.tenant_id=p.tenant_id AND f.workspace_id=p.workspace_id AND f.preparation_id=p.id AND f.attempt_id=p.current_attempt_id JOIN model_preparation_attempts a ON a.tenant_id=f.tenant_id AND a.workspace_id=f.workspace_id AND a.preparation_id=f.preparation_id AND a.id=f.attempt_id WHERE p.tenant_id=$1 AND p.workspace_id=$2 AND p.id=$3 AND f.id=$4 FOR SHARE OF p,f,a`,
		`SELECT p.state,p.current_attempt_id,p.model_id,f.report_bytes,a.state,a.physical_stopped_at IS NOT NULL FROM model_preparations p JOIN model_preparation_artifacts f ON f.tenant_id=p.tenant_id AND f.workspace_id=p.workspace_id AND f.preparation_id=p.id AND f.attempt_id=p.current_attempt_id JOIN model_preparation_attempts a ON a.tenant_id=f.tenant_id AND a.workspace_id=f.workspace_id AND a.preparation_id=f.preparation_id AND a.id=f.attempt_id WHERE p.tenant_id=? AND p.workspace_id=? AND p.id=? AND f.id=? FOR SHARE`, c.Scope.TenantID, c.Scope.WorkspaceID, c.Scope.PreparationID, c.ArtifactID)).Scan(&state, &attempt, &base, &report, &output, &stopped)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("MODEL_DERIVED_REVIEW_REQUIRED")
	}
	if e != nil {
		return modelReadError(e)
	}
	sum := sha256.Sum256(report)
	if state != preparationReviewState && state != preparationApprovedState || attempt != c.AttemptID || base != c.BaseModelID || output != preparationOutputState || !stopped || hex.EncodeToString(sum[:]) != c.ReportSHA256 {
		return fail("MODEL_DERIVED_REVIEW_REQUIRED")
	}
	return nil
}

// StageDerivedWithin is one part of the caller's atomic review workflow. Never
// commit it independently: PostgreSQL defers its graph, MySQL keeps it unservable.
func (s *Service) StageDerivedWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, p PreparedDerivedFiles, review, name string) (string, error) {
	c, e := s.registrationScope(ctx, q, a, w, p, review, name)
	if e != nil {
		return "", e
	}
	if e = s.registrationPreparation(ctx, q, c); e != nil {
		return "", e
	}
	var kind, digest, state, tenant, workspace, storedName, creator string
	var raw []byte
	e = q.QueryRow(ctx, modelSQL(q,
		`SELECT kind,catalog_digest,manifest,state,tenant_id::text,workspace_id::text,name,created_by::text FROM model_versions WHERE id=$1 FOR UPDATE`,
		`SELECT kind,catalog_digest,manifest,state,tenant_id,workspace_id,name,created_by FROM model_versions WHERE id=? FOR UPDATE`, c.ModelID)).Scan(&kind, &digest, &raw, &state, &tenant, &workspace, &storedName, &creator)
	if e == nil {
		stored, decodeError := wire.DecodeDerivedCatalog(raw)
		actual, digestError := stored.Digest()
		if decodeError != nil || digestError != nil || kind != agentwire.DerivedModelKind || digest != p.Digest() || actual != digest || tenant != w.TenantID || workspace != w.ID || storedName != name || creator != a.Me.User.ID {
			return "", fail("MODEL_DERIVED_IDENTITY_CONFLICT")
		}
		if state == derivedRetiredState {
			return "", fail("MODEL_DERIVED_ALREADY_RETIRED")
		}
		if state != derivedReadyState && !(q.Dialect() == transaction.MySQL && state == derivedStageState) {
			return "", fail("MODEL_DERIVED_IDENTITY_CONFLICT")
		}
		return c.ModelID, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", modelReadError(e)
	}
	raw, e = c.CanonicalBytes()
	if e != nil {
		return "", e
	}
	state = derivedReadyState
	if q.Dialect() == transaction.MySQL {
		state = derivedStageState
	}
	n, e := q.Exec(ctx, modelSQL(q,
		`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,'merged_lora')`,
		`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES(?,?,?,?,?,?,CAST(? AS JSON),?,?,'merged_lora')`, c.ModelID, w.TenantID, w.ID, name, agentwire.ApprovedModel().ModelKey, p.Digest(), raw, state, a.Me.User.ID))
	if e != nil {
		return "", modelReadError(e)
	}
	if n != 1 {
		return "", fail("STORAGE_UNAVAILABLE")
	}
	return c.ModelID, nil
}

func (s *Service) registrationReview(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, c wire.DerivedCatalog, review, name string, approved bool) error {
	var decision, artifact, report, preparation, attempt, reviewer, modelName, base, currentAttempt, state string
	var model, parentReview, parentModel *string
	var expected, revision int64
	e := q.QueryRow(ctx, modelSQL(q,
		`SELECT r.decision,r.artifact_id::text,r.report_sha256,r.preparation_id::text,r.attempt_id::text,r.reviewed_by::text,r.model_name,r.model_id::text,p.model_id::text,p.current_attempt_id::text,p.state,p.review_id::text,p.derived_model_id::text,r.expected_revision,p.revision FROM model_preparation_reviews r JOIN model_preparations p ON p.tenant_id=r.tenant_id AND p.workspace_id=r.workspace_id AND p.id=r.preparation_id WHERE r.tenant_id=$1 AND r.workspace_id=$2 AND r.id=$3 FOR SHARE OF r,p`,
		`SELECT r.decision,r.artifact_id,r.report_sha256,r.preparation_id,r.attempt_id,r.reviewed_by,r.model_name,r.model_id,p.model_id,p.current_attempt_id,p.state,p.review_id,p.derived_model_id,r.expected_revision,p.revision FROM model_preparation_reviews r JOIN model_preparations p ON p.tenant_id=r.tenant_id AND p.workspace_id=r.workspace_id AND p.id=r.preparation_id WHERE r.tenant_id=? AND r.workspace_id=? AND r.id=? FOR SHARE`, w.TenantID, w.ID, review)).Scan(&decision, &artifact, &report, &preparation, &attempt, &reviewer, &modelName, &model, &base, &currentAttempt, &state, &parentReview, &parentModel, &expected, &revision)
	if errors.Is(e, sql.ErrNoRows) {
		return fail("MODEL_DERIVED_REVIEW_REQUIRED")
	}
	if e != nil {
		return modelReadError(e)
	}
	if decision != "approve" || artifact != c.ArtifactID || report != c.ReportSHA256 || preparation != c.Scope.PreparationID || attempt != c.AttemptID || reviewer != a.Me.User.ID || modelName != name || model == nil || *model != c.ModelID || base != c.BaseModelID || currentAttempt != c.AttemptID {
		return fail("MODEL_DERIVED_REVIEW_REQUIRED")
	}
	if state == preparationApprovedState {
		if parentReview == nil || *parentReview != review || parentModel == nil || *parentModel != c.ModelID || revision != expected+1 {
			return fail("MODEL_DERIVED_REVIEW_REQUIRED")
		}
	} else if approved || state != preparationReviewState || parentReview != nil || parentModel != nil || revision != expected {
		return fail("MODEL_DERIVED_REVIEW_REQUIRED")
	}
	return nil
}

func (s *Service) registrationLineage(ctx context.Context, q transaction.Executor, w workspaces.Workspace, c wire.DerivedCatalog, review, digest string) (bool, error) {
	raw, e := c.CanonicalBytes()
	if e != nil {
		return false, e
	}
	var exact bool
	e = q.QueryRow(ctx, modelSQL(q,
		`SELECT EXISTS(SELECT 1 FROM model_derivations WHERE tenant_id=$1 AND workspace_id=$2 AND model_id=$3 AND review_id=$4 AND catalog_digest=$5 AND preparation_id=$6 AND attempt_id=$7 AND artifact_id=$8 AND base_model_id=$9 AND report_sha256=$10 AND catalog=$11::jsonb)`,
		`SELECT EXISTS(SELECT 1 FROM model_derivations WHERE tenant_id=? AND workspace_id=? AND model_id=? AND review_id=? AND catalog_digest=? AND preparation_id=? AND attempt_id=? AND artifact_id=? AND base_model_id=? AND report_sha256=? AND catalog=CAST(? AS JSON))`, w.TenantID, w.ID, c.ModelID, review, digest, c.Scope.PreparationID, c.AttemptID, c.ArtifactID, c.BaseModelID, c.ReportSHA256, raw)).Scan(&exact)
	return exact, modelReadError(e)
}

// RegisterDerivedWithin binds the owner's real review to the verified immutable
// files. MySQL owners must have staged before inserting their review record.
func (s *Service) RegisterDerivedWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, p PreparedDerivedFiles, review, name string) (string, error) {
	c, e := s.registrationScope(ctx, q, a, w, p, review, name)
	if e != nil {
		return "", e
	}
	if e = s.registrationReview(ctx, q, a, w, c, review, name, false); e != nil {
		return "", e
	}
	if _, e = s.StageDerivedWithin(ctx, q, a, w, p, review, name); e != nil {
		return "", e
	}
	exact, e := s.registrationLineage(ctx, q, w, c, review, p.Digest())
	if e != nil {
		return "", e
	}
	if exact {
		return c.ModelID, nil
	}
	var exists bool
	e = q.QueryRow(ctx, modelSQL(q, `SELECT EXISTS(SELECT 1 FROM model_derivations WHERE model_id=$1)`, `SELECT EXISTS(SELECT 1 FROM model_derivations WHERE model_id=?)`, c.ModelID)).Scan(&exists)
	if e != nil {
		return "", modelReadError(e)
	}
	if exists {
		return "", fail("MODEL_DERIVED_IDENTITY_CONFLICT")
	}
	raw, e := c.CanonicalBytes()
	if e != nil {
		return "", e
	}
	n, e := q.Exec(ctx, modelSQL(q,
		`INSERT INTO model_derivations(model_id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,base_model_id,review_id,catalog_digest,catalog,report_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11)`,
		`INSERT INTO model_derivations(model_id,tenant_id,workspace_id,preparation_id,attempt_id,artifact_id,base_model_id,review_id,catalog_digest,catalog,report_sha256) VALUES(?,?,?,?,?,?,?,?,?,CAST(? AS JSON),?)`, c.ModelID, w.TenantID, w.ID, c.Scope.PreparationID, c.AttemptID, c.ArtifactID, c.BaseModelID, review, p.Digest(), raw, c.ReportSHA256))
	if e != nil {
		return "", modelReadError(e)
	}
	if n != 1 {
		return "", fail("STORAGE_UNAVAILABLE")
	}
	return c.ModelID, nil
}

// FinalizeDerivedWithin follows the approved parent update; its caller still
// owns the reviewed-decision audit, commit and suppression on unknown outcome.
func (s *Service) FinalizeDerivedWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, p PreparedDerivedFiles, review, name string) (string, error) {
	c, e := s.registrationScope(ctx, q, a, w, p, review, name)
	if e != nil {
		return "", e
	}
	if e = s.registrationReview(ctx, q, a, w, c, review, name, true); e != nil {
		return "", e
	}
	exact, e := s.registrationLineage(ctx, q, w, c, review, p.Digest())
	if e != nil {
		return "", e
	}
	if !exact {
		return "", fail("MODEL_DERIVED_REVIEW_REQUIRED")
	}
	if q.Dialect() == transaction.MySQL {
		var published bool
		e = q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT EXISTS(SELECT 1 FROM model_publications WHERE tenant_id=? AND workspace_id=? AND model_id=?)`, w.TenantID, w.ID, c.ModelID)).Scan(&published)
		if e != nil {
			return "", modelReadError(e)
		}
		if !published {
			n, e := q.Exec(ctx, transaction.SQL(transaction.MySQL, `INSERT INTO model_publications(model_id,tenant_id,workspace_id) VALUES(?,?,?)`, c.ModelID, w.TenantID, w.ID))
			if e != nil {
				return "", modelReadError(e)
			}
			if n != 1 {
				return "", fail("STORAGE_UNAVAILABLE")
			}
		}
	}
	stored, e := s.DecodeDerivedVersionWithin(ctx, q, w.TenantID, w.ID, c.ModelID)
	if e != nil {
		return "", e
	}
	digest, e := stored.Digest()
	if e != nil || digest != p.Digest() {
		return "", fail("MODEL_DERIVED_IDENTITY_CONFLICT")
	}
	return c.ModelID, nil
}

func (s *Service) OpenReviewAssetWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, model, name string) (*os.File, datawire.Asset, error) {
	var zero datawire.Asset
	if s == nil || ctx == nil || q == nil {
		return nil, zero, fail("STORAGE_UNAVAILABLE")
	}
	if !admin(a) || s.Identity == nil || s.Workspaces == nil {
		return nil, zero, fail("ACCESS_DENIED")
	}
	if e := s.accessExecutor(ctx, q, w.TenantID, w.ID, model); e != nil {
		return nil, zero, e
	}
	if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
		return nil, zero, e
	}
	current, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, w.ID)
	if e != nil {
		return nil, zero, e
	}
	if current.TenantID != w.TenantID || current.Status != "active" {
		return nil, zero, fail("ACCESS_DENIED")
	}
	file, asset, e := s.OpenVerificationAssetWithin(ctx, q, w.TenantID, w.ID, model, name, "")
	if e != nil {
		if file != nil {
			file.Close()
		}
		return nil, zero, e
	}
	return file, asset, nil
}
