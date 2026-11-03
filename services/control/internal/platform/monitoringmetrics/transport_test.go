package monitoringmetrics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestRealEmptyWriteCommitsSuccess(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[h2], func(t *testing.T) {
			r, _ := New(Defaults(), nil)
			server := httptest.NewUnstartedServer(r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if n, e := w.Write(nil); n != 0 || e != nil {
					t.Errorf("empty write = %d, %v", n, e)
				}
			})))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			client := server.Client()
			client.Timeout = 3 * time.Second
			resp, e := client.Get(server.URL)
			if e != nil {
				t.Fatal(e)
			}
			body, e := io.ReadAll(resp.Body)
			resp.Body.Close()
			if e != nil || resp.StatusCode != 200 || len(body) != 0 || (resp.ProtoMajor == 2) != h2 {
				t.Fatal("actual empty response", e, resp.StatusCode, resp.ProtoMajor)
			}
			snap := r.Snapshot()
			if snap.Inflight != 0 || len(snap.Series) != 1 || snap.Series[0].Outcome != "succeeded" || snap.Series[0].ResponseBytes != 0 || snap.Series[0].Count != 1 {
				t.Fatal("successful empty write misclassified", snap)
			}
		})
	}
}

func TestRealFullDuplexResponsePrecedesRequestCompletion(t *testing.T) {
	r, _ := New(Defaults(), nil)
	joined := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer close(joined)
		r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			controller := http.NewResponseController(w)
			if e := controller.EnableFullDuplex(); e != nil {
				t.Error(e)
				return
			}
			reader := bufio.NewReader(req.Body)
			if first, e := reader.ReadString('\n'); e != nil || first != "first\n" {
				t.Error("initial request chunk", first, e)
				return
			}
			if _, e := io.WriteString(w, "ready\n"); e != nil {
				t.Error(e)
				return
			}
			if e := controller.Flush(); e != nil {
				t.Error(e)
				return
			}
			if rest, e := io.ReadAll(reader); e != nil || string(rest) != "second\n" {
				t.Error("remaining request chunk", string(rest), e)
				return
			}
			if _, e := io.WriteString(w, "complete\n"); e != nil {
				t.Error(e)
			}
		})).ServeHTTP(w, req)
	}))
	defer server.Close()
	conn, e := net.DialTimeout("tcp", server.Listener.Addr().String(), 3*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if e := conn.SetDeadline(time.Now().Add(3 * time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e := fmt.Fprintf(conn, "POST /duplex HTTP/1.1\r\nHost: fixture.invalid\r\nTransfer-Encoding: chunked\r\n\r\n6\r\nfirst\n\r\n"); e != nil {
		t.Fatal(e)
	}
	response, e := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if e != nil {
		t.Fatal("response blocked until request completion", e)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if first, e := reader.ReadString('\n'); e != nil || first != "ready\n" {
		t.Fatal("response not available before final request chunk", first, e)
	}
	if _, e := io.WriteString(conn, "7\r\nsecond\n\r\n0\r\n\r\n"); e != nil {
		t.Fatal(e)
	}
	if rest, e := io.ReadAll(reader); e != nil || string(rest) != "complete\n" {
		t.Fatal("response completion", string(rest), e)
	}
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("full-duplex handler did not join")
	}
	snap := r.Snapshot()
	if snap.Inflight != 0 || len(snap.Series) != 1 || snap.Series[0].Outcome != "succeeded" || snap.Series[0].ResponseBytes != uint64(len("ready\ncomplete\n")) {
		t.Fatal("full-duplex metrics", snap)
	}
}

func TestRealWriteDeadlineRetainsObservedWriteFailure(t *testing.T) {
	r, _ := New(Defaults(), nil)
	joined := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer close(joined)
		r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			controller := http.NewResponseController(w)
			if e := controller.SetWriteDeadline(time.Now().Add(-time.Second)); e != nil {
				t.Error("actual write deadline unavailable", e)
				return
			}
			io.WriteString(w, "buffered before flush\n")
			if e := controller.Flush(); !errors.Is(e, os.ErrDeadlineExceeded) {
				t.Error("actual transport flush should reach expired deadline", e)
			}
		})).ServeHTTP(w, req)
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	resp, e := client.Get(server.URL)
	if resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if e == nil {
		t.Fatal("expired write deadline fabricated a complete response")
	}
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("write-deadline handler did not join")
	}
	snap := r.Snapshot()
	if snap.Inflight != 0 || len(snap.Series) != 1 || snap.Series[0].Outcome != "write_error" {
		t.Fatal("observed write failure hidden by consequent context cancellation", snap)
	}
}

// Reading the first event while the handler is still waiting proves the flush
// crossed the real TLS transport. Closing the response must cancel that exact
// stream and join its handler before metric readback, in both HTTP versions.
func TestRealSSEFlushAndClientCancellation(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[h2], func(t *testing.T) {
			r, _ := New(Defaults(), nil)
			joined := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				defer close(joined)
				r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					if _, e := io.WriteString(w, "data: first\n\n"); e != nil {
						t.Error(e)
						return
					}
					if e := http.NewResponseController(w).Flush(); e != nil {
						t.Error(e)
						return
					}
					<-req.Context().Done()
				})).ServeHTTP(w, req)
			}))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/workspaces/scoped/events?private=discarded", nil)
			resp, e := server.Client().Do(req)
			if e != nil {
				t.Fatal(e)
			}
			defer resp.Body.Close()
			first, e := bufio.NewReader(resp.Body).ReadString('\n')
			if e != nil || first != "data: first\n" || (resp.ProtoMajor == 2) != h2 {
				t.Fatal("first event was not flushed", first, e)
			}
			select {
			case <-joined:
				t.Fatal("stream ended before cancellation")
			default:
			}
			if e := resp.Body.Close(); e != nil {
				t.Fatal(e)
			}
			select {
			case <-joined:
			case <-ctx.Done():
				t.Fatal("cancelled handler did not join")
			}
			snap := r.Snapshot()
			if snap.Inflight != 0 || len(snap.Series) != 1 || snap.Series[0].Outcome != "client_cancelled" || snap.Series[0].ResponseBytes != uint64(len("data: first\n\n")) {
				t.Fatal("cancelled stream lost actual observations", snap)
			}
		})
	}
}
