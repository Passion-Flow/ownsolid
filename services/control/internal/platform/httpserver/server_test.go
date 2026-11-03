package httpserver

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestActualTLSServeCancelsRequestAndJoinsIndependentCleanupBeforeReturning(t *testing.T) {
	certificateFixture := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	tlsConfiguration := certificateFixture.TLS.Clone()
	certificateFixture.Close()
	reserve, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := reserve.Addr().String()
	reserve.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	releaseCleanup := make(chan struct{})
	cleaned := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- Serve(ctx, address, tlsConfiguration, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-r.Context().Done()
			close(cancelled)
			<-releaseCleanup
			close(cleaned)
		}))
	}()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	// Bounded startup discovery only. The request/cleanup ordering itself uses
	// barriers, not a timing assumption or a production retry.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, e := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if e == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TLS server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	requestDone := make(chan struct{})
	go func() {
		response, _ := client.Get("https://" + address)
		if response != nil {
			response.Body.Close()
		}
		close(requestDone)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("request not admitted")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("base request context not cancelled")
	}
	select {
	case <-finished:
		t.Fatal("serve returned before independent cleanup")
	default:
	}
	close(releaseCleanup)
	select {
	case e := <-finished:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cleanup join did not finish")
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("cleanup incomplete")
	}
	<-requestDone
}
