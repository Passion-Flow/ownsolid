//go:build repository_integration && monitoring_integration && linux

package monitoring_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	"github.com/google/uuid"
)

// This owner binds a verified fixture child only. Installed inventory approval,
// authenticated cell binding and process supervision remain the startup owner's
// responsibility; this fixture must never be wired into product startup.
type cardinalityFixtureOwner struct {
	observation monitoring.SeriesCardinalitySourceObservation
	life        context.Context
	transport   *http.Transport
	beforeRead  func()
}

func (o *cardinalityFixtureOwner) OpenSeriesCardinalitySource(ctx context.Context, id string) (monitoring.SeriesCardinalitySourceLease, error) {
	if ctx.Err() != nil || o.life.Err() != nil || id != o.observation.SourceID {
		return nil, fmt.Errorf("fixture source unavailable")
	}
	return &cardinalityFixtureLease{owner: o}, nil
}

type cardinalityFixtureLease struct{ owner *cardinalityFixtureOwner }

func (l *cardinalityFixtureLease) Observation() monitoring.SeriesCardinalitySourceObservation {
	o := l.owner.observation
	o.Arguments = slices.Clone(o.Arguments)
	return o
}
func (l *cardinalityFixtureLease) Context() context.Context { return l.owner.life }
func (l *cardinalityFixtureLease) Close() error             { return nil }
func (l *cardinalityFixtureLease) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme+"://"+r.URL.Host != l.owner.observation.Origin || l.owner.life.Err() != nil {
		return nil, fmt.Errorf("fixture endpoint changed")
	}
	if l.owner.beforeRead != nil {
		l.owner.beforeRead()
	}
	return l.owner.transport.RoundTrip(r)
}

func newCardinalityFixtureOwner(t *testing.T, c monitoring.Config, process monitoringFixtureProcess, before func()) *cardinalityFixtureOwner {
	t.Helper()
	// Read approval input separately from the running /proc executable digest.
	file, err := os.Open(os.Getenv("OWNSOLID_TEST_VICTORIA_METRICS"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, readError := io.Copy(hash, file)
	closeError := file.Close()
	if readError != nil || closeError != nil {
		t.Fatal(readError, closeError)
	}
	transport := &http.Transport{MaxConnsPerHost: c.ConcurrentSnapshots, MaxIdleConnsPerHost: c.ConcurrentSnapshots,
		IdleConnTimeout: time.Duration(c.OperationSeconds) * time.Second, ResponseHeaderTimeout: time.Duration(c.OperationSeconds) * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	return &cardinalityFixtureOwner{monitoring.SeriesCardinalitySourceObservation{
		SourceID: c.SeriesCardinality.SourceID, InstanceID: uuid.NewString(), Origin: process.origin,
		ApprovedExecutableSHA256: fmt.Sprintf("%x", hash.Sum(nil)), RunningExecutableSHA256: process.binarySHA256, Arguments: slices.Clone(process.argv),
	}, process.life, transport, before}
}

func openCardinalityFixtureSource(t *testing.T, g *story, process monitoringFixtureProcess, before func()) *monitoring.SeriesCardinalityQuerySource {
	t.Helper()
	ctx, cancel := context.WithTimeout(g.ctx, 5*time.Second)
	defer cancel()
	source, err := monitoring.OpenSeriesCardinalityQuerySource(ctx, g.config, newCardinalityFixtureOwner(t, g.config, process, before))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	return source
}
