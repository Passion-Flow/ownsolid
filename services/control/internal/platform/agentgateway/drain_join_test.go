package agentgateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This isolates RPC ownership, not database authorization or reader qualification.
type retainedFileBackend struct {
	fakeBackend
	path      string
	file      *os.File
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func (b *retainedFileBackend) Heartbeat(ctx context.Context, _, _ string, _ wire.Handshake) (wire.Disposition, error) {
	f, err := os.Open(b.path)
	if err != nil {
		return wire.Disposition{}, err
	}
	defer f.Close()
	b.file = f
	close(b.started)
	<-ctx.Done()
	close(b.cancelled)
	<-b.release
	var data [1]byte
	if _, err := f.Read(data[:]); err != nil {
		return wire.Disposition{}, err
	}
	return wire.Disposition{}, ctx.Err()
}

func TestDrainAndWaitRetainsBackendUntilActualCallbackExit(t *testing.T) {
	file := filepath.Join(t.TempDir(), "published-asset")
	if err := os.WriteFile(file, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	b := &retainedFileBackend{path: file, started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	g, conn := start(t, b, 4)
	var release sync.Once
	finish := func() { release.Do(func() { close(b.release) }) }
	t.Cleanup(finish)
	stream := open(t, conn)
	if err := stream.SendMsg(heartbeat(1)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-b.started:
	case <-ctx.Done():
		t.Fatal("RPC did not reach retained file owner")
	}
	g.Drain()
	select {
	case <-b.cancelled:
	case <-ctx.Done():
		t.Fatal("drain did not cancel the real RPC callback")
	}
	if _, err := exchange(t, open(t, conn), heartbeat(2)); status.Code(err) != codes.Unavailable {
		t.Fatal("drained gateway admitted another callback", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithCancel(t.Context())
	stop()
	if err := g.DrainAndWait(short); !errors.Is(err, context.Canceled) {
		t.Fatal("unreturned backend reported a join", err)
	}
	if _, err := b.file.Stat(); err != nil {
		t.Fatal("transport close invalidated the callback's retained file", err)
	}
	select {
	case <-g.joined:
		t.Fatal("closed transport was mistaken for callback completion")
	default:
	}
	finish()
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			if err := g.DrainAndWait(ctx); err != nil {
				t.Error("concurrent real callback join", err)
			}
		})
	}
	workers.Wait()
	if _, err := b.file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("confirmed callback join preceded its file close", err)
	}
}

func TestDrainAndWaitEmptyGatewayAndInvalidContext(t *testing.T) {
	g, _ := start(t, &fakeBackend{}, 1)
	if err := g.DrainAndWait(nil); err == nil || g.draining.Err() != nil {
		t.Fatal("invalid context changed admission", err)
	}
	if err := g.DrainAndWait(t.Context()); err != nil {
		t.Fatal("empty gateway did not join", err)
	}
	if err := g.DrainAndWait(t.Context()); err != nil {
		t.Fatal("repeated drain did not join", err)
	}
}
