package datasets

// Explicit native DM statements for the upload, processor, publication and
// recovery path. These are owned SQL, not runtime PostgreSQL/MySQL rewrites.
// JSON parameters are bound as text at their call sites; fingerprints stay binary.
// Unquoted identifiers resolve to the uppercase native schema catalog names.
const dmInspectionExistingRows = `SELECT count(*) FROM dataset_rows WHERE draft_id=?`

const dmInspectionAccept = `UPDATE dataset_drafts SET state=?,total_rows=?,invalid_rows=?,validation_completed_at=CURRENT_TIMESTAMP(6),fatal_source_error=?,validation_errors=?,errors_truncated=?,error_code=?,error_line_number=?,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmPublicationVersionCount = `SELECT count(*) FROM dataset_versions WHERE dataset_id=?`

const dmPublicationNextOrdinal = `SELECT next_version FROM datasets WHERE id=? FOR UPDATE`

const dmPublicationAdvanceOrdinal = `UPDATE datasets SET next_version=next_version+1,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmPublicationInsertVersion = `INSERT INTO dataset_versions(id,tenant_id,workspace_id,dataset_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

const dmPublicationCompleteDraft = `UPDATE dataset_drafts SET state='published',published_version_id=?,error_code=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmAdminResolveWorkspace = `SELECT d.workspace_id FROM datasets d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND d.id=?`

const dmDraftLock = `SELECT error_line_number,id,dataset_id,workspace_id,created_by,parent_version_id,source_name,source_sha256,expected_bytes,source_received_at,state,revision,CASE WHEN validation_completed_at IS NOT NULL THEN total_rows END,CASE WHEN validation_completed_at IS NOT NULL THEN invalid_rows END,validation_completed_at,fatal_source_error,validation_errors,errors_truncated,error_code,current_job_id,published_version_id,created_at,updated_at FROM dataset_drafts WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=? FOR UPDATE`

const dmDraftFindIntent = `SELECT id,creation_fingerprint FROM dataset_drafts WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND created_by=? AND creation_key=?`

const dmDraftActiveCount = `SELECT count(*) FROM dataset_drafts WHERE tenant_id=? AND workspace_id=? AND state NOT IN ('published','cancelled')`

const dmDraftInsert = `INSERT INTO dataset_drafts(id,tenant_id,workspace_id,dataset_id,created_by,requested_audience,requested_epoch,membership_authority,source_object_id,source_name,source_sha256,expected_bytes,state,creation_key,creation_fingerprint,parent_version_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'uploading',?,?,?)`

const dmIntegrityTenant = `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1`

const dmIntegrityReferencesExist = `SELECT CASE WHEN EXISTS(SELECT 1 FROM dataset_drafts d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND d.source_received_at IS NOT NULL) OR EXISTS(SELECT 1 FROM dataset_versions v JOIN installation_state i ON i.tenant_id=v.tenant_id WHERE i.id=1) THEN 1 ELSE 0 END FROM DUAL`

const dmIntegritySources = `SELECT d.source_object_id,d.source_sha256,d.expected_bytes FROM dataset_drafts d JOIN installation_state i ON i.tenant_id=d.tenant_id WHERE i.id=1 AND d.source_received_at IS NOT NULL ORDER BY d.id`

const dmIntegrityVersions = `SELECT id,dataset_id,workspace_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason FROM dataset_versions WHERE tenant_id=(SELECT tenant_id FROM installation_state WHERE id=1) ORDER BY id`

const dmJobRequestCancel = `UPDATE dataset_jobs SET cancel_requested_at=COALESCE(cancel_requested_at,CURRENT_TIMESTAMP(6)),cancel_requested_by=COALESCE(cancel_requested_by,?),updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmCancelJobDraft = `SELECT draft_id FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=?`

const dmDraftCancel = `UPDATE dataset_drafts SET state='cancelled',upload_attempt_id=NULL,upload_expires_at=NULL,error_code='DATASET_JOB_CANCELLED',updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmRetryJobDraft = `SELECT draft_id FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=?`

const dmRetryFindIntent = `SELECT id,creation_fingerprint FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND draft_id=? AND requested_by=? AND creation_key=?`

const dmRetryInsertJob = `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,retry_of,not_before,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'pending',?,?,?,?,?,?,TIMESTAMPADD(SQL_TSI_SECOND,?,CURRENT_TIMESTAMP(6)),?,?)`

const dmRetryAttachDraft = `UPDATE dataset_drafts SET state=?,current_job_id=?,error_code=NULL,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmJobLock = `SELECT error_line_number,reason,id,dataset_id,draft_id,workspace_id,kind,requested_by,expected_revision,state,version_id,input_sha256,split_seed,validation_basis_points,attempt_count,attempt_id,lease_expires_at,cancel_requested_at,error_code,retryable,created_at,updated_at,completed_at FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=? FOR UPDATE`

const dmJobDraft = `SELECT draft_id FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=?`

const dmPublicationRows = `SELECT id,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced FROM dataset_rows WHERE draft_id=? AND deleted=0 ORDER BY line_number,id`

const dmPublicationFindIntent = `SELECT id,creation_fingerprint FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND draft_id=? AND requested_by=? AND creation_key=?`

const dmPublicationCountLimit = `SELECT count(*) FROM dataset_versions WHERE dataset_id=?`

const dmPublicationQueue = `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,'publish',?,?,?,?,?,?,'pending',?,?,?,?,?,?,?)`

const dmPublicationAttachDraft = `UPDATE dataset_drafts SET state='publishing',current_job_id=?,error_code=NULL,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmDatasetRetire = `UPDATE datasets SET retired_at=CURRENT_TIMESTAMP(6),retire_reason=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmRetireLockDrafts = `SELECT id FROM dataset_drafts WHERE dataset_id=? ORDER BY id FOR UPDATE`

const dmRetireLockJobs = `SELECT id FROM dataset_jobs WHERE dataset_id=? AND state IN ('pending','running') ORDER BY id FOR UPDATE`

const dmRetireCancelJobs = `UPDATE dataset_jobs SET cancel_requested_at=COALESCE(cancel_requested_at,CURRENT_TIMESTAMP(6)),cancel_requested_by=COALESCE(cancel_requested_by,?),completed_at=CASE WHEN state='pending' THEN CURRENT_TIMESTAMP(6) ELSE completed_at END,error_code=CASE WHEN state='pending' THEN 'DATASET_RETIRED' ELSE error_code END,state=CASE WHEN state='pending' THEN 'cancelled' ELSE state END,updated_at=CURRENT_TIMESTAMP(6) WHERE dataset_id=? AND state IN ('pending','running')`

const dmRetireCancelDrafts = `UPDATE dataset_drafts SET state=CASE WHEN state='publishing' THEN 'editing' ELSE 'cancelled' END,error_code='DATASET_RETIRED',upload_attempt_id=NULL,upload_expires_at=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE dataset_id=? AND state IN ('uploading','validating','publishing') AND NOT EXISTS(SELECT 1 FROM dataset_jobs j WHERE j.id=dataset_drafts.current_job_id AND j.state='running')`

const dmVersionRetire = `UPDATE dataset_versions SET retired_at=CURRENT_TIMESTAMP(6),retire_reason=? WHERE id=?`

const dmRestoreCancelAttempts = `UPDATE dataset_job_attempts SET state='cancelled',finished_at=CURRENT_TIMESTAMP(6),error_code='DATASET_RESTORE_INVALIDATED' WHERE state='running'`

const dmRestoreCancelJobs = `UPDATE dataset_jobs SET state='cancelled',lease_expires_at=NULL,completed_at=CURRENT_TIMESTAMP(6),error_code='DATASET_RESTORE_INVALIDATED',retryable=0,updated_at=CURRENT_TIMESTAMP(6) WHERE state IN ('pending','running')`

const dmRestoreCancelDrafts = `UPDATE dataset_drafts SET state=CASE WHEN state='publishing' THEN 'editing' ELSE 'cancelled' END,upload_attempt_id=NULL,upload_expires_at=NULL,error_code='DATASET_RESTORE_INVALIDATED',updated_at=CURRENT_TIMESTAMP(6) WHERE state IN ('uploading','validating','publishing')`

const dmRowNextLine = `SELECT COALESCE(max(line_number),0)+1 FROM dataset_rows WHERE draft_id=?`

const dmRowInsert = `INSERT INTO dataset_rows(id,tenant_id,workspace_id,dataset_id,draft_id,line_number,source_id,prompt,completion) VALUES(?,?,?,?,?,?,?,?,?)`

const dmRowLock = `SELECT id,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced FROM dataset_rows WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND draft_id=? AND id=? AND deleted=0 FOR UPDATE`

const dmRowDelete = `UPDATE dataset_rows SET deleted=1,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmRowReplace = `UPDATE dataset_rows SET source_id=?,prompt=?,completion=?,error_code=NULL,raw_preview='',preview_replaced=0,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmRowRead = `SELECT id,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced FROM dataset_rows WHERE id=?`

const dmRowHistoryInsert = `INSERT INTO dataset_row_history(tenant_id,workspace_id,dataset_id,draft_id,row_id,revision,actor_user_id,action,content_sha256,reason) VALUES(?,?,?,?,?,?,?,?,?,?)`

const dmDraftAdvanceRevision = `UPDATE dataset_drafts SET revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmDraftRowCounts = `SELECT count(*),COALESCE(SUM(CASE WHEN error_code IS NOT NULL THEN 1 ELSE 0 END),0) FROM dataset_rows WHERE draft_id=? AND deleted=0`

const dmDraftErrorsPage = `SELECT line_number,error_code,raw_preview,preview_replaced FROM dataset_rows WHERE draft_id=? AND deleted=0 AND error_code IS NOT NULL ORDER BY line_number LIMIT `

const dmDraftRefreshCounts = `UPDATE dataset_drafts SET total_rows=?,invalid_rows=?,validation_errors=?,errors_truncated=?,error_code=CASE WHEN ?>0 THEN 'DATASET_VALIDATION_FAILED' END,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmAuditInsert = `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,CURRENT_TIMESTAMP(6),?,?,?,?,?,'success',?,?,?,?)`

const dmDelegatedAuthority = `SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?`

const dmDatasetLock = `SELECT id,workspace_id,name,origin,license,rights_asserted,created_by,revision,created_at,updated_at,retired_at,retire_reason FROM datasets WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`

const dmDatasetFindIntent = `SELECT id,creation_fingerprint FROM datasets WHERE tenant_id=? AND workspace_id=? AND created_by=? AND creation_key=?`

const dmDatasetActiveCount = `SELECT count(*) FROM datasets WHERE tenant_id=? AND workspace_id=? AND retired_at IS NULL`

const dmDatasetInsert = `INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,?,?,1,?,?,?)`

const dmSnapshotImportTenant = `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1`

const dmSnapshotImportAudit = `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,reason,request_id) VALUES(?,CURRENT_TIMESTAMP(6),?,'operator',?,'success','dataset_store',?,?)`

const dmSnapshotTenant = `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1`

const dmSnapshotSources = `SELECT source_object_id,source_sha256,expected_bytes FROM dataset_drafts WHERE tenant_id=? AND source_received_at IS NOT NULL ORDER BY source_object_id`

const dmSnapshotVersions = `SELECT id,dataset_id,workspace_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason FROM dataset_versions WHERE tenant_id=? ORDER BY id`

const dmUploadObject = `SELECT source_object_id FROM dataset_drafts WHERE id=?`

const dmUploadBusy = `SELECT CASE WHEN upload_expires_at>CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END FROM dataset_drafts WHERE id=? AND upload_attempt_id IS NOT NULL`

const dmUploadClaim = `UPDATE dataset_drafts SET state='uploading',upload_attempt_id=?,upload_expires_at=TIMESTAMPADD(SQL_TSI_SECOND,?,CURRENT_TIMESTAMP(6)),error_code=NULL,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmUploadFailure = `UPDATE dataset_drafts SET state='failed',upload_attempt_id=NULL,upload_expires_at=NULL,error_code=?,updated_at=CURRENT_TIMESTAMP(6) WHERE id=? AND upload_attempt_id=? AND source_received_at IS NULL`

const dmUploadCurrentLease = `SELECT CASE WHEN upload_attempt_id=? AND upload_expires_at>CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END FROM dataset_drafts WHERE id=?`

const dmUploadQueueInspection = `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,input_sha256,input_policy,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,'import',?,?,?,?,'Validate uploaded source',?,'pending',?,?,?,?)`

const dmUploadComplete = `UPDATE dataset_drafts SET source_received_at=CURRENT_TIMESTAMP(6),upload_attempt_id=NULL,upload_expires_at=NULL,state='validating',current_job_id=?,error_code=NULL,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmVersionLock = `SELECT id,dataset_id,workspace_id,draft_id,job_id,attempt_id,ordinal,parent_version_id,published_by,published_at,raw_source_sha256,effective_source_sha256,manifest_sha256,manifest,row_mapping_sha256,retired_at,retire_reason FROM dataset_versions WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND id=? FOR UPDATE`

const dmDatasetHasPublishedVersion = `SELECT CASE WHEN EXISTS(SELECT 1 FROM dataset_versions WHERE tenant_id=? AND workspace_id=? AND dataset_id=?) THEN 1 ELSE 0 END FROM DUAL`

const dmWorkerEligible = `SELECT CASE WHEN (state='pending' AND not_before<=CURRENT_TIMESTAMP(6)) OR (state='running' AND lease_expires_at<=CURRENT_TIMESTAMP(6)) THEN 1 ELSE 0 END FROM dataset_jobs WHERE id=?`

const dmWorkerExpireAttempt = `UPDATE dataset_job_attempts SET state='expired',finished_at=CURRENT_TIMESTAMP(6),error_code='DATASET_ATTEMPT_EXPIRED' WHERE job_id=? AND id=? AND state='running'`

const dmWorkerInput = `SELECT input_policy,source_object_id,expected_bytes FROM dataset_jobs JOIN dataset_drafts ON dataset_drafts.id=dataset_jobs.draft_id WHERE dataset_jobs.id=?`

const dmWorkerClaim = `UPDATE dataset_jobs SET state='running',attempt_id=?,attempt_count=attempt_count+1,lease_expires_at=TIMESTAMPADD(SQL_TSI_SECOND,?,CURRENT_TIMESTAMP(6)),error_code=NULL,error_line_number=NULL,retryable=0,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmWorkerInsertAttempt = `INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state) VALUES(?,?,?,?,?,?,?,'running')`

const dmWorkerAcceptLease = `SELECT CASE WHEN lease_expires_at>CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END FROM dataset_jobs WHERE id=?`

const dmWorkerCompleteAttempt = `UPDATE dataset_job_attempts SET state='succeeded',finished_at=CURRENT_TIMESTAMP(6),memory_enforcement=? WHERE job_id=? AND id=? AND state='running'`

const dmWorkerCompleteJob = `UPDATE dataset_jobs SET state='succeeded',lease_expires_at=NULL,error_code=NULL,error_line_number=NULL,retryable=0,completed_at=CURRENT_TIMESTAMP(6),updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmWorkerWatchLease = `SELECT CASE WHEN state='running' AND attempt_id=? AND cancel_requested_at IS NULL AND lease_expires_at>CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END FROM dataset_jobs WHERE id=?`

const dmWorkerFailureLease = `SELECT CASE WHEN lease_expires_at>CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END FROM dataset_jobs WHERE id=?`

const dmWorkerJobErrorLine = `UPDATE dataset_jobs SET error_line_number=? WHERE id=?`

const dmWorkerDraftErrorLine = `UPDATE dataset_drafts SET error_line_number=? WHERE id=? AND current_job_id=?`

const dmWorkerFinishAttempt = `UPDATE dataset_job_attempts SET state=?,finished_at=CURRENT_TIMESTAMP(6),error_code=? WHERE job_id=? AND id=? AND state='running'`

const dmWorkerFinishJob = `UPDATE dataset_jobs SET state=?,lease_expires_at=NULL,error_code=?,error_line_number=NULL,retryable=?,completed_at=CURRENT_TIMESTAMP(6),updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmWorkerFinishDraft = `UPDATE dataset_drafts SET state=?,error_code=?,error_line_number=NULL,fatal_source_error=CASE WHEN fatal_source_error=1 OR ?=1 THEN 1 ELSE 0 END,updated_at=CURRENT_TIMESTAMP(6) WHERE id=? AND current_job_id=?`

const dmWorkerCandidate = `SELECT j.id,j.tenant_id,j.workspace_id,j.dataset_id,j.draft_id,j.requested_by,j.requested_audience,j.requested_epoch,j.membership_authority FROM dataset_jobs j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND ((j.state='pending' AND j.not_before<=CURRENT_TIMESTAMP(6)) OR (j.state='running' AND j.lease_expires_at<=CURRENT_TIMESTAMP(6))) ORDER BY j.created_at,j.id LIMIT 1`

const dmWorkerActorLock = `SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=? FOR UPDATE`

const dmWorkerWorkspaceLock = `SELECT w.status,CASE WHEN EXISTS(SELECT 1 FROM tenants t JOIN installation_state i ON i.tenant_id=t.id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.id=w.tenant_id AND t.status='active') THEN 1 ELSE 0 END FROM workspaces w WHERE w.tenant_id=? AND w.id=? FOR UPDATE`

const dmWorkerMembership = `SELECT role,authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?`
