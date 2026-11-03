package artifactstores

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type ingestionInput struct {
	Grant   string                `json:"grant_id"`
	Model   *ModelImportRequest   `json:"model,omitempty"`
	Dataset *DatasetImportRequest `json:"dataset,omitempty"`
}
type ingestionRow struct {
	Ingestion
	Creator        string
	Input          []byte
	Dataset        *string
	VerifiedDigest *string
}

func (s *Service) IngestModel(ctx context.Context, a identity.Auth, workspace, object, grant string, input ModelImportRequest, key, request string) (Ingestion, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Ingestion{}, admissionError
	}
	defer finish()
	if !admin(a) || !input.LicenseAcknowledged || !workspaces.ValidReason(input.Reason) {
		return Ingestion{}, fail("ACCESS_DENIED")
	}
	if s.Models == nil || s.Models.Identity != s.Identity || s.Models.Workspaces != s.Workspaces {
		return Ingestion{}, fail("OBJECT_INGESTION_UNAVAILABLE")
	}
	return s.ingest(ctx, a, workspace, object, "model", ingestionInput{Grant: grant, Model: &input}, key, request, input.Reason)
}
func (s *Service) IngestDataset(ctx context.Context, a identity.Auth, workspace, object, grant string, input DatasetImportRequest, key, request string) (Ingestion, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Ingestion{}, admissionError
	}
	defer finish()
	if !id(input.DatasetID) || !workspaces.ValidReason(input.Reason) {
		return Ingestion{}, fail("VALIDATION_FAILED")
	}
	if s.Datasets == nil || s.Datasets.Identity != s.Identity || s.Datasets.Workspaces != s.Workspaces {
		return Ingestion{}, fail("OBJECT_INGESTION_UNAVAILABLE")
	}
	return s.ingest(ctx, a, workspace, object, "dataset", ingestionInput{Grant: grant, Dataset: &input}, key, request, input.Reason)
}
func (s *Service) ingest(ctx context.Context, a identity.Auth, workspace, object, kind string, input ingestionInput, key, request, reason string) (Ingestion, error) {
	if !id(key) || !id(request) || input.Grant != "" && !id(input.Grant) {
		return Ingestion{}, fail("VALIDATION_FAILED")
	}
	if e := s.admitNewBusiness(ctx, a, workspace); e != nil {
		return Ingestion{}, e
	}
	raw, e := json.Marshal(input)
	if e != nil || len(raw) > 16384 {
		return Ingestion{}, fail("VALIDATION_FAILED")
	}
	fingerprint := sha256.Sum256(append(append([]byte(kind+":"+a.Me.User.ID+":"), raw...), []byte(object)...))
	issuer, e := s.grantIssuer(ctx, a, workspace, object, input.Grant)
	if e != nil {
		return Ingestion{}, e
	}
	var out Ingestion
	e = s.within(ctx, a, workspace, issuer, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		action := "ingest_" + kind
		if e = s.authorize(ctx, q, a, w, o, action, input.Grant); e != nil {
			return e
		}
		if !writeRole(a, w) {
			return fail("ACCESS_DENIED")
		}
		if e = liveWithin(ctx, q, o, false); e != nil {
			return e
		}
		if o.State != "ready" {
			return fail("OBJECT_NOT_READY")
		}
		if kind == "model" && o.Purpose != "model_bundle" || kind == "dataset" && o.Purpose != "dataset_source" {
			return fail("OBJECT_PURPOSE_INVALID")
		}
		previous, e := scanIngestion(q.QueryRow(ctx, statement(q, "SELECT id::text,object_id::text,kind,COALESCE(model_import_id,dataset_draft_id)::text,state,error_code,created_by::text,input,target_dataset_id::text,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND created_by=$4 AND creation_key=$5 FOR UPDATE", "SELECT id,object_id,kind,COALESCE(model_import_id,dataset_draft_id),state,error_code,created_by,`input`,target_dataset_id,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND created_by=? AND creation_key=? FOR UPDATE", `SELECT "ID","OBJECT_ID","KIND",COALESCE("MODEL_IMPORT_ID","DATASET_DRAFT_ID"),"STATE","ERROR_CODE","CREATED_BY","INPUT","TARGET_DATASET_ID","SOURCE_VERIFIED_SHA256" FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "CREATED_BY"=? AND "CREATION_KEY"=? FOR UPDATE`, w.TenantID, w.ID, o.ID, a.Me.User.ID, key)))
		if e == nil {
			var original []byte
			if e = q.QueryRow(ctx, statement(q, "SELECT fingerprint FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4", "SELECT fingerprint FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=?", `SELECT "FINGERPRINT" FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=?`, w.TenantID, w.ID, o.ID, previous.ID)).Scan(&original); e != nil {
				return e
			}
			if subtle.ConstantTimeCompare(original, fingerprint[:]) != 1 {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out = previous.Ingestion
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) && e.Error() != "RESOURCE_NOT_FOUND" {
			return e
		}
		if o.OperationID != nil {
			return fail("OBJECT_BUSY")
		}
		ingestion := uuid.NewString()
		var dataset *string
		if input.Dataset != nil {
			dataset = &input.Dataset.DatasetID
		}
		if _, e = q.Exec(ctx, statement(q, "INSERT INTO artifact_object_ingestions(id,tenant_id,workspace_id,object_id,created_by,creation_key,fingerprint,kind,target_dataset_id,state,input) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'intent',$10)", "INSERT INTO artifact_object_ingestions(id,tenant_id,workspace_id,object_id,created_by,creation_key,fingerprint,kind,target_dataset_id,state,`input`) VALUES(?,?,?,?,?,?,?,?,?,'intent',CAST(? AS JSON))", `INSERT INTO "ARTIFACT_OBJECT_INGESTIONS"("ID","TENANT_ID","WORKSPACE_ID","OBJECT_ID","CREATED_BY","CREATION_KEY","FINGERPRINT","KIND","TARGET_DATASET_ID","STATE","INPUT") VALUES(?,?,?,?,?,?,?,?,?,'intent',?)`, ingestion, w.TenantID, w.ID, o.ID, a.Me.User.ID, key, fingerprint[:], kind, dataset, jsonParameter(q, raw))); e != nil {
			return e
		}
		if e = s.operationWithin(ctx, q, a, w, o, "ingest", "ready"); e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "object.ingestion_requested", o.ID, reason, request); e != nil {
			return e
		}
		out = Ingestion{ID: ingestion, ObjectID: o.ID, Kind: kind, State: "intent"}
		return nil
	})
	if e != nil {
		return Ingestion{}, e
	}
	return out, nil
}
func scanIngestion(row transaction.Row) (ingestionRow, error) {
	var out ingestionRow
	e := row.Scan(&out.ID, &out.ObjectID, &out.Kind, &out.TargetID, &out.State, &out.ErrorCode, &out.Creator, &out.Input, &out.Dataset, &out.VerifiedDigest)
	if errors.Is(e, sql.ErrNoRows) {
		return ingestionRow{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return ingestionRow{}, e
	}
	return out, nil
}
func (s *Service) ingestionWithin(ctx context.Context, q transaction.Executor, o storedObject, a identity.Auth) (ingestionRow, error) {
	return scanIngestion(q.QueryRow(ctx, statement(q, "SELECT id::text,object_id::text,kind,COALESCE(model_import_id,dataset_draft_id)::text,state,error_code,created_by::text,input,target_dataset_id::text,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND created_by=$4 AND state IN ('intent','running') ORDER BY created_at,id LIMIT 1 FOR UPDATE", "SELECT id,object_id,kind,COALESCE(model_import_id,dataset_draft_id),state,error_code,created_by,`input`,target_dataset_id,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND created_by=? AND state IN ('intent','running') ORDER BY created_at,id LIMIT 1 FOR UPDATE", `SELECT "ID","OBJECT_ID","KIND",COALESCE("MODEL_IMPORT_ID","DATASET_DRAFT_ID"),"STATE","ERROR_CODE","CREATED_BY","INPUT","TARGET_DATASET_ID","SOURCE_VERIFIED_SHA256" FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "CREATED_BY"=? AND "STATE" IN ('intent','running') ORDER BY "CREATED_AT","ID" LIMIT 1 FOR UPDATE`, o.Tenant, o.WorkspaceID, o.ID, a.Me.User.ID)))
}
func (s *Service) sourceRecheck(ctx context.Context, a identity.Auth, o storedObject, input ingestionInput) error {
	issuer, e := s.grantIssuer(ctx, a, o.WorkspaceID, o.ID, input.Grant)
	if e != nil {
		return e
	}
	e = s.within(ctx, a, o.WorkspaceID, issuer, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, o.ID)
		if e != nil {
			return e
		}
		if current.OperationID == nil || o.OperationID == nil || *current.OperationID != *o.OperationID || current.State != "ready" {
			return fail("OBJECT_OPERATION_STALE")
		}
		if e = liveWithin(ctx, q, current, false); e != nil {
			return e
		}
		kind := "model"
		if input.Dataset != nil {
			kind = "dataset"
		}
		return s.authorize(ctx, q, a, w, current, "ingest_"+kind, input.Grant)
	})
	if e != nil {
		return e
	}
	return s.verifyLicenseIntegrity(ctx)
}
func (s *Service) processIngestion(ctx context.Context, a identity.Auth, o storedObject) error {
	var row ingestionRow
	e := s.within(ctx, a, o.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, o.ID)
		if e != nil {
			return e
		}
		if current.OperationID == nil || *current.OperationID != *o.OperationID {
			return fail("OBJECT_OPERATION_STALE")
		}
		row, e = s.ingestionWithin(ctx, q, current, a)
		if e != nil {
			return e
		}
		_, e = q.Exec(ctx, statement(q, "UPDATE artifact_object_ingestions SET state='running',updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4", "UPDATE artifact_object_ingestions SET state='running',updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=?", `UPDATE "ARTIFACT_OBJECT_INGESTIONS" SET "STATE"='running',"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=?`, o.Tenant, o.WorkspaceID, o.ID, row.ID))
		return e
	})
	if e != nil {
		return e
	}
	var input ingestionInput
	if json.Unmarshal(row.Input, &input) != nil || row.Kind == "model" && input.Model == nil || row.Kind == "dataset" && input.Dataset == nil {
		return fail("OBJECT_INGESTION_INVALID")
	}
	if receipt, accepted, e := s.acceptedTargetReceipt(ctx, a, o, row, input); e != nil {
		return e
	} else if accepted {
		if row.VerifiedDigest == nil {
			if row.Kind != "dataset" || row.TargetID == nil {
				return fail("OBJECT_TARGET_SOURCE_UNPROVEN")
			}
			if e = s.recordSourceVerification(ctx, a, o, row, *row.TargetID); e != nil {
				return e
			}
		}
		return s.submitReceipt(ctx, a, o, row, receipt)
	}

	if e = s.sourceRecheck(ctx, a, o, input); e != nil {
		return e
	}
	staged, _, e := s.verifiedSource(ctx, a, o.WorkspaceID, o.ID, input.Grant, "ingest_"+row.Kind)
	if e != nil {
		return e
	}
	defer s.Stage.Discard(staged)
	var receipt any
	if row.Kind == "model" {
		if s.Models == nil || s.Models.Identity != s.Identity || s.Models.Workspaces != s.Workspaces || !admin(a) {
			return fail("OBJECT_INGESTION_UNAVAILABLE")
		}
		var target models.Import
		if row.TargetID != nil {
			target, e = s.Models.GetImport(ctx, a, *row.TargetID)
		} else {
			target, e = s.Models.StartImport(ctx, a, models.ImportRequest{WorkspaceID: o.WorkspaceID, Name: input.Model.Name, CatalogDigest: input.Model.CatalogDigest, ExpectedBytes: o.ExpectedBytes, LicenseAcknowledged: input.Model.LicenseAcknowledged, Reason: input.Model.Reason}, row.ID, uuid.NewString())
		}
		if e != nil {
			return safe(e)
		}
		if target.WorkspaceID != o.WorkspaceID || target.ExpectedBytes != o.ExpectedBytes || target.CatalogDigest != input.Model.CatalogDigest {
			return fail("OBJECT_TARGET_CHANGED")
		}
		if e = s.recordTarget(ctx, a, o, row, target.ID); e != nil {
			return e
		}
		if target.State == "uploading" {
			for offset := int64(0); offset < o.ExpectedBytes; {
				if e = s.sourceRecheck(ctx, a, o, input); e != nil {
					return e
				}
				length := models.ChunkBytes
				if offset+length > o.ExpectedBytes {
					length = o.ExpectedBytes - offset
				}
				hash := sha256.New()
				if _, e = io.Copy(hash, io.NewSectionReader(staged.File, offset, length)); e != nil {
					return fail("OBJECT_STAGING_FAILED")
				}
				target, e = s.Models.UploadChunk(ctx, a, target.ID, offset, length, hex.EncodeToString(hash.Sum(nil)), io.NewSectionReader(staged.File, offset, length))
				if e != nil {
					return safe(e)
				}
				if target.UploadedBytes <= offset {
					return fail("OBJECT_TARGET_CHANGED")
				}
				offset += length
			}
			if e = s.recordSourceVerification(ctx, a, o, row, target.ID); e != nil {
				return e
			}
			row.VerifiedDigest = &o.SHA256

			if e = s.sourceRecheck(ctx, a, o, input); e != nil {
				return e
			}
			target, e = s.Models.QueueImport(ctx, a, target.ID, input.Model.Reason, uuid.NewString())
			if e != nil {
				return safe(e)
			}
		}
		if row.VerifiedDigest == nil || *row.VerifiedDigest != o.SHA256 {
			return fail("OBJECT_TARGET_SOURCE_UNPROVEN")
		}
		if target.State != "queued" && target.State != "verifying" && target.State != "ready" {
			return fail("OBJECT_TARGET_NOT_ACCEPTED")
		}
		receipt = struct {
			Target, State, SourceSHA256, CatalogDigest string
			Bytes                                      int64
		}{target.ID, target.State, o.SHA256, target.CatalogDigest, o.ExpectedBytes}
	} else {
		if s.Datasets == nil || s.Datasets.Identity != s.Identity || s.Datasets.Workspaces != s.Workspaces || row.Dataset == nil {
			return fail("OBJECT_INGESTION_UNAVAILABLE")
		}
		var target datasets.Draft
		if row.TargetID != nil {
			target, e = s.Datasets.GetDraft(ctx, a, o.WorkspaceID, *row.Dataset, *row.TargetID)
		} else {
			target, e = s.Datasets.CreateDraft(ctx, a, o.WorkspaceID, *row.Dataset, datasets.DraftRequest{SourceName: input.Dataset.SourceName, ExpectedBytes: o.ExpectedBytes, SourceSHA256: o.SHA256, Reason: input.Dataset.Reason}, row.ID, uuid.NewString())
		}
		if e != nil {
			return safe(e)
		}
		if target.WorkspaceID != o.WorkspaceID || target.DatasetID != *row.Dataset || target.ExpectedBytes != o.ExpectedBytes || target.SourceSHA256 != o.SHA256 {
			return fail("OBJECT_TARGET_CHANGED")
		}
		if e = s.recordTarget(ctx, a, o, row, target.ID); e != nil {
			return e
		}
		if target.SourceReceivedAt == nil {
			if e = s.sourceRecheck(ctx, a, o, input); e != nil {
				return e
			}
			target, e = s.Datasets.Upload(ctx, a, o.WorkspaceID, *row.Dataset, target.ID, o.SHA256, o.ExpectedBytes, staged.File, uuid.NewString())
			if e != nil {
				return safe(e)
			}
		}
		if target.SourceReceivedAt == nil {
			return fail("OBJECT_TARGET_NOT_ACCEPTED")
		}
		if e = s.recordSourceVerification(ctx, a, o, row, target.ID); e != nil {
			return e
		}
		receipt = struct {
			Target, Dataset, State, SourceSHA256 string
			Bytes                                int64
		}{target.ID, target.DatasetID, target.State, o.SHA256, o.ExpectedBytes}
	}
	return s.submitReceipt(ctx, a, o, row, receipt)
}
func (s *Service) submitReceipt(ctx context.Context, a identity.Auth, o storedObject, row ingestionRow, receipt any) error {
	raw, e := json.Marshal(receipt)
	if e != nil || len(raw) > 16384 {
		return fail("OBJECT_RECEIPT_INVALID")
	}
	return s.within(ctx, a, o.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, o.ID)
		if e != nil {
			return e
		}
		if current.OperationID == nil || *current.OperationID != *o.OperationID {
			return fail("OBJECT_OPERATION_STALE")
		}
		var proven *string
		if e = q.QueryRow(ctx, statement(q, "SELECT source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4 FOR UPDATE", "SELECT source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=? FOR UPDATE", `SELECT "SOURCE_VERIFIED_SHA256" FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=? FOR UPDATE`, o.Tenant, o.WorkspaceID, o.ID, row.ID)).Scan(&proven); e != nil {
			return e
		}
		if proven == nil || *proven != o.SHA256 {
			return fail("OBJECT_TARGET_SOURCE_UNPROVEN")
		}
		if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_object_ingestions SET state='submitted',receipt=$5,error_code=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4", "UPDATE artifact_object_ingestions SET state='submitted',receipt=CAST(? AS JSON),error_code=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=?", `UPDATE "ARTIFACT_OBJECT_INGESTIONS" SET "STATE"='submitted',"RECEIPT"=?,"ERROR_CODE"=NULL,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=?`, ingestionReceiptArgs(q, o, row.ID, raw)...)); e != nil {
			return e
		}
		if e = clearOperation(ctx, q, current, "ready", current.UploadID, current.ETag); e != nil {
			return e
		}
		return audit(ctx, q, a, w, "object.ingestion_submitted", o.ID, "Submit verified source to existing artifact owner", uuid.NewString())
	})
}
func (s *Service) recordTarget(ctx context.Context, a identity.Auth, o storedObject, row ingestionRow, target string) error {
	return s.within(ctx, a, o.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, o.ID)
		if e != nil {
			return e
		}
		if current.OperationID == nil || *current.OperationID != *o.OperationID {
			return fail("OBJECT_OPERATION_STALE")
		}
		column := "model_import_id"
		if row.Kind == "dataset" {
			column = "dataset_draft_id"
		}
		var previous *string
		if e = q.QueryRow(ctx, statement(q, "SELECT "+column+"::text FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4 FOR UPDATE", "SELECT "+column+" FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=? FOR UPDATE", `SELECT `+damengColumn(column)+` FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=? FOR UPDATE`, o.Tenant, o.WorkspaceID, o.ID, row.ID)).Scan(&previous); e != nil {
			return e
		}
		if previous != nil {
			if *previous != target {
				return fail("OBJECT_TARGET_CHANGED")
			}
			return nil
		}
		_, e = q.Exec(ctx, statement(q, "UPDATE artifact_object_ingestions SET "+column+"=$5,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4 AND ("+column+" IS NULL OR "+column+"=$5)", "UPDATE artifact_object_ingestions SET "+column+"=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=? AND ("+column+" IS NULL OR "+column+"=?)", `UPDATE "ARTIFACT_OBJECT_INGESTIONS" SET `+damengColumn(column)+`=?,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=? AND (`+damengColumn(column)+` IS NULL OR `+damengColumn(column)+`=?)`, recordTargetArgs(q, o, row.ID, target)...))
		return e
	})
}
func recordTargetArgs(q transaction.Executor, o storedObject, id, target string) []any {
	if positionalArguments(q) {
		return []any{target, o.Tenant, o.WorkspaceID, o.ID, id, target}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, id, target}
}
func ingestionReceiptArgs(q transaction.Executor, o storedObject, id string, raw []byte) []any {
	if positionalArguments(q) {
		return []any{jsonParameter(q, raw), o.Tenant, o.WorkspaceID, o.ID, id}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, id, raw}
}

func (s *Service) recordSourceVerification(ctx context.Context, a identity.Auth, o storedObject, row ingestionRow, target string) error {
	return s.within(ctx, a, o.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, o.ID)
		if e != nil {
			return e
		}
		if current.OperationID == nil || *current.OperationID != *o.OperationID {
			return fail("OBJECT_OPERATION_STALE")
		}
		column := "model_import_id"
		if row.Kind == "dataset" {
			column = "dataset_draft_id"
		}
		n, e := q.Exec(ctx, statement(q, "UPDATE artifact_object_ingestions SET source_verified_sha256=$6,source_verified_at=COALESCE(source_verified_at,clock_timestamp()),updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4 AND "+column+"=$5", "UPDATE artifact_object_ingestions SET source_verified_sha256=?,source_verified_at=COALESCE(source_verified_at,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=? AND "+column+"=?", `UPDATE "ARTIFACT_OBJECT_INGESTIONS" SET "SOURCE_VERIFIED_SHA256"=?,"SOURCE_VERIFIED_AT"=COALESCE("SOURCE_VERIFIED_AT",SYS_EXTRACT_UTC(SYSTIMESTAMP(6))),"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=? AND `+damengColumn(column)+`=?`, verificationArgs(q, o, row.ID, target)...))
		if e != nil {
			return e
		}
		if n != 1 {
			return fail("OBJECT_TARGET_CHANGED")
		}
		return nil
	})
}
func verificationArgs(q transaction.Executor, o storedObject, id, target string) []any {
	if positionalArguments(q) {
		return []any{o.SHA256, o.Tenant, o.WorkspaceID, o.ID, id, target}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, id, target, o.SHA256}
}

func (s *Service) ingestionForResume(ctx context.Context, a identity.Auth, o storedObject) (Ingestion, error) {
	var row ingestionRow
	e := s.within(ctx, a, o.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		var e error
		current, e := s.objectWithin(ctx, q, w, o.ID)
		if e != nil {
			return e
		}
		row, e = s.ingestionWithin(ctx, q, current, a)
		if e != nil {
			return e
		}
		var input ingestionInput
		if json.Unmarshal(row.Input, &input) != nil {
			return fail("OBJECT_INGESTION_INVALID")
		}
		return nil
	})
	if e != nil {
		return Ingestion{}, e
	}
	return row.Ingestion, nil
}
func (s *Service) GetIngestion(ctx context.Context, a identity.Auth, workspace, object, ingestion string) (Ingestion, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Ingestion{}, admissionError
	}
	defer finish()
	if !id(ingestion) {
		return Ingestion{}, fail("RESOURCE_NOT_FOUND")
	}
	var out Ingestion
	e := s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		row, e := scanIngestion(q.QueryRow(ctx, statement(q, "SELECT id::text,object_id::text,kind,COALESCE(model_import_id,dataset_draft_id)::text,state,error_code,created_by::text,input,target_dataset_id::text,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4", "SELECT id,object_id,kind,COALESCE(model_import_id,dataset_draft_id),state,error_code,created_by,`input`,target_dataset_id,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=?", `SELECT "ID","OBJECT_ID","KIND",COALESCE("MODEL_IMPORT_ID","DATASET_DRAFT_ID"),"STATE","ERROR_CODE","CREATED_BY","INPUT","TARGET_DATASET_ID","SOURCE_VERIFIED_SHA256" FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=?`, w.TenantID, w.ID, o.ID, ingestion)))
		if e != nil {
			return e
		}
		if row.Creator != a.Me.User.ID && !admin(a) && !(w.Role != nil && *w.Role == "owner") {
			return fail("RESOURCE_NOT_FOUND")
		}
		out = row.Ingestion
		if out.ErrorCode == nil && o.OperationKind != nil && *o.OperationKind == "ingest" {
			out.ErrorCode = o.ErrorCode
		}
		return nil
	})
	if e != nil {
		return Ingestion{}, e
	}
	return out, nil
}

func (s *Service) acceptedTargetReceipt(ctx context.Context, a identity.Auth, o storedObject, row ingestionRow, input ingestionInput) (any, bool, error) {
	if row.TargetID == nil {
		return nil, false, nil
	}
	if row.Kind == "model" {
		if s.Models == nil || s.Models.Identity != s.Identity || !admin(a) || input.Model == nil {
			return nil, false, fail("OBJECT_INGESTION_UNAVAILABLE")
		}
		target, e := s.Models.GetImport(ctx, a, *row.TargetID)
		if e != nil {
			return nil, false, safe(e)
		}
		if target.WorkspaceID != o.WorkspaceID || target.ExpectedBytes != o.ExpectedBytes || target.CatalogDigest != input.Model.CatalogDigest {
			return nil, false, fail("OBJECT_TARGET_CHANGED")
		}
		if target.State != "queued" && target.State != "verifying" && target.State != "ready" {
			return nil, false, nil
		}
		if row.VerifiedDigest == nil || *row.VerifiedDigest != o.SHA256 {
			return nil, false, fail("OBJECT_TARGET_SOURCE_UNPROVEN")
		}
		return struct {
			Target, State, SourceSHA256, CatalogDigest string
			Bytes                                      int64
		}{target.ID, target.State, o.SHA256, target.CatalogDigest, o.ExpectedBytes}, true, nil
	}
	if s.Datasets == nil || s.Datasets.Identity != s.Identity || row.Dataset == nil {
		return nil, false, fail("OBJECT_INGESTION_UNAVAILABLE")
	}
	target, e := s.Datasets.GetDraft(ctx, a, o.WorkspaceID, *row.Dataset, *row.TargetID)
	if e != nil {
		return nil, false, safe(e)
	}
	if target.WorkspaceID != o.WorkspaceID || target.DatasetID != *row.Dataset || target.ExpectedBytes != o.ExpectedBytes || target.SourceSHA256 != o.SHA256 {
		return nil, false, fail("OBJECT_TARGET_CHANGED")
	}
	if target.SourceReceivedAt == nil {
		return nil, false, nil
	}
	return struct {
		Target, Dataset, State, SourceSHA256 string
		Bytes                                int64
	}{target.ID, target.DatasetID, target.State, o.SHA256, o.ExpectedBytes}, true, nil
}

func (s *Service) ListIngestions(ctx context.Context, a identity.Auth, workspace, object, after string, limit int) ([]Ingestion, error) {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	if after != "" && !id(after) || limit < 1 || limit > MaximumObjectPageSize {
		return nil, fail("VALIDATION_FAILED")
	}
	out := []Ingestion{}
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		owner := admin(a) || w.Role != nil && *w.Role == "owner"
		rows, e := q.Query(ctx, statement(q, "SELECT id::text,object_id::text,kind,COALESCE(model_import_id,dataset_draft_id)::text,state,error_code,created_by::text,input,target_dataset_id::text,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND ($4::boolean OR created_by=$5) AND ($6::text='' OR id>NULLIF($6::text,'')::uuid) ORDER BY id LIMIT $7", "SELECT id,object_id,kind,COALESCE(model_import_id,dataset_draft_id),state,error_code,created_by,`input`,target_dataset_id,source_verified_sha256 FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND (? OR created_by=?) AND (?='' OR id>?) ORDER BY id LIMIT ?", `SELECT "ID","OBJECT_ID","KIND",COALESCE("MODEL_IMPORT_ID","DATASET_DRAFT_ID"),"STATE","ERROR_CODE","CREATED_BY","INPUT","TARGET_DATASET_ID","SOURCE_VERIFIED_SHA256" FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND (?=1 OR "CREATED_BY"=?) AND (? IS NULL OR "ID">?) ORDER BY "ID" LIMIT `+strconv.Itoa(limit), ingestionListArgs(q, w, o.ID, owner, a.Me.User.ID, after, limit)...))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			row, e := scanIngestion(rows)
			if e != nil {
				return e
			}
			out = append(out, row.Ingestion)
		}
		return rows.Err()
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func ingestionListArgs(q transaction.Executor, w workspaces.Workspace, object string, owner bool, user, after string, limit int) []any {
	if q.Dialect() == transaction.Dameng {
		return []any{w.TenantID, w.ID, object, owner, user, damengCursor(after), damengCursor(after)}
	}
	if positionalArguments(q) {
		return []any{w.TenantID, w.ID, object, owner, user, after, after, limit}
	}
	return []any{w.TenantID, w.ID, object, owner, user, after, limit}
}
