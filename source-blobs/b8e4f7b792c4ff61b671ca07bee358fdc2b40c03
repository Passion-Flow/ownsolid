package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestAgentGatewayRequestOwnerSealAndRejoin(t *testing.T) {
	var owner agentGatewayRequests
	for range 32 {
		if !owner.begin() {
			t.Fatal("open owner refused registration")
		}
	}
	joined := owner.stop()
	short, cancel := context.WithCancel(t.Context())
	cancel()
	if err := owner.wait(short); !errors.Is(err, context.Canceled) {
		t.Fatal("active requests reported joined", err)
	}
	var workers sync.WaitGroup
	for range 32 {
		workers.Go(func() {
			if owner.begin() {
				t.Error("request registered after sealing")
			}
			owner.end()
		})
	}
	workers.Wait()
	select {
	case <-joined:
	default:
		t.Fatal("last admitted request did not complete the join")
	}
	if err := owner.wait(short); err != nil {
		t.Fatal("actual completion was hidden by an already canceled caller", err)
	}
}

func TestAgentGatewayHandlerJoinRetainsRealFileOwner(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "published"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	started, release := make(chan struct{}), make(chan struct{})
	var released sync.Once
	finish := func() { released.Do(func() { close(release) }) }
	h := &agentGatewayHandler{ready: func() error {
		file, err := root.Open("published")
		if err != nil {
			return err
		}
		defer file.Close()
		close(started)
		<-release
		_, err = io.ReadAll(file)
		return err
	}}
	server := httptest.NewUnstartedServer(h)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	t.Cleanup(finish)
	h.machine = config.MachineServerConfiguration{ListenAddress: strings.TrimPrefix(server.URL, "https://"), Origin: server.URL, TLSCertificateFile: "/synthetic/certificate.pem", TLSPrivateKeyFile: "/synthetic/key.pem"}
	g := newRuntimeLifecycle(t.Context())
	g.stops = append(g.stops, h.requests.wait)
	g.closers = append(g.closers, root.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	response := make(chan error, 1)
	go func() {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/health/ready", nil)
		if err != nil {
			response <- err
			return
		}
		r, err := server.Client().Do(request)
		if err == nil {
			_, err = io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if r.StatusCode != http.StatusOK {
				err = errors.New("ADMITTED_REQUEST_FAILED")
			}
		}
		response <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("real HTTPS request did not open its file")
	}
	h.requests.stop()
	lateRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/health/ready", nil)
	if err != nil {
		t.Fatal(err)
	}
	late, err := server.Client().Do(lateRequest)
	if err != nil {
		t.Fatal(err)
	}
	late.Body.Close()
	if late.StatusCode != http.StatusServiceUnavailable {
		t.Fatal("sealed handler dispatched another file owner", late.StatusCode)
	}
	short, stop := context.WithCancel(t.Context())
	stop()
	if err := g.Close(short); err == nil || err.Error() != "CONTROL_SHUTDOWN_TIMEOUT" {
		t.Fatal("unreturned HTTP request reported a process join", err)
	}
	file, err := root.Open("published")
	if err != nil {
		t.Fatal("timed-out join closed a still-used file owner", err)
	}
	file.Close()
	finish()
	select {
	case err := <-response:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("admitted request did not finish")
	}
	if err := g.Close(ctx); err != nil {
		t.Fatal("confirmed HTTP owner join failed", err)
	}
	if file, err := root.Open("published"); err == nil {
		file.Close()
		t.Fatal("confirmed process join retained the file owner")
	}
}
