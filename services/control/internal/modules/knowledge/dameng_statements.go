package knowledge

// Native DM statements and their exact argument order are owned here and at each
// call site. No runtime SQL translation or vendor fallback is permitted.

const dmKnowledgeCancelBaseAttempts = `UPDATE knowledge_attempts SET state='cancelled',lease_hash=NULL,lease_expires_at=NULL,error_code='KNOWLEDGE_CANCELLED',finished_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND state IN ('pending','running')`

const dmKnowledgeFailBaseDocuments = `UPDATE knowledge_documents SET state='failed',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND state IN ('pending','running')`

const dmKnowledgeFailBaseGeneration = `UPDATE knowledge_generations SET state='failed',error_code='KNOWLEDGE_CANCELLED',finished_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND state='building'`

const dmKnowledgeUpdateBase = `UPDATE knowledge_bases SET name=?,name_key=?,enabled=?,revision=revision+1,building_generation_id=CASE WHEN ?=1 THEN building_generation_id ELSE NULL END,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeLoadGeneration = `SELECT g.id,g.catalog_sha256,g.dimension,g.chunk_tokens,g.overlap_tokens,g.state,g.expected_documents,(SELECT count(DISTINCT a.document_id) FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id WHERE a.generation_id=g.id AND a.state='ready' AND d.deleted_at IS NULL AND a.source_id=d.visible_source_id),(SELECT count(DISTINCT a.document_id) FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id WHERE a.generation_id=g.id AND a.state IN ('failed','cancelled') AND d.deleted_at IS NULL AND (g.state<>'active' OR a.id=d.current_attempt_id)),g.error_code,g.created_at,g.activated_at,g.finished_at FROM knowledge_generations g WHERE g.tenant_id=? AND g.workspace_id=? AND g.base_id=? AND g.id=?`

const dmKnowledgeCountBaseDocuments = `SELECT count(*),COALESCE(SUM(CASE WHEN visible_source_id IS NOT NULL THEN 1 ELSE 0 END),0) FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL`

const dmKnowledgeLockBase = `SELECT id,workspace_id,name,enabled,revision,active_generation_id,building_generation_id,created_by,created_at,updated_at FROM knowledge_bases WHERE initialization_state='ready' AND tenant_id=? AND workspace_id=? AND id=? FOR UPDATE`

const dmKnowledgeResolveBaseWorkspace = `SELECT b.workspace_id FROM knowledge_bases b JOIN installation_state i ON i.tenant_id=b.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND b.initialization_state='ready' AND b.id=?`

const dmKnowledgeCountBases = `SELECT count(*) FROM knowledge_bases WHERE initialization_state='ready' AND tenant_id=? AND workspace_id=?`

const dmKnowledgeInsertBase = `INSERT INTO knowledge_bases(id,tenant_id,workspace_id,name,name_key,active_generation_id,created_by,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,NULL,?,?,?)`

const dmKnowledgeInsertActiveGeneration = `INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,membership_authority,expected_documents,activated_at) VALUES(?,?,?,?,?,384,?,?,'active',?,?,?,?,0,CURRENT_TIMESTAMP(6))`

const dmKnowledgeLockDocument = `SELECT id,base_id,workspace_id,name,revision,state,visible_source_id,pending_source_id,current_attempt_id,created_by,created_at,updated_at,deleted_at FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND id=? FOR UPDATE`

const dmKnowledgeLoadSourceMetadata = `SELECT id,source_version,sha256,size_bytes,created_at FROM knowledge_source_versions WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND id=?`

const dmKnowledgeLoadAttempt = `SELECT id,kind,state,attempt_number,chunk_count,error_code,requested_by,created_at,started_at,finished_at FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND id=?`

const dmKnowledgeCountSourceAttempts = `SELECT coalesce(max(attempt_number),0) FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND source_id=? AND generation_id=?`

const dmKnowledgeInsertAttempt = `INSERT INTO knowledge_attempts(id,tenant_id,workspace_id,base_id,document_id,source_id,generation_id,kind,attempt_number,expected_document_revision,requested_by,requested_audience,requested_epoch,membership_authority,state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'pending')`

const dmKnowledgeCountDocuments = `SELECT count(*) FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL`

const dmKnowledgeInsertDocument = `INSERT INTO knowledge_documents(id,tenant_id,workspace_id,base_id,name,name_key,state,pending_source_id,created_by) VALUES(?,?,?,?,?,?,'pending',NULL,?)`

const dmKnowledgeInsertFirstSource = `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by,size_bytes) VALUES(?,?,?,?,?,1,?,?,?,?)`

const dmKnowledgeAttachDocumentAttempt = `UPDATE knowledge_documents SET current_attempt_id=?,pending_source_id=? WHERE id=?`

const dmKnowledgeRetirePendingSource = `UPDATE knowledge_source_versions SET superseded_at=CURRENT_TIMESTAMP(6) WHERE id=? AND superseded_at IS NULL`

const dmKnowledgeNextSourceVersion = `SELECT coalesce(max(source_version),0)+1 FROM knowledge_source_versions WHERE document_id=?`

const dmKnowledgeInsertSource = `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by,size_bytes) VALUES(?,?,?,?,?,?,?,?,?,?)`

const dmKnowledgeReplaceDocumentSource = `UPDATE knowledge_documents SET pending_source_id=?,current_attempt_id=?,state='pending',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeRetryDocument = `UPDATE knowledge_documents SET current_attempt_id=?,state='pending',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeCancelDocumentAttempts = `UPDATE knowledge_attempts SET state='cancelled',lease_hash=NULL,lease_expires_at=NULL,error_code='KNOWLEDGE_CANCELLED',finished_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND state IN ('pending','running')`

const dmKnowledgeRetireDocumentSources = `UPDATE knowledge_source_versions SET superseded_at=CURRENT_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND superseded_at IS NULL`

const dmKnowledgeDeleteDocument = `UPDATE knowledge_documents SET state='deleted',visible_source_id=NULL,pending_source_id=NULL,revision=revision+1,deleted_at=CURRENT_TIMESTAMP(6),delete_reason=?,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeGetSource = `SELECT id,source_version,content,sha256,size_bytes,created_at FROM knowledge_source_versions WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND id=?`

const dmKnowledgeCountGenerationAttempts = `SELECT COALESCE(SUM(CASE WHEN state IN ('pending','running') THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN state IN ('failed','cancelled') THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN state='ready' THEN 1 ELSE 0 END),0) FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND generation_id=?`

const dmKnowledgeCountVisibleDocuments = `SELECT count(*) FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL AND visible_source_id IS NOT NULL`

const dmKnowledgeCountReadySources = `SELECT count(*) FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id WHERE a.tenant_id=? AND a.workspace_id=? AND a.base_id=? AND a.generation_id=? AND a.state='ready' AND d.deleted_at IS NULL AND d.visible_source_id=a.source_id`

const dmKnowledgeCancelGenerationAttempts = `UPDATE knowledge_attempts SET state='cancelled',lease_hash=NULL,lease_expires_at=NULL,error_code=?,finished_at=CURRENT_TIMESTAMP(6) WHERE generation_id=? AND state IN ('pending','running')`

const dmKnowledgeFailGeneration = `UPDATE knowledge_generations SET state='failed',error_code=?,finished_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeClearBuildingGeneration = `UPDATE knowledge_bases SET building_generation_id=NULL,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeRetireGeneration = `UPDATE knowledge_generations SET state='retired',finished_at=CURRENT_TIMESTAMP(6) WHERE id=? AND state='active'`

const dmKnowledgeActivateGeneration = `UPDATE knowledge_generations SET state='active',activated_at=CURRENT_TIMESTAMP(6),finished_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeActivateDocuments = `UPDATE knowledge_documents d SET current_attempt_id=(SELECT a.id FROM knowledge_attempts a WHERE a.tenant_id=d.tenant_id AND a.workspace_id=d.workspace_id AND a.base_id=d.base_id AND a.document_id=d.id AND a.generation_id=? AND a.state='ready' AND a.source_id=d.visible_source_id),state='ready',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE d.tenant_id=? AND d.workspace_id=? AND d.base_id=? AND d.pending_source_id IS NULL AND d.deleted_at IS NULL AND EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.tenant_id=d.tenant_id AND a.workspace_id=d.workspace_id AND a.base_id=d.base_id AND a.document_id=d.id AND a.generation_id=? AND a.state='ready' AND a.source_id=d.visible_source_id)`

const dmKnowledgeAttachGeneration = `UPDATE knowledge_bases SET active_generation_id=?,building_generation_id=NULL,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeCountLiveAttempts = `SELECT count(*) FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND state IN ('pending','running')`

const dmKnowledgeCountGenerations = `SELECT count(*) FROM knowledge_generations WHERE tenant_id=? AND workspace_id=? AND base_id=?`

const dmKnowledgeListRebuildSources = `SELECT id,visible_source_id,revision FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at IS NULL AND visible_source_id IS NOT NULL ORDER BY id`

const dmKnowledgeInsertBuildingGeneration = `INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,membership_authority,expected_documents) VALUES(?,?,?,?,?,384,?,?,'building',?,?,?,?,?)`

const dmKnowledgeAttachBuildingGeneration = `UPDATE knowledge_bases SET building_generation_id=?,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeNextBuildingGeneration = `SELECT g.tenant_id,g.workspace_id,g.base_id,g.id,g.requested_by,g.requested_audience,g.requested_epoch,g.membership_authority FROM knowledge_generations g JOIN knowledge_bases b ON b.tenant_id=g.tenant_id AND b.workspace_id=g.workspace_id AND b.id=g.base_id AND b.initialization_state='ready' JOIN installation_state i ON i.tenant_id=g.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND g.state='building' ORDER BY g.created_at,g.id LIMIT 1`

const dmKnowledgeLockBuildingGeneration = `SELECT id FROM knowledge_generations WHERE id=? AND tenant_id=? AND workspace_id=? AND base_id=? FOR UPDATE`

const dmKnowledgeLockAnswerActor = `SELECT id FROM users WHERE id=? FOR UPDATE`

const dmKnowledgeCheckAnswerSource = `SELECT CASE WHEN EXISTS(SELECT 1 FROM knowledge_documents d JOIN knowledge_source_versions v ON v.id=d.visible_source_id AND v.document_id=d.id AND v.tenant_id=d.tenant_id AND v.workspace_id=d.workspace_id AND v.base_id=d.base_id JOIN knowledge_chunks c ON c.tenant_id=v.tenant_id AND c.workspace_id=v.workspace_id AND c.base_id=v.base_id AND c.document_id=v.document_id AND c.source_id=v.id JOIN knowledge_attempts a ON a.id=c.attempt_id AND a.tenant_id=c.tenant_id AND a.workspace_id=c.workspace_id AND a.base_id=c.base_id AND a.generation_id=c.generation_id AND a.document_id=c.document_id AND a.source_id=c.source_id WHERE d.tenant_id=? AND d.workspace_id=? AND d.base_id=? AND d.id=? AND d.deleted_at IS NULL AND v.id=? AND v.sha256=? AND v.source_version=? AND c.generation_id=? AND c.ordinal=? AND c.start_byte=? AND c.end_byte=? AND a.state='ready') THEN 1 ELSE 0 END FROM DUAL`

const dmKnowledgeLockEvidenceActor = `SELECT id FROM users WHERE id=? FOR UPDATE`

const dmKnowledgeEvidenceSource = `SELECT v.content FROM knowledge_documents d JOIN knowledge_source_versions v ON v.tenant_id=d.tenant_id AND v.workspace_id=d.workspace_id AND v.base_id=d.base_id AND v.document_id=d.id AND v.id=d.visible_source_id WHERE d.tenant_id=? AND d.workspace_id=? AND d.base_id=? AND d.id=? AND d.deleted_at IS NULL AND v.id=? AND v.source_version=? AND v.sha256=? AND ?<=v.size_bytes`

const dmKnowledgeRestoreAttempts = `UPDATE knowledge_attempts SET state='failed',lease_hash=NULL,lease_expires_at=NULL,error_code='KNOWLEDGE_WORKER_INTERRUPTED',finished_at=CURRENT_TIMESTAMP(6) WHERE state IN ('pending','running')`

const dmKnowledgeRestoreDocuments = `UPDATE knowledge_documents SET state='failed',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE deleted_at IS NULL AND pending_source_id IS NOT NULL AND state IN ('pending','running')`

const dmKnowledgeRestoreGenerations = `UPDATE knowledge_generations SET state='failed',error_code='KNOWLEDGE_WORKER_INTERRUPTED',finished_at=CURRENT_TIMESTAMP(6) WHERE state='building'`

const dmKnowledgeRestoreBases = `UPDATE knowledge_bases SET building_generation_id=NULL,revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE building_generation_id IS NOT NULL`

const dmKnowledgeRetentionCandidate = `SELECT b.tenant_id,b.workspace_id,b.id FROM knowledge_bases b JOIN installation_state i ON i.tenant_id=b.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND b.initialization_state='ready' AND(? IS NULL OR b.id>?) ORDER BY b.id LIMIT 1`

const dmKnowledgeRetentionWorkspaceLock = `SELECT id FROM workspaces WHERE tenant_id=? AND id=? FOR UPDATE`

const dmKnowledgeRetentionBaseLock = `SELECT id FROM knowledge_bases WHERE tenant_id=? AND workspace_id=? AND id=? AND initialization_state='ready' FOR UPDATE`

const dmKnowledgeRetentionDetach = `UPDATE knowledge_documents SET current_attempt_id=NULL WHERE id IN(SELECT id FROM (SELECT id FROM knowledge_documents WHERE tenant_id=? AND workspace_id=? AND base_id=? AND deleted_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6)) AND current_attempt_id IS NOT NULL ORDER BY deleted_at,id LIMIT `

const dmKnowledgeRetentionChunks = `DELETE FROM knowledge_chunks WHERE (attempt_id,ordinal) IN(SELECT attempt_id,ordinal FROM (SELECT c.attempt_id,c.ordinal FROM knowledge_chunks c JOIN knowledge_attempts a ON a.id=c.attempt_id JOIN knowledge_generations g ON g.id=c.generation_id JOIN knowledge_bases b ON b.id=c.base_id JOIN knowledge_documents d ON d.id=c.document_id JOIN knowledge_source_versions v ON v.id=c.source_id WHERE c.tenant_id=? AND c.workspace_id=? AND c.base_id=? AND a.state NOT IN ('pending','running') AND ((g.state IN ('failed','retired') AND g.finished_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6)) AND g.id<>b.active_generation_id AND (b.building_generation_id IS NULL OR g.id<>b.building_generation_id)) OR(v.superseded_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6)) AND (d.visible_source_id IS NULL OR v.id<>d.visible_source_id) AND (d.pending_source_id IS NULL OR v.id<>d.pending_source_id) AND(d.deleted_at IS NULL OR d.deleted_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6))))) ORDER BY c.attempt_id,c.ordinal LIMIT `

const dmKnowledgeRetentionAttempts = `DELETE FROM knowledge_attempts WHERE id IN(SELECT id FROM (SELECT a.id FROM knowledge_attempts a JOIN knowledge_generations g ON g.id=a.generation_id JOIN knowledge_bases b ON b.id=a.base_id JOIN knowledge_documents d ON d.id=a.document_id JOIN knowledge_source_versions v ON v.id=a.source_id WHERE a.tenant_id=? AND a.workspace_id=? AND a.base_id=? AND a.state NOT IN ('pending','running') AND (d.current_attempt_id IS NULL OR a.id<>d.current_attempt_id) AND NOT EXISTS(SELECT 1 FROM knowledge_chunks c WHERE c.attempt_id=a.id) AND ((g.state IN ('failed','retired') AND g.finished_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6)) AND g.id<>b.active_generation_id AND (b.building_generation_id IS NULL OR g.id<>b.building_generation_id)) OR(v.superseded_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6)) AND (d.visible_source_id IS NULL OR v.id<>d.visible_source_id) AND (d.pending_source_id IS NULL OR v.id<>d.pending_source_id) AND(d.deleted_at IS NULL OR d.deleted_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6))))) ORDER BY a.created_at,a.id LIMIT `

const dmKnowledgeRetentionSources = `DELETE FROM knowledge_source_versions WHERE id IN(SELECT id FROM (SELECT v.id FROM knowledge_source_versions v JOIN knowledge_documents d ON d.id=v.document_id WHERE v.tenant_id=? AND v.workspace_id=? AND v.base_id=? AND v.superseded_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6)) AND (d.visible_source_id IS NULL OR v.id<>d.visible_source_id) AND (d.pending_source_id IS NULL OR v.id<>d.pending_source_id) AND(d.deleted_at IS NULL OR d.deleted_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6))) AND NOT EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.source_id=v.id) ORDER BY v.created_at,v.id LIMIT `

const dmKnowledgeRetentionDocuments = `DELETE FROM knowledge_documents WHERE id IN(SELECT id FROM (SELECT d.id FROM knowledge_documents d WHERE d.tenant_id=? AND d.workspace_id=? AND d.base_id=? AND d.deleted_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6)) AND d.current_attempt_id IS NULL AND NOT EXISTS(SELECT 1 FROM knowledge_source_versions v WHERE v.document_id=d.id) AND NOT EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.document_id=d.id) ORDER BY d.deleted_at,d.id LIMIT `

const dmKnowledgeRetentionGenerations = `DELETE FROM knowledge_generations WHERE id IN(SELECT id FROM (SELECT g.id FROM knowledge_generations g JOIN knowledge_bases b ON b.id=g.base_id WHERE g.tenant_id=? AND g.workspace_id=? AND g.base_id=? AND g.state IN ('failed','retired') AND g.finished_at<TIMESTAMPADD(SQL_TSI_DAY,-?,CURRENT_TIMESTAMP(6)) AND g.id<>b.active_generation_id AND (b.building_generation_id IS NULL OR g.id<>b.building_generation_id) AND NOT EXISTS(SELECT 1 FROM knowledge_attempts a WHERE a.generation_id=g.id) ORDER BY g.finished_at,g.id LIMIT `

const dmKnowledgeRetentionIntents = `DELETE FROM knowledge_mutation_intents WHERE (tenant_id,workspace_id,actor_id,idempotency_key) IN(SELECT tenant_id,workspace_id,actor_id,idempotency_key FROM (SELECT tenant_id,workspace_id,actor_id,idempotency_key FROM knowledge_mutation_intents WHERE tenant_id=? AND workspace_id=? AND purpose<>'create_base' AND created_at<TIMESTAMPADD(SQL_TSI_SECOND,-?,CURRENT_TIMESTAMP(6)) ORDER BY created_at,idempotency_key LIMIT `

const dmKnowledgeRetentionAudit = `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,CURRENT_TIMESTAMP(6),NULL,?,?,'admin','knowledge.retention_completed','success','knowledge_base',?,'Bounded retired knowledge retention applied',?)`

const dmKnowledgeAudit = `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,CURRENT_TIMESTAMP(6),?,?,?,?,?,'success',?,?,?,?)`

const dmKnowledgeFindIntent = `SELECT entity_id,purpose,request_hash FROM knowledge_mutation_intents WHERE tenant_id=? AND workspace_id=? AND actor_id=? AND idempotency_key=? FOR UPDATE`

const dmKnowledgeSaveIntent = `INSERT INTO knowledge_mutation_intents(tenant_id,workspace_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES(?,?,?,?,?,?,?)`

const dmKnowledgeDelegation = `SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?`

const dmKnowledgeFailAttempt = `UPDATE knowledge_attempts SET state='failed',lease_hash=NULL,lease_expires_at=NULL,error_code=?,finished_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeFailDocument = `UPDATE knowledge_documents SET state='failed',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeClaimSource = `SELECT content,sha256 FROM knowledge_source_versions WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND id=?`

const dmKnowledgeClaimAttempt = `UPDATE knowledge_attempts SET state='running',lease_hash=?,lease_expires_at=TIMESTAMPADD(SQL_TSI_SECOND,?,CURRENT_TIMESTAMP(6)),started_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeRunDocument = `UPDATE knowledge_documents SET state='running',updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeLiveClaim = `SELECT CASE WHEN EXISTS(SELECT 1 FROM knowledge_attempts a JOIN knowledge_documents d ON d.id=a.document_id AND d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.base_id=a.base_id JOIN knowledge_bases b ON b.id=a.base_id AND b.tenant_id=a.tenant_id AND b.workspace_id=a.workspace_id JOIN knowledge_generations g ON g.id=a.generation_id AND g.base_id=a.base_id AND g.tenant_id=a.tenant_id AND g.workspace_id=a.workspace_id JOIN users u ON u.id=a.requested_by JOIN workspaces w ON w.id=a.workspace_id AND w.tenant_id=a.tenant_id JOIN tenants t ON t.id=a.tenant_id JOIN installation_state i ON i.tenant_id=a.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND a.id=? AND a.lease_hash=? AND a.state='running' AND a.lease_expires_at>CURRENT_TIMESTAMP(6) AND u.status='active' AND u.auth_epoch=a.requested_epoch AND w.status='active' AND t.status='active' AND b.initialization_state='ready' AND b.enabled=1 AND d.deleted_at IS NULL AND g.catalog_sha256=? AND ((a.requested_audience='admin' AND u.is_platform_admin=1) OR(a.requested_audience='portal' AND EXISTS(SELECT 1 FROM workspace_memberships m WHERE m.tenant_id=a.tenant_id AND m.workspace_id=a.workspace_id AND m.user_id=a.requested_by AND m.authority_id=a.membership_authority AND m.role IN ('owner','member')))) AND ((a.kind='ingest' AND b.active_generation_id=g.id AND g.state='active' AND d.pending_source_id=a.source_id AND d.current_attempt_id=a.id AND d.revision=a.expected_document_revision AND d.state='running') OR(a.kind='rebuild' AND b.building_generation_id=g.id AND g.state='building' AND d.visible_source_id=a.source_id))) THEN 1 ELSE 0 END FROM DUAL`

const dmKnowledgeCompleteAttempt = `UPDATE knowledge_attempts SET state='ready',lease_hash=NULL,lease_expires_at=NULL,chunk_count=?,finished_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeRetireVisibleSource = `UPDATE knowledge_source_versions SET superseded_at=CURRENT_TIMESTAMP(6) WHERE id=? AND superseded_at IS NULL`

const dmKnowledgeCompleteDocument = `UPDATE knowledge_documents SET visible_source_id=?,pending_source_id=NULL,state='ready',revision=revision+1,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`

const dmKnowledgeLockWorkerActor = `SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=? FOR UPDATE`

const dmKnowledgeLockWorkerWorkspace = `SELECT w.status,CASE WHEN t.status='active' THEN 1 ELSE 0 END FROM workspaces w JOIN tenants t ON t.id=w.tenant_id JOIN installation_state i ON i.tenant_id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND w.tenant_id=? AND w.id=? FOR UPDATE`

const dmKnowledgeWorkerMembership = `SELECT role,authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?`

const dmKnowledgeLockWorkerGeneration = `SELECT id FROM knowledge_generations WHERE tenant_id=? AND workspace_id=? AND base_id=? AND id=? FOR UPDATE`

const dmKnowledgeLockWorkerAttempt = `SELECT state,lease_hash,CASE WHEN lease_expires_at<=CURRENT_TIMESTAMP(6) THEN 1 ELSE 0 END FROM knowledge_attempts WHERE tenant_id=? AND workspace_id=? AND base_id=? AND document_id=? AND source_id=? AND generation_id=? AND id=? AND requested_by=? AND requested_audience=? AND requested_epoch=? AND expected_document_revision=? AND kind=? FOR UPDATE`

const dmKnowledgeNextCandidate = `SELECT a.id,a.tenant_id,a.workspace_id,a.base_id,a.document_id,a.source_id,a.generation_id,a.kind,a.expected_document_revision,a.requested_by,a.requested_audience,a.requested_epoch,a.membership_authority FROM knowledge_attempts a JOIN knowledge_bases b ON b.tenant_id=a.tenant_id AND b.workspace_id=a.workspace_id AND b.id=a.base_id AND b.initialization_state='ready' JOIN installation_state i ON i.tenant_id=a.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND (a.state='pending' OR(a.state='running' AND a.lease_expires_at<=CURRENT_TIMESTAMP(6))) ORDER BY a.created_at,a.id LIMIT 1`

const dmKnowledgeLockWorkflowActor = `SELECT id FROM users WHERE id=? FOR UPDATE`
