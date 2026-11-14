package http

import (
	"errors"
	"sync"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
	"github.com/google/uuid"
)

type agentGatewayPublishedReaders struct {
	datasets   *datasets.PublishedReader
	training   *training.PublishedReader
	closeOnce  sync.Once
	closeError error
}

func (r *agentGatewayPublishedReaders) close() error {
	r.closeOnce.Do(func() { r.closeError = errors.Join(r.training.Close(), r.datasets.Close()) })
	return r.closeError
}

// NewAgentGatewayWithPublishedReaders builds the ordinary gateway graph with
// independent readers of both configured, existing artifact namespaces. tenant
// must come from the trusted installed-tenant startup owner, never client input.
// It creates no namespace, writable store or processing worker. On failure it
// closes every reader it opened and returns no gateway. The successful caller
// owns ClosePublishedReaders after stopping and joining its active requests.
func NewAgentGatewayWithPublishedReaders(s *identity.Service, ready func() error, tenant string) (*AgentGateway, error) {
	id, e := uuid.Parse(tenant)
	if s == nil || s.DB == nil || ready == nil || e != nil || id == uuid.Nil || id.String() != tenant || s.Config.DatabaseEngine() != config.PostgreSQLDatabaseEngine {
		return nil, errors.New("AGENT_GATEWAY_CONFIGURATION_INVALID")
	}
	datasetReader, e := datasets.OpenPublishedReader(s.Config.Datasets.ArtifactRoot, tenant)
	if e != nil {
		return nil, e
	}
	trainingReader, e := training.OpenPublishedReader(s.Config.Training.ArtifactRoot, tenant)
	if e != nil {
		return nil, errors.Join(e, datasetReader.Close())
	}
	readers := &agentGatewayPublishedReaders{datasets: datasetReader, training: trainingReader}
	p := newAgentPath(s, telemetrystore.ServiceAgentGateway, ready)
	if p.err != nil {
		return nil, errors.Join(p.err, readers.close())
	}
	p.datasets.PublishedReader = datasetReader
	p.training.PublishedReader = trainingReader
	gateway := agentGatewayFromPath(p)
	gateway.publishedReaders = readers
	return gateway, nil
}

// ClosePublishedReaders releases only the independently opened input readers.
// Stop admission and drain/join active handlers and claims before calling it.
// It is safe to call concurrently and repeatedly. Returned file descriptors
// remain owned by their callers; this never closes a writer or the SQL pool.
func (g *AgentGateway) ClosePublishedReaders() error {
	if g == nil || g.publishedReaders == nil {
		return nil
	}
	return g.publishedReaders.close()
}
