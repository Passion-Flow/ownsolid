package httpserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestAgedListenerReportsUnconfirmedProcessShutdown(t *testing.T) {
	for _, cooperate := range []bool{true, false} {
		name := "handler_joins"
		if !cooperate {
			name = "handler_exceeds_budget"
		}
		t.Run(name, func(t *testing.T) {
			certificate, roots := transportCertificate(t)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			var releaseOnce sync.Once
			finish := func() { releaseOnce.Do(func() { close(release) }) }
			handlerDone := make(chan struct{})
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(handlerDone)
				w.WriteHeader(http.StatusOK)
				if err := http.NewResponseController(w).Flush(); err != nil {
					return
				}
				if !cooperate {
					<-release
					return
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			})
			ctx, stop := context.WithCancel(t.Context())
			policy := config.DefaultHTTPPolicy()
			policy.ShutdownSeconds = 1
			done := make(chan error, 1)
			go func() {
				done <- ServeListener(ctx, listener, &tls.Config{Certificates: []tls.Certificate{certificate},
					MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"}}, handler, policy,
					ConnectionAge{Maximum: time.Minute, Grace: time.Second})
			}()
			transport := &http.Transport{ForceAttemptHTTP2: true,
				TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}}
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			t.Cleanup(func() {
				stop()
				finish()
				transport.CloseIdleConnections()
			})
			response, err := client.Get("https://" + listener.Addr().String() + "/held")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.ProtoMajor != 2 || response.StatusCode != http.StatusOK {
				t.Fatalf("held HTTP/2 response not established: %s %d", response.Proto, response.StatusCode)
			}
			stop()
			select {
			case err := <-done:
				if cooperate && err != nil {
					t.Fatalf("cooperative shutdown failed: %v", err)
				}
				if !cooperate && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("unconfirmed shutdown reported success: %v", err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("connection owner exceeded process shutdown bound")
			}
			finish()
			select {
			case <-handlerDone:
			case <-time.After(time.Second):
				t.Fatal("released handler did not join")
			}
		})
	}
}

func TestAgedListenerReleasesConnectionCapacityAfterPeerClose(t *testing.T) {
	certificate, roots := transportCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	policy := config.DefaultHTTPPolicy()
	policy.MaxConnections = 1
	policy.MaxActiveRequests = 1
	done := make(chan error, 1)
	go func() {
		done <- ServeListener(ctx, listener, &tls.Config{Certificates: []tls.Certificate{certificate},
			MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"}}, http.NotFoundHandler(), policy,
			ConnectionAge{Maximum: time.Minute, Grace: time.Second})
	}()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("connection-capacity listener did not join")
		}
	})
	dial := func() (*tls.Conn, error) {
		return tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", listener.Addr().String(),
			&tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"}})
	}
	first, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if extra, err := dial(); err == nil {
		_ = extra.Close()
		t.Fatal("listener exceeded its single connection slot")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		next, err := dial()
		if err == nil {
			if next.ConnectionState().NegotiatedProtocol != "h2" {
				t.Error("replacement did not negotiate verified HTTP/2")
			}
			_ = next.Close()
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("peer closure did not release connection capacity: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRetirementLogUsesObservedElapsedTime(t *testing.T) {
	accepted := time.Now()
	conn := &agingConn{id: "observed-connection", acceptedAt: accepted, drainAt: accepted.Add(55 * time.Second)}
	for _, tc := range []struct {
		name, reason      string
		started, finished time.Duration
		forced            bool
	}{
		{"early_process_stop", "process_shutdown", time.Second, 2 * time.Second, false},
		{"delayed_age_drain", "connection_age", 55*time.Second + 3*time.Millisecond, 56*time.Second + 250*time.Millisecond, false},
		{"grace_expiry", "connection_age", 55*time.Second + 5*time.Millisecond, 57 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			logger.Info("http_connection_retired", retirementLogFields(conn, tc.reason, accepted.Add(tc.started), accepted.Add(tc.finished), tc.forced)...)
			var event struct {
				Message   string    `json:"msg"`
				Reason    string    `json:"reason"`
				ID        string    `json:"connection_id"`
				Scheduled float64   `json:"scheduled_age_seconds"`
				Started   float64   `json:"drain_started_age_seconds"`
				Age       float64   `json:"age_seconds"`
				Forced    bool      `json:"forced"`
				StartedAt time.Time `json:"drain_started_at"`
				RetiredAt time.Time `json:"retired_at"`
			}
			if err := json.Unmarshal(output.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			if event.Message != "http_connection_retired" || event.Reason != tc.reason || event.ID != conn.id || event.Forced != tc.forced {
				t.Fatalf("lost retirement identity/outcome: %+v", event)
			}
			if event.Scheduled != 55 || math.Abs(event.Started-tc.started.Seconds()) > 1e-9 || math.Abs(event.Age-tc.finished.Seconds()) > 1e-9 {
				t.Fatalf("planned jitter replaced observed elapsed time: %+v", event)
			}
			if !event.StartedAt.Equal(accepted.Add(tc.started)) || !event.RetiredAt.Equal(accepted.Add(tc.finished)) {
				t.Fatal("lost actual retirement timestamps")
			}
		})
	}
}
