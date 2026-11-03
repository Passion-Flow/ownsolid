package knowledge

import (
	"context"
	"encoding/json"
	"log/slog"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"time"
	"unicode/utf8"
)

const encoderReadinessProbe = "Project-one offline encoder readiness."

func (s *Service) OpenConfiguredProcessor(ctx context.Context) error {
	if s.Identity.Config.Knowledge.Enabled {
		if e := s.verifyDatabase(ctx); e != nil {
			return e
		}
	}
	processor, e := NewProcessor(s.Identity.Config.Knowledge, s.policy())
	if e != nil {
		return e
	}
	if processor == nil {
		return nil
	}
	if _, e = processor.Execute(ctx, encoderReadinessProbe, false, s.policy().ChunkTokens, s.policy().OverlapTokens); e != nil {
		processor.Close()
		return e
	}
	s.Processor = processor
	return nil
}

// Run admits one durable local job at a time; the Processor also bounds concurrent queries.
// Callers cancel and join this loop before stopping the database or returning from serve.
func (s *Service) Run(ctx context.Context) {
	var previous string
	nextCleanup := time.Now()
	for ctx.Err() == nil {
		_, e := s.ProcessNext(ctx)
		if ctx.Err() != nil {
			return
		}
		if e == nil {
			maintenance, cancel := context.WithTimeout(ctx, time.Duration(s.policy().CommitSeconds)*time.Second)
			e = s.ReconcileGeneration(maintenance)
			cancel()
		}
		if e == nil && !time.Now().Before(nextCleanup) {
			maintenance, cancel := context.WithTimeout(ctx, time.Duration(s.policy().CommitSeconds)*time.Second)
			_, e = s.Cleanup(maintenance)
			if e == nil {
				e = s.Processor.CleanupScratch()
			}
			cancel()
			nextCleanup = time.Now().Add(time.Duration(s.policy().CleanupSeconds) * time.Second)
		}
		if e != nil {
			code := errorCode(e)
			if code != previous {
				slog.Error("knowledge_control_iteration_failed", "code", code)
				previous = code
			}
		} else {
			previous = ""
		}
		timer := time.NewTimer(time.Duration(s.policy().PollMilliseconds) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// VerifyStored is read-only and requires no extension or migration authority.
func (s *Service) VerifyStored(ctx context.Context) error {
	if !s.Identity.Config.Knowledge.Enabled {
		return nil
	}
	if !s.Processor.Available() {
		return failure("KNOWLEDGE_UNAVAILABLE")
	}
	return s.verifyDatabase(ctx)
}
func (s *Service) verifyDatabase(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().QuerySeconds))
	defer cancel()
	repo, e := s.repository()
	if e != nil {
		return e
	}
	if repo.Dialect() == transaction.PostgreSQL {
		if e = database.CheckKnowledgeVectorWithin(ctx, repo); e != nil {
			return failure("KNOWLEDGE_CONFIGURATION_INVALID")
		}
		var valid bool
		if e = repo.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_extension e JOIN pg_catalog.pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='vector' AND e.extversion='0.8.7' AND n.nspname='public') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace n CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) a WHERE n.nspname='public' AND a.grantee=0 AND a.privilege_type='CREATE') AND EXISTS(SELECT 1 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace JOIN pg_catalog.pg_depend d ON d.classid='pg_catalog.pg_type'::regclass AND d.objid=t.oid AND d.deptype='e' JOIN pg_catalog.pg_extension e ON e.oid=d.refobjid WHERE a.attrelid='knowledge_chunks'::regclass AND a.attname='embedding' AND a.atttypmod=384 AND NOT a.attisdropped AND n.nspname='public' AND t.typname='vector' AND e.extname='vector')`)).Scan(&valid); e != nil || !valid {
			return failure("KNOWLEDGE_CONFIGURATION_INVALID")
		}
		return nil
	}
	var valid bool
	if e = repo.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='knowledge_chunks' AND column_name='embedding' AND data_type='json') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='knowledge_bases' AND column_name='initialization_state') AND NOT EXISTS(SELECT 1 FROM knowledge_bases b JOIN installation_state i ON i.tenant_id=b.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND (b.initialization_state<>'ready' OR b.active_generation_id IS NULL))`)).Scan(&valid); e != nil || !valid {
		return failure("KNOWLEDGE_CONFIGURATION_INVALID")
	}
	// Stream historical immutable bytes/vectors, including retired generations;
	// readiness never adopts or rewrites incomplete graph or corrupt retained data.
	rows, e := repo.Query(ctx, transaction.SQL(transaction.MySQL, `SELECT v.content,v.sha256,v.size_bytes FROM knowledge_source_versions v JOIN installation_state i ON i.tenant_id=v.tenant_id WHERE i.id=1 AND i.bootstrap_complete ORDER BY v.id`))
	if e != nil {
		return knowledgeError(e)
	}
	for rows.Next() {
		if e = ctx.Err(); e != nil {
			rows.Close()
			return knowledgeError(e)
		}
		var content []byte
		var digest string
		var size int
		if e = rows.Scan(&content, &digest, &size); e != nil {
			rows.Close()
			return knowledgeError(e)
		}
		if size < 1 || size > config.MaximumKnowledgeSourceBytes || size != len(content) || !utf8.Valid(content) || hashBytes(content) != digest {
			rows.Close()
			return failure("KNOWLEDGE_INVALID_SOURCE")
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return knowledgeError(e)
	}
	var contract workerContract
	if json.Unmarshal([]byte(WorkerContractJSON), &contract) != nil || contract.Dimension != EmbeddingDimension {
		return failure("KNOWLEDGE_CONFIGURATION_INVALID")
	}
	rows, e = repo.Query(ctx, transaction.SQL(transaction.MySQL, `SELECT c.embedding FROM knowledge_chunks c JOIN installation_state i ON i.tenant_id=c.tenant_id WHERE i.id=1 AND i.bootstrap_complete ORDER BY c.attempt_id,c.ordinal`))
	if e != nil {
		return knowledgeError(e)
	}
	defer rows.Close()
	for rows.Next() {
		if e = ctx.Err(); e != nil {
			return knowledgeError(e)
		}
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return knowledgeError(e)
		}
		if _, _, e = decodeVector(raw, contract.VectorNormTolerance); e != nil {
			return e
		}
	}
	return knowledgeError(rows.Err())
}
