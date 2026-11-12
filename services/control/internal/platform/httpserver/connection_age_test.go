package httpserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func TestDedicatedTLSAgeSendsGoAwayAndBoundsIdleTransport(t *testing.T) {
	certificate, roots := transportCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	draining := make(chan time.Time, 1)
	finish := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/initial" {
			draining <- ConnectionDrainAt(r.Context())
			w.WriteHeader(http.StatusOK)
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
			select {
			case <-finish:
				_, _ = io.WriteString(w, "complete")
			case <-r.Context().Done():
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- ServeListener(ctx, listener, &tls.Config{Certificates: []tls.Certificate{certificate},
			MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"}}, handler,
			config.DefaultHTTPPolicy(), ConnectionAge{Maximum: time.Second, Grace: time.Second})
	}()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("aged listener did not shut down")
		}
	})

	connect := func() (*tls.Conn, *http2.Framer) {
		t.Helper()
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", listener.Addr().String(),
			&tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		if state := conn.ConnectionState(); state.NegotiatedProtocol != "h2" || len(state.VerifiedChains) == 0 {
			t.Fatal("verified HTTP/2 was not negotiated")
		}
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(conn, http2.ClientPreface); err != nil {
			t.Fatal(err)
		}
		framer := http2.NewFramer(conn, conn)
		framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
		if err := framer.WriteSettings(); err != nil {
			t.Fatal(err)
		}
		return conn, framer
	}
	request := func(framer *http2.Framer, stream uint32, route string) {
		t.Helper()
		var block bytes.Buffer
		encoder := hpack.NewEncoder(&block)
		for _, field := range []hpack.HeaderField{
			{Name: ":method", Value: http.MethodGet}, {Name: ":scheme", Value: "https"},
			{Name: ":authority", Value: "localhost"}, {Name: ":path", Value: route},
		} {
			if err := encoder.WriteField(field); err != nil {
				t.Fatal(err)
			}
		}
		if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, BlockFragment: block.Bytes(), EndStream: true, EndHeaders: true}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(framer *http2.Framer, stream uint32, status string, ended bool) {
		t.Helper()
		for {
			frame, err := framer.ReadFrame()
			if err != nil {
				t.Fatal(err)
			}
			switch frame := frame.(type) {
			case *http2.SettingsFrame:
				if !frame.IsAck() {
					if err := framer.WriteSettingsAck(); err != nil {
						t.Fatal(err)
					}
				}
			case *http2.MetaHeadersFrame:
				if frame.StreamID == stream {
					if frame.PseudoValue("status") != status || frame.StreamEnded() != ended {
						t.Fatalf("unexpected status: %v", frame.Fields)
					}
					for _, field := range frame.RegularFields() {
						if field.Name == "connection" {
							t.Fatal("connection header escaped onto the HTTP/2 wire")
						}
					}
					return
				}
			case *http2.GoAwayFrame:
				t.Fatalf("GOAWAY preceded the admitted response headers: %+v", frame)
			}
		}
	}

	_, framer := connect()
	request(framer, 1, "/initial")
	read(framer, 1, "200", false)
	var drain time.Time
	select {
	case drain = <-draining:
	case <-time.After(time.Second):
		t.Fatal("listener did not report its connection age")
	}
	if drain.IsZero() {
		t.Fatal("listener did not attach its connection age")
	}
	timer := time.NewTimer(time.Until(drain))
	defer timer.Stop()
	<-timer.C
	// Send no further request. Age must notify a stream whose headers are
	// already on the wire, while keeping that stream's response intact.
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			t.Fatalf("held response received no automatic GOAWAY: %v", err)
		}
		if retirement, ok := frame.(*http2.GoAwayFrame); ok {
			if retirement.ErrCode != http2.ErrCodeNo || retirement.LastStreamID != 1 {
				t.Fatalf("GOAWAY did not retain the admitted stream: %+v", retirement)
			}
			break
		}
	}
	close(finish)
	var body bytes.Buffer
	completed := false
	for !completed {
		frame, err := framer.ReadFrame()
		if err != nil {
			t.Fatalf("admitted response was interrupted after GOAWAY: %v", err)
		}
		if data, ok := frame.(*http2.DataFrame); ok && data.StreamID == 1 {
			body.Write(data.Data())
			completed = data.StreamEnded()
		}
	}
	if body.String() != "complete" {
		t.Fatalf("admitted response body changed: %q", body.String())
	}
	_, young := connect()
	request(young, 1, "/healthy")
	read(young, 1, "204", true)

	idle, _ := connect()
	// No request asks this connection to drain. The socket limit must end
	// it before the client deadline or the normal server idle timeout.
	if _, err := io.Copy(io.Discard, idle); errors.Is(err, context.DeadlineExceeded) ||
		err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("aged idle socket did not close cleanly: %v", err)
	}
}
