//go:build repository_integration && object_storage_integration

package artifactstores

import (
	"bytes"
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

type backupReadHold struct {
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func newBackupReadHold() *backupReadHold {
	return &backupReadHold{started: make(chan struct{}), resume: make(chan struct{})}
}

type backupTestProxy struct {
	hold     atomic.Pointer[backupReadHold]
	requests atomic.Uint64
}

func (p *backupTestProxy) serve(upstream http.Handler, w http.ResponseWriter, r *http.Request) {
	p.requests.Add(1)
	if h := p.hold.Load(); h != nil && r.Method == http.MethodGet && r.Header.Get("If-Match") != "" {
		h.once.Do(func() { close(h.started) })
		select {
		case <-h.resume:
		case <-r.Context().Done():
			return
		}
	}
	upstream.ServeHTTP(w, r)
}

func backupProviderFixture(t *testing.T) (*objectStory, *backupTestProxy) {
	t.Helper()
	origin, err := url.Parse(os.Getenv("OWNSOLID_TEST_BACKUP_S3"))
	if err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		t.Fatal("OWNSOLID_TEST_BACKUP_S3 must name the isolated SeaweedFS endpoint")
	}
	root := t.TempDir()
	keys := os.Getenv("OWNSOLID_TEST_BACKUP_KEYS")
	if !filepath.IsAbs(keys) {
		t.Fatal("an isolated provider credential directory is required")
	}
	stagePath := filepath.Join(root, "stage")
	if err := os.Mkdir(stagePath, 0700); err != nil {
		t.Fatal(err)
	}

	proxy := &backupTestProxy{}
	upstream := httputil.NewSingleHostReverseProxy(origin)
	front := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy.serve(upstream, w, r)
	}))
	t.Cleanup(front.Close)
	ca := filepath.Join(root, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: front.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	c := config.ObjectStorageConfiguration{
		Enabled: true, Backend: "seaweedfs_s3", Endpoint: front.URL, Region: "us-east-1",
		Bucket: "m22-fixture-" + strings.ReplaceAll(uuid.NewString(), "-", ""), RefusalBucket: "fixture-refusal",
		AccessKeyFile: filepath.Join(keys, "access"), SecretKeyFile: filepath.Join(keys, "secret"),
		RefusalAccessKeyFile: filepath.Join(keys, "refusal-access"), RefusalSecretKeyFile: filepath.Join(keys, "refusal-secret"),
		Transport: "https", CAFile: ca, DialAddresses: []string{"127.0.0.1"}, StagingRoot: stagePath,
		PartBytes: config.MinimumS3PartBytes, MaximumObjectBytes: 16 << 20, MinimumFreeBytes: 4096,
		OperationSeconds: 5, TransferSeconds: 20, UploadSeconds: 60, ObjectLifetimeSeconds: 3600,
		MaximumGrantSeconds: 300, MaximumConcurrentTransfers: 1, MaximumListPages: 2, WorkerPollSeconds: 1,
	}
	// This fixture qualifies the data plane, with real signed requests through
	// a TLS frontend. Full startup and management mTLS have their own profile.
	for i, kind := range []string{"s3_grpc", "master_grpc", "filer_grpc", "volume_grpc"} {
		c.Management = append(c.Management, config.ObjectStorageManagementProbe{
			Kind: kind, Authority: "127.0.0.1:" + strconv.Itoa(55001+i), DialAddresses: []string{"127.0.0.1"},
			CAFile: ca, ClientCertificateFile: ca, ClientKeyFile: filepath.Join(root, "unused-management-key"),
		})
	}
	access, secret, err := c.ObjectStorageSecrets()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	t.Cleanup(cancel)
	client := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	bootstrap := s3.New(s3.Options{
		BaseEndpoint: aws.String(origin.String()), UsePathStyle: true, Region: c.Region,
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: access, SecretAccessKey: secret}, nil
		}),
		HTTPClient: client,
		Retryer:    aws.NopRetryer{},
	})
	if _, err := bootstrap.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &c.Bucket}); err != nil {
		t.Fatal(err)
	}

	fixture := repositoryfixture.New(t)
	if err := objectstore.InitializeStage(c, fixture.Tenant); err != nil {
		t.Fatal(err)
	}
	stage, err := objectstore.OpenStage(c, fixture.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	transport, err := objectstore.NewS3(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	owner := &identity.Service{Storage: fixture.Store, Config: config.Config{ObjectStorage: &c}}
	workspaces := &workspaces.Service{Identity: owner}
	g := &objectStory{f: fixture, ctx: ctx, owner: owner}
	g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "", "", fixture.Tenant, fixture.Workspace, fixture.User)
	g.admin = g.session(t, fixture.User, "admin")
	g.portal = g.session(t, fixture.User, "portal")
	g.initializeLicense(t)

	run, stop := context.WithCancel(context.Background())
	g.s = &Service{Identity: owner, Workspaces: workspaces, Configuration: c, Transport: transport, Stage: stage}
	g.s.lifecycle = &lifecycle{
		ctx: run, cancel: stop, digest: configurationDigest(c), identity: owner,
		workspaces: workspaces, transport: transport, stage: stage, license: g.license,
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := g.s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return g, proxy
}

type backupAcquireResult struct {
	fence *BackupFence
	err   error
}

func acquireInBackground(ctx context.Context, s *Service) <-chan backupAcquireResult {
	result := make(chan backupAcquireResult, 1)
	go func() {
		fence, err := s.AcquireBackupFence(ctx)
		result <- backupAcquireResult{fence, err}
	}()
	return result
}

func waitForBackupGate(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		s.lifecycle.mu.Lock()
		active := s.lifecycle.backup != nil
		s.lifecycle.mu.Unlock()
		if active {
			return
		}
		select {
		case <-deadline:
			t.Fatal("backup admission did not close")
		case <-ticker.C:
		}
	}
}

func awaitBackupValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for fixture")
		var zero T
		return zero
	}
}

type backupPartReader struct {
	io.Reader
	hold *backupReadHold
}

func (r backupPartReader) Read(p []byte) (int, error) {
	r.hold.once.Do(func() { close(r.hold.started) })
	<-r.hold.resume
	return r.Reader.Read(p)
}

func TestActualObjectBackupFence(t *testing.T) {
	g, proxy := backupProviderFixture(t)
	source := []byte("isolated artifact bytes for backup")
	pending, request, _ := g.create(t, source)
	g.work(t)
	part := objectstore.Part{Number: 1, Bytes: pending.ExpectedBytes, SHA256: pending.SHA256}
	partHold := newBackupReadHold()
	t.Cleanup(func() {
		select {
		case <-partHold.resume:
		default:
			close(partHold.resume)
		}
	})
	partDone := make(chan error, 1)
	go func() {
		_, err := g.s.PutPart(g.ctx, g.portal, g.f.Workspace, pending.ID, part,
			backupPartReader{bytes.NewReader(source), partHold}, uuid.NewString())
		partDone <- err
	}()
	awaitBackupValue(t, partHold.started)

	acquire, cancel := context.WithCancel(g.ctx)
	defer cancel()
	first := acquireInBackground(acquire, g.s)
	waitForBackupGate(t, g.s)
	if _, err := g.s.AcquireBackupFence(g.ctx); err != errBackupFenced {
		t.Fatalf("concurrent acquire: %v", err)
	}
	if _, err := g.s.Delete(g.ctx, g.portal, g.f.Workspace, pending.ID, "Delete test object", uuid.NewString()); err != errBackupFenced {
		t.Fatalf("delete while draining: %v", err)
	}
	if _, err := g.s.Create(g.ctx, g.portal, request, uuid.NewString(), uuid.NewString()); err != errBackupFenced {
		t.Fatalf("create while draining: %v", err)
	}
	if worked, err := g.s.ProcessNext(g.ctx); worked || err != errBackupFenced {
		t.Fatalf("worker while draining: %v %v", worked, err)
	}
	cancel()
	if result := awaitBackupValue(t, first); result.fence != nil || !transaction.IsCancellationOnly(result.err) {
		t.Fatalf("canceled acquisition: %+v", result)
	}

	second := acquireInBackground(g.ctx, g.s)
	waitForBackupGate(t, g.s)
	select {
	case <-second:
		t.Fatal("fence acquired before admitted part finished")
	default:
	}
	close(partHold.resume)
	if err := awaitBackupValue(t, partDone); err != nil {
		t.Fatal(err)
	}
	result := awaitBackupValue(t, second)
	if result.err != nil {
		t.Fatal(result.err)
	}
	f := result.fence
	checkBackupRefusal(t, f, g.ctx, "OBJECT_BACKUP_UNSETTLED")
	if err := f.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.s.Complete(g.ctx, g.portal, g.f.Workspace, pending.ID, "Complete fixture object", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	g.work(t)
	tombstone, _, _ := g.create(t, []byte("disposable upload to abort"))
	g.work(t)
	if _, err := g.s.Abort(g.ctx, g.portal, g.f.Workspace, tombstone.ID, "Abort fixture object", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	g.work(t)
	current, err := g.s.AcquireBackupFence(g.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.s.Delete(g.ctx, g.portal, g.f.Workspace, pending.ID, "Delete fixture object", uuid.NewString()); err != errBackupFenced {
		t.Fatalf("stale token released successor: %v", err)
	}

	checkBackupRefusalWithLimit(t, current, g.ctx, 1, "OBJECT_BACKUP_INVENTORY_LIMIT")
	initial, err := current.VerifyInventory(g.ctx, 8)
	if err != nil || len(initial.Objects) != 2 || initial.TenantID != g.f.Tenant || initial.DatabaseOID <= 0 ||
		!objectstore.ValidDigest(initial.InventorySHA256) {
		t.Fatalf("incorrect SQL/provider inventory: %+v %v", initial, err)
	}
	initialReady := backupEntry(t, initial, pending.ID)
	if initialReady.State != "ready" || initialReady.SHA256 != pending.SHA256 || initialReady.ETag == nil ||
		backupEntry(t, initial, tombstone.ID).State != "aborted" {
		t.Fatalf("incorrect ready object or tombstone: %+v", initial.Objects)
	}
	ref := objectstore.Ref{Tenant: g.f.Tenant, Workspace: g.f.Workspace, Object: pending.ID}
	abandoned, err := g.s.Transport.Create(g.ctx, ref, metadata(storedObject{Object: pending}))
	if err != nil {
		t.Fatal(err)
	}
	checkBackupRefusal(t, current, g.ctx, "OBJECT_BACKUP_UNSETTLED")
	if err := g.s.Transport.Abort(g.ctx, ref, abandoned.ID); err != nil {
		t.Fatal(err)
	}

	ingestion := uuid.NewString()
	g.exec(t, `INSERT INTO artifact_object_ingestions(id,tenant_id,workspace_id,object_id,created_by,creation_key,fingerprint,kind,state,input)
		SELECT $2,tenant_id,workspace_id,id,created_by,$2,fingerprint,'model','intent','{}'
		FROM artifact_objects WHERE id=$1`, "", "", pending.ID, ingestion)
	checkBackupRefusal(t, current, g.ctx, "OBJECT_BACKUP_UNSETTLED")
	g.exec(t, "UPDATE artifact_object_ingestions SET state='running' WHERE id=$1", "", "", ingestion)
	checkBackupRefusal(t, current, g.ctx, "OBJECT_BACKUP_UNSETTLED")
	g.exec(t, "UPDATE artifact_object_ingestions SET state='failed' WHERE id=$1", "", "", ingestion)

	type resultInventory struct {
		inventory BackupInventory
		err       error
	}
	verify := func(f *BackupFence) <-chan resultInventory {
		result := make(chan resultInventory, 1)
		go func() {
			inventory, err := f.VerifyInventory(g.ctx, 8)
			result <- resultInventory{inventory, err}
		}()
		return result
	}
	h := newBackupReadHold()
	proxy.hold.Store(h)
	changed := verify(current)
	awaitBackupValue(t, h.started)
	checkBackupRefusal(t, current, g.ctx, "OBJECT_BACKUP_BUSY")
	if err := current.Release(); err == nil || err.Error() != "OBJECT_BACKUP_BUSY" {
		t.Fatalf("released during readback: %v", err)
	}
	g.exec(t, "UPDATE artifact_objects SET revision=revision+1 WHERE id=$1", "", "", pending.ID)
	proxy.hold.Store(nil)
	close(h.resume)
	if got := awaitBackupValue(t, changed); got.err == nil || got.err.Error() != "OBJECT_BACKUP_INVENTORY_CHANGED" || !reflect.DeepEqual(got.inventory, BackupInventory{}) {
		t.Fatalf("changed SQL inventory: %+v", got)
	}
	retry, err := current.VerifyInventory(g.ctx, 8)
	if err != nil || len(retry.Objects) != 2 || backupEntry(t, retry, pending.ID).Revision != initialReady.Revision+1 ||
		retry.InventorySHA256 == initial.InventorySHA256 {
		t.Fatalf("retry did not bind revised SQL inventory: %+v %v", retry, err)
	}

	foreignTenant, foreignWorkspace, foreignObject := uuid.NewString(), uuid.NewString(), uuid.NewString()
	g.exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign fixture tenant','active')", "", "", foreignTenant)
	g.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign fixture Workspace','active')", "", "", foreignWorkspace, foreignTenant)
	g.exec(t, `INSERT INTO artifact_objects(id,tenant_id,workspace_id,created_by,requested_audience,requested_epoch,
		creation_key,fingerprint,name,purpose,expected_bytes,sha256,part_bytes,store_id,state,
		operation_id,operation_kind,operation_phase,operation_authority,operation_expires_at,upload_expires_at,expires_at,execution_epoch)
		SELECT $2,$3,$4,created_by,requested_audience,requested_epoch,$2,fingerprint,name,purpose,expected_bytes,sha256,
		part_bytes,store_id,'creating',$2,'create','prepared','{}',now(),upload_expires_at,expires_at,requested_epoch
		FROM artifact_objects WHERE id=$1`, "", "", pending.ID, foreignObject, foreignTenant, foreignWorkspace)
	requests := proxy.requests.Load()
	checkBackupRefusal(t, current, g.ctx, "OBJECT_BACKUP_SCOPE_MISMATCH")
	if proxy.requests.Load() != requests {
		t.Fatal("foreign scope reached the provider")
	}
	g.exec(t, "DELETE FROM artifact_objects WHERE id=$1", "", "", foreignObject)

	h = newBackupReadHold()
	proxy.hold.Store(h)
	terminated := verify(current)
	awaitBackupValue(t, h.started)
	if err := g.s.Close(g.ctx); err != nil {
		t.Fatal(err)
	}
	if got := awaitBackupValue(t, terminated); !transaction.IsCancellationOnly(got.err) || !reflect.DeepEqual(got.inventory, BackupInventory{}) {
		t.Fatalf("shutdown readback result: %+v", got)
	}
	if err := current.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.s.AcquireBackupFence(g.ctx); err == nil || err.Error() != "OBJECT_NOT_ADMITTED" {
		t.Fatalf("stopped service admitted a fence: %v", err)
	}
	t.Log("SeaweedFS multipart drain, ingestion refusal, tombstone verification, scope refusal, changed inventory retry and cancellation passed")
}

func backupEntry(t *testing.T, inventory BackupInventory, object string) BackupObjectVersion {
	t.Helper()
	for _, entry := range inventory.Objects {
		if entry.ObjectID == object {
			return entry
		}
	}
	t.Fatalf("object %s missing from inventory", object)
	return BackupObjectVersion{}
}

func checkBackupRefusal(t *testing.T, f *BackupFence, ctx context.Context, code string) {
	t.Helper()
	checkBackupRefusalWithLimit(t, f, ctx, 8, code)
}

func checkBackupRefusalWithLimit(t *testing.T, f *BackupFence, ctx context.Context, limit int, code string) {
	t.Helper()
	out, err := f.VerifyInventory(ctx, limit)
	if err == nil || err.Error() != code || !reflect.DeepEqual(out, BackupInventory{}) {
		t.Fatalf("expected %s and no receipt: %+v %v", code, out, err)
	}
}
