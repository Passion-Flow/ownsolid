package artifactstores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

type lifecycle struct {
	mu         sync.Mutex
	stopped    bool
	active     sync.WaitGroup
	calls      int
	backup     *BackupFence
	ctx        context.Context
	cancel     context.CancelFunc
	digest     string
	identity   *identity.Service
	workspaces *workspaces.Service
	models     *models.Service
	datasets   *datasets.Service
	transport  *objectstore.S3
	stage      *objectstore.Stage
	license    *licensing.Service
}

func configurationDigest(c config.ObjectStorageConfiguration) string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// NewService borrows configured database/artifact owners and takes exclusive
// ownership of the supplied transport/staging handles only after admission.
// Failure leaves those handles with the caller for explicit cleanup.
func NewService(ctx context.Context, i *identity.Service, w *workspaces.Service, m *models.Service, d *datasets.Service, c config.ObjectStorageConfiguration, t *objectstore.S3, stage *objectstore.Stage) (*Service, error) {
	if ctx == nil || i == nil || w == nil || w.Identity != i || m == nil || m.Identity != i || d == nil || d.Identity != i || m.Workspaces != w || d.Workspaces != w || m.RootDirectory == "" || d.Store == nil || d.Processor == nil || t == nil || stage == nil || c.Validate() != nil {
		return nil, fail("OBJECT_COMPOSITION_INVALID")
	}
	license, e := configuredLicenseOwner(i)
	if e != nil {
		return nil, e
	}
	if e = license.VerifyStored(ctx); e != nil {
		return nil, safe(e)
	}
	// Copy slices so caller-owned configuration cannot silently change admission.
	c.DialAddresses = append([]string(nil), c.DialAddresses...)
	c.Management = append([]config.ObjectStorageManagementProbe(nil), c.Management...)
	for n := range c.Management {
		c.Management[n].DialAddresses = append([]string(nil), c.Management[n].DialAddresses...)
	}
	s := &Service{Identity: i, Workspaces: w, Models: m, Datasets: d, Configuration: c, Transport: t, Stage: stage}
	if !t.MatchesConfiguration(c) || stage.Identity() != t.Identity() || stage.Identity() != objectstore.ConfigurationIdentity(c) {
		return nil, fail("OBJECT_STORE_CHANGED")
	}
	if e := s.verifyStored(ctx); e != nil {
		return nil, e
	}
	if e := m.VerifyStoredVersions(ctx); e != nil {
		return nil, safe(e)
	}
	if e := d.VerifyStored(ctx); e != nil {
		return nil, safe(e)
	}
	if e := t.VerifyProfile(ctx); e != nil {
		return nil, safe(e)
	}
	run, cancel := context.WithCancel(context.Background())
	s.lifecycle = &lifecycle{ctx: run, cancel: cancel, digest: configurationDigest(c), identity: i, workspaces: w, models: m, datasets: d, transport: t, stage: stage, license: license}
	return s, nil
}
func (s *Service) begin(ctx context.Context) (context.Context, func(), error) {
	return s.enter(ctx, true)
}

// enter joins shutdown. Only individual operations count toward the backup
// drain. The idle worker supervisor must stay alive across a fence.
func (s *Service) enter(ctx context.Context, operation bool) (context.Context, func(), error) {
	if s == nil || s.lifecycle == nil || ctx == nil {
		return nil, nil, fail("OBJECT_NOT_ADMITTED")
	}
	l := s.lifecycle
	l.mu.Lock()
	if !s.admittedLocked() {
		l.mu.Unlock()
		return nil, nil, fail("OBJECT_NOT_ADMITTED")
	}
	if operation && l.backup != nil {
		l.mu.Unlock()
		return nil, nil, errBackupFenced
	}
	if operation {
		l.calls++
	}
	l.active.Add(1)
	l.mu.Unlock()

	run, finish := l.operationContext(ctx)
	return run, func() {
		if operation {
			l.mu.Lock()
			l.calls--
			if l.calls == 0 && l.backup != nil {
				close(l.backup.drained)
			}
			l.mu.Unlock()
		}
		finish()
	}, nil
}

func (s *Service) admittedLocked() bool {
	l := s.lifecycle
	license, err := configuredLicenseOwner(s.Identity)
	return !l.stopped && err == nil && license == l.license && l.digest == configurationDigest(s.Configuration) &&
		s.Identity == l.identity && s.Workspaces == l.workspaces && s.Models == l.models &&
		s.Datasets == l.datasets && s.Transport == l.transport && s.Stage == l.stage
}

// operationContext owns an already registered active operation.
func (l *lifecycle) operationContext(ctx context.Context) (context.Context, func()) {
	run, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.ctx, cancel)
	return run, func() { stop(); cancel(); l.active.Done() }
}

// Close fences new calls, cancels accepted calls, and waits before releasing the
// process lock. A timeout retains the handles and permits an explicit retry.
func (s *Service) Close(ctx context.Context) error {
	if s == nil || s.lifecycle == nil || ctx == nil {
		return fail("OBJECT_NOT_ADMITTED")
	}
	l := s.lifecycle
	l.mu.Lock()
	l.stopped = true
	l.cancel()
	l.mu.Unlock()
	done := make(chan struct{})
	go func() { l.active.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return safe(ctx.Err())
	case <-done:
	}
	if e := l.transport.Close(); e != nil {
		return safe(e)
	}
	return safe(l.stage.Close())
}

// Run processes only durable prepared work. Unknown sent effects are never
// retried here; explicit Reconcile inspects their exact remote identity.
func (s *Service) Run(ctx context.Context, report func(error)) error {
	ctx, done, e := s.enter(ctx, false)
	if e != nil {
		return e
	}
	defer done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return safe(ctx.Err())
		case <-timer.C:
		}
		_, e = s.ProcessNext(ctx)
		if e != errBackupFenced && workerReportable(e, ctx.Err() != nil) && report != nil {
			report(safe(e))
		}
		if ctx.Err() != nil {
			if e != errBackupFenced && workerReportable(e, true) {
				return safe(e)
			}
			return safe(ctx.Err())
		}
		timer.Reset(time.Duration(s.Configuration.WorkerPollSeconds) * time.Second)
	}
}
func (s *Service) VerifyStored(ctx context.Context) error {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return e
	}
	defer done()
	if e = s.verifyLicenseIntegrity(ctx); e != nil {
		return e
	}
	if e = s.verifyStored(ctx); e != nil {
		return e
	}
	return safe(s.Transport.VerifyProfile(ctx))
}
func (s *Service) verifyStored(ctx context.Context) error {
	r, e := s.Identity.Repository()
	if e != nil {
		return safe(e)
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	return safe(r.Within(bounded, true, func(ctx context.Context, q transaction.Executor) error {
		var tenant string
		if e := q.QueryRow(ctx, statement(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1", `SELECT "TENANT_ID" FROM "INSTALLATION_STATE" WHERE "ID"=1 AND "BOOTSTRAP_COMPLETE"=1`)).Scan(&tenant); e != nil {
			return e
		}
		if tenant != s.Stage.Tenant() {
			return fail("OBJECT_STORE_CHANGED")
		}
		// Resolve actual product columns. Missing candidate migrations fail admission.
		for _, pair := range [][3]string{
			{"SELECT " + pgObjectFields + " FROM artifact_objects LIMIT 0", "SELECT " + myObjectFields + " FROM artifact_objects LIMIT 0", `SELECT ` + dmObjectFields + ` FROM "ARTIFACT_OBJECTS" LIMIT 0`},
			{"SELECT tenant_id,workspace_id,object_id,number,bytes,sha256,stage_id,state,etag FROM artifact_object_parts LIMIT 0", "SELECT tenant_id,workspace_id,object_id,number,bytes,sha256,stage_id,state,etag FROM artifact_object_parts LIMIT 0", `SELECT "TENANT_ID","WORKSPACE_ID","OBJECT_ID","NUMBER","BYTES","SHA256","STAGE_ID","STATE","ETAG" FROM "ARTIFACT_OBJECT_PARTS" LIMIT 0`},
			{"SELECT id,object_id,recipient_user_id,creator_epoch,recipient_epoch,not_before,expires_at,revoked_at FROM artifact_object_grants LIMIT 0", "SELECT id,object_id,recipient_user_id,creator_epoch,recipient_epoch,not_before,expires_at,revoked_at FROM artifact_object_grants LIMIT 0", `SELECT "ID","OBJECT_ID","RECIPIENT_USER_ID","CREATOR_EPOCH","RECIPIENT_EPOCH","NOT_BEFORE","EXPIRES_AT","REVOKED_AT" FROM "ARTIFACT_OBJECT_GRANTS" LIMIT 0`},
			{"SELECT id,object_id,kind,model_import_id,dataset_draft_id,source_verified_sha256,receipt FROM artifact_object_ingestions LIMIT 0", "SELECT id,object_id,kind,model_import_id,dataset_draft_id,source_verified_sha256,receipt FROM artifact_object_ingestions LIMIT 0", `SELECT "ID","OBJECT_ID","KIND","MODEL_IMPORT_ID","DATASET_DRAFT_ID","SOURCE_VERIFIED_SHA256","RECEIPT" FROM "ARTIFACT_OBJECT_INGESTIONS" LIMIT 0`},
		} {
			rows, e := q.Query(ctx, statement(q, pair[0], pair[1], pair[2]))
			if e != nil {
				return e
			}
			rows.Close()
			if e = rows.Err(); e != nil {
				return e
			}
		}
		var invalid int64
		if e := q.QueryRow(ctx, statement(q, "SELECT count(*) FROM artifact_objects WHERE tenant_id<>$1 OR store_id<>$2", "SELECT count(*) FROM artifact_objects WHERE tenant_id<>? OR store_id<>?", `SELECT COUNT(*) FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"<>? OR "STORE_ID"<>?`, s.Stage.Tenant(), s.Stage.Namespace())).Scan(&invalid); e != nil {
			return e
		}
		if invalid != 0 {
			return fail("OBJECT_STORE_CHANGED")
		}
		if e := q.QueryRow(ctx, statement(q, "SELECT count(*) FROM artifact_object_ingestions i JOIN artifact_objects o ON o.tenant_id=i.tenant_id AND o.workspace_id=i.workspace_id AND o.id=i.object_id WHERE i.state='submitted' AND (i.source_verified_sha256 IS NULL OR i.source_verified_sha256<>o.sha256 OR i.source_verified_at IS NULL OR i.receipt IS NULL)", "SELECT count(*) FROM artifact_object_ingestions i JOIN artifact_objects o ON o.tenant_id=i.tenant_id AND o.workspace_id=i.workspace_id AND o.id=i.object_id WHERE i.state='submitted' AND (i.source_verified_sha256 IS NULL OR i.source_verified_sha256<>o.sha256 OR i.source_verified_at IS NULL OR i.receipt IS NULL)", `SELECT COUNT(*) FROM "ARTIFACT_OBJECT_INGESTIONS" "I" JOIN "ARTIFACT_OBJECTS" "O" ON "O"."TENANT_ID"="I"."TENANT_ID" AND "O"."WORKSPACE_ID"="I"."WORKSPACE_ID" AND "O"."ID"="I"."OBJECT_ID" WHERE "I"."STATE"='submitted' AND ("I"."SOURCE_VERIFIED_SHA256" IS NULL OR "I"."SOURCE_VERIFIED_SHA256"<>"O"."SHA256" OR "I"."SOURCE_VERIFIED_AT" IS NULL OR "I"."RECEIPT" IS NULL)`)).Scan(&invalid); e != nil {
			return e
		}
		if invalid != 0 {
			return fail("OBJECT_STORED_PROVENANCE_INVALID")
		}
		return nil
	}))
}
