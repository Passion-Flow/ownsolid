package http

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type agentReaderFixture struct {
	owner       *identity.Service
	tenant      string
	connections *atomic.Int32
}

func newAgentReaderFixture(t *testing.T) agentReaderFixture {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	datasetPath, trainingPath := filepath.Join(root, "datasets"), filepath.Join(root, "training")
	for _, path := range []string{datasetPath, trainingPath} {
		if e = os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	tenant := uuid.NewString()
	if e = datasets.InitializeStore(datasetPath, tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	if e = training.InitializeStore(trainingPath, tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	datasetWriter, e := datasets.OpenStore(datasetPath, tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := datasetWriter.Close(); e != nil {
			t.Error(e)
		}
	})
	trainingWriter, e := training.OpenStore(trainingPath, tenant, 4096, 1<<20, 16)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := trainingWriter.Close(); e != nil {
			t.Error(e)
		}
	})
	poolConfig, e := pgxpool.ParseConfig("postgresql://127.0.0.1/ownsolid_test?sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	connections := new(atomic.Int32)
	poolConfig.MinConns = 0
	poolConfig.BeforeConnect = func(context.Context, *pgx.ConnConfig) error {
		connections.Add(1)
		return errors.New("EXPLICIT_NO_DATABASE_FIXTURE")
	}
	pool, e := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	policy := config.DefaultRuntimePolicy()
	owner := &identity.Service{DB: pool, Config: config.Config{Runtime: &policy}}
	owner.Config.Database.MaxOpenConnections = 10
	owner.Config.Database.OperatorReservedConnections = 3
	owner.Config.Datasets.ArtifactRoot = datasetPath
	owner.Config.Training.ArtifactRoot = trainingPath
	t.Cleanup(func() {
		if connections.Load() != 0 {
			t.Error("file-only graph construction attempted a database connection")
		}
	})
	return agentReaderFixture{owner: owner, tenant: tenant, connections: connections}
}

func (f agentReaderFixture) writerExclusion(t *testing.T) {
	t.Helper()
	if writer, e := datasets.OpenStore(f.owner.Config.Datasets.ArtifactRoot, f.tenant, 4096); e == nil {
		writer.Close()
		t.Fatal("gateway reader released the dataset writer's exclusion")
	}
	if writer, e := training.OpenStore(f.owner.Config.Training.ArtifactRoot, f.tenant, 4096, 1<<20, 16); e == nil {
		writer.Close()
		t.Fatal("gateway reader released the training writer's exclusion")
	}
}

// Count only descriptors for the generated namespace inodes. Unrelated pool or
// runtime descriptors cannot change this assertion while another goroutine runs.
func agentReaderOwnedDescriptors(t *testing.T, paths ...string) int {
	t.Helper()
	var owned []os.FileInfo
	for _, path := range paths {
		root, e := os.Stat(path)
		if e != nil {
			t.Fatal(e)
		}
		owned = append(owned, root)
		entries, e := os.ReadDir(path)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range entries {
			info, e := entry.Info()
			if e != nil {
				t.Fatal(e)
			}
			if info.Mode().IsRegular() {
				owned = append(owned, info)
			}
		}
	}
	directory := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		directory = "/dev/fd"
	}
	entries, e := os.ReadDir(directory)
	if e != nil {
		t.Fatal("owned descriptor observation unavailable", e)
	}
	count := 0
	for _, entry := range entries {
		fd, e := strconv.Atoi(entry.Name())
		if e != nil {
			continue
		}
		var held syscall.Stat_t
		if syscall.Fstat(fd, &held) != nil {
			continue
		}
		for _, target := range owned {
			original, ok := target.Sys().(*syscall.Stat_t)
			if ok && held.Dev == original.Dev && held.Ino == original.Ino {
				count++
				break
			}
		}
	}
	return count
}

func TestAgentGatewayPublishedReaderGraph(t *testing.T) {
	f := newAgentReaderFixture(t)
	paths := []string{f.owner.Config.Datasets.ArtifactRoot, f.owner.Config.Training.ArtifactRoot}
	before := agentReaderOwnedDescriptors(t, paths...)
	ready := func() error { return errors.New("EXPLICIT_TEST_NOT_READY") }
	gateway, e := NewAgentGatewayWithPublishedReaders(f.owner, ready, f.tenant)
	if e != nil || gateway == nil {
		t.Fatal("actual reader graph construction", e)
	}
	t.Cleanup(func() {
		gateway.Sessions.Drain()
		if e := gateway.ClosePublishedReaders(); e != nil {
			t.Error(e)
		}
	})
	trainingOwner, ok := gateway.Nodes.Training.(*training.Service)
	if !ok {
		t.Fatal("Node graph lost its concrete training owner")
	}
	preparationOwner, ok := gateway.Nodes.Preparations.(*modelpreparations.Service)
	if !ok {
		t.Fatal("Node graph lost its concrete preparation owner")
	}
	datasetOwner := trainingOwner.Datasets
	if datasetOwner == nil || datasetOwner.Identity != f.owner || trainingOwner.Identity != f.owner || preparationOwner.Identity != f.owner || trainingOwner.Nodes != gateway.Nodes || preparationOwner.Nodes != gateway.Nodes || preparationOwner.Training != trainingOwner || preparationOwner.Datasets != datasetOwner || trainingOwner.Holdouts != preparationOwner || trainingOwner.Workspaces != datasetOwner.Workspaces || preparationOwner.Workspaces != datasetOwner.Workspaces {
		t.Fatal("published readers were attached to a different authority graph")
	}
	if datasetOwner.PublishedReader != gateway.publishedReaders.datasets || trainingOwner.PublishedReader != gateway.publishedReaders.training || datasetOwner.Store != nil || trainingOwner.Store != nil || preparationOwner.Store != nil || datasetOwner.Processor != nil {
		t.Fatal("gateway opened a writer/processor or did not bind its real readers")
	}
	if after := agentReaderOwnedDescriptors(t, paths...); after != before+4 {
		t.Fatal("two independent root/carrier pairs were not retained", before, after)
	}
	f.writerExclusion(t)
	gateway.Sessions.Drain()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if e := gateway.ClosePublishedReaders(); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if after := agentReaderOwnedDescriptors(t, paths...); after != before {
		t.Fatal("reader close leaked namespace descriptors", before, after)
	}
	f.writerExclusion(t)
	if _, e = datasetOwner.PublishedReader.OpenVersionAssetContext(context.Background(), uuid.NewString(), uuid.NewString(), "train.jsonl", 1, "invalid"); e == nil {
		t.Fatal("closed dataset reader remained usable")
	}
	if e = trainingOwner.PublishedReader.VerifyPublished(context.Background(), training.UploadDescriptor{}); e == nil {
		t.Fatal("closed training reader remained usable")
	}
	legacy, e := NewAgentGateway(f.owner, ready)
	if e != nil || legacy == nil || legacy.publishedReaders != nil {
		t.Fatal("existing constructor acquired reader ownership", e)
	}
	legacy.Sessions.Drain()
	if e = legacy.ClosePublishedReaders(); e != nil {
		t.Fatal(e)
	}
}

func TestAgentGatewayPublishedReaderFailureCleanup(t *testing.T) {
	for _, kind := range []string{"foreign_tenant", "missing_second_namespace", "invalid_graph"} {
		t.Run(kind, func(t *testing.T) {
			f := newAgentReaderFixture(t)
			paths := []string{f.owner.Config.Datasets.ArtifactRoot, f.owner.Config.Training.ArtifactRoot}
			before := agentReaderOwnedDescriptors(t, paths...)
			tenant := f.tenant
			switch kind {
			case "foreign_tenant":
				tenant = uuid.NewString()
			case "missing_second_namespace":
				f.owner.Config.Training.ArtifactRoot = filepath.Join(paths[1], "missing")
			case "invalid_graph":
				f.owner.Config.Database.MaxOpenConnections = 0
			}
			gateway, e := NewAgentGatewayWithPublishedReaders(f.owner, func() error { return errors.New("EXPLICIT_TEST_NOT_READY") }, tenant)
			if e == nil || gateway != nil {
				t.Fatal("invalid reader graph returned a gateway", e)
			}
			if after := agentReaderOwnedDescriptors(t, paths...); after != before {
				t.Fatal("constructor failure leaked an owned reader", before, after)
			}
			f.owner.Config.Datasets.ArtifactRoot, f.owner.Config.Training.ArtifactRoot = paths[0], paths[1]
			f.writerExclusion(t)
		})
	}
}

func TestAgentGatewayPublishedReaderRequiresTrustedOwner(t *testing.T) {
	for _, tenant := range []string{"", uuid.Nil.String(), "invalid"} {
		if gateway, e := NewAgentGatewayWithPublishedReaders(nil, nil, tenant); e == nil || gateway != nil {
			t.Fatal("missing owner released a gateway")
		}
	}
	var gateway *AgentGateway
	if e := gateway.ClosePublishedReaders(); e != nil {
		t.Fatal(e)
	}
}
