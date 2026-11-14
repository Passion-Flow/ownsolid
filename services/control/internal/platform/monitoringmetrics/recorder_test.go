package monitoringmetrics

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type basicWriter struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (w *basicWriter) Header() http.Header         { return w.header }
func (w *basicWriter) WriteHeader(n int)           { w.code = n }
func (w *basicWriter) Write(p []byte) (int, error) { return w.body.Write(p) }

type allWriter struct {
	basicWriter
	fail                   error
	reads, writes, flushes int
	deadline               time.Time
}

func (w *allWriter) Flush()                                       { w.flushes++ }
func (w *allWriter) FlushError() error                            { w.flushes++; return w.fail }
func (w *allWriter) CloseNotify() <-chan bool                     { return nil }
func (w *allWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) { return nil, nil, w.fail }
func (w *allWriter) ReadFrom(r io.Reader) (int64, error)          { w.reads++; return w.body.ReadFrom(r) }
func (w *allWriter) WriteString(s string) (int, error)            { w.writes++; return w.body.WriteString(s) }
func (w *allWriter) SetReadDeadline(t time.Time) error            { w.deadline = t; return w.fail }
func (w *allWriter) SetWriteDeadline(t time.Time) error           { w.deadline = t; return w.fail }
func (w *allWriter) EnableFullDuplex() error                      { return w.fail }
func (w *allWriter) Push(s string, p *http.PushOptions) error     { return w.fail }

var optionalTypes = []reflect.Type{
	reflect.TypeOf((*http.Flusher)(nil)).Elem(), reflect.TypeOf((*interface{ FlushError() error })(nil)).Elem(), reflect.TypeOf((*http.CloseNotifier)(nil)).Elem(), reflect.TypeOf((*http.Hijacker)(nil)).Elem(), reflect.TypeOf((*io.ReaderFrom)(nil)).Elem(), reflect.TypeOf((*interface {
		SetReadDeadline(time.Time) error
		SetWriteDeadline(time.Time) error
	})(nil)).Elem(), reflect.TypeOf((*interface{ EnableFullDuplex() error })(nil)).Elem(), reflect.TypeOf((*http.Pusher)(nil)).Elem(), reflect.TypeOf((*io.StringWriter)(nil)).Elem(),
}

func TestTransparentOptionalInterfacesAndController(t *testing.T) {
	sentinel := errors.New("original transport failure")
	for _, under := range []http.ResponseWriter{&basicWriter{header: http.Header{}}, &allWriter{basicWriter: basicWriter{header: http.Header{}}, fail: sentinel}} {
		r, _ := New(Defaults(), nil)
		r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for _, kind := range optionalTypes {
				if reflect.TypeOf(w).Implements(kind) != reflect.TypeOf(under).Implements(kind) {
					t.Fatalf("interface changed: %v", kind)
				}
			}
			u, ok := w.(interface{ Unwrap() http.ResponseWriter })
			if !ok || u.Unwrap() != under {
				t.Fatal("unwrap identity")
			}
			if _, ok := under.(*allWriter); ok {
				c := http.NewResponseController(w)
				stamp := time.Now().Add(time.Second)
				if c.SetReadDeadline(stamp) != sentinel || c.SetWriteDeadline(stamp) != sentinel || c.EnableFullDuplex() != sentinel || c.Flush() != sentinel {
					t.Fatal("controller return changed")
				}
				if _, _, e := c.Hijack(); e != sentinel {
					t.Fatal("hijack error changed")
				}
				if w.(http.Pusher).Push("/fixed", nil) != sentinel {
					t.Fatal("push changed")
				}
			}
		})).ServeHTTP(under, httptest.NewRequest("GET", "https://example.invalid/api/v1/admin/monitoring?secret=must-not-appear", nil))
		snap := r.Snapshot()
		if snap.Inflight != 0 || len(snap.Series) != 1 {
			t.Fatal(snap)
		}
		if _, ok := under.(*allWriter); ok && snap.Series[0].Outcome != "hijack_error" {
			t.Fatal(snap)
		}
	}
}
func TestReaderFromStringWriterCountOnceAndSSE(t *testing.T) {
	r, _ := New(Defaults(), nil)
	w := &allWriter{basicWriter: basicWriter{header: http.Header{}}}
	r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n, e := io.WriteString(w, "abc"); n != 3 || e != nil {
			t.Fatal(n, e)
		}
		if n, e := w.(io.ReaderFrom).ReadFrom(strings.NewReader("defgh")); n != 5 || e != nil {
			t.Fatal(n, e)
		}
		w.(http.Flusher).Flush()
	})).ServeHTTP(w, httptest.NewRequest("GET", "https://example.invalid/api/v1/workspaces/private-id/events", nil))
	s := r.Snapshot().Series[0]
	if s.ResponseBytes != 8 || s.Count != 1 || w.body.String() != "abcdefgh" || w.reads != 1 || w.writes != 1 || w.flushes != 1 {
		t.Fatal(s, w)
	}
	var count uint64
	for _, n := range s.Buckets {
		count += n
	}
	if count != 1 {
		t.Fatal("histogram double count")
	}
}
func TestRealHTTP2AndSSETransport(t *testing.T) {
	r, _ := New(Defaults(), nil)
	server := httptest.NewUnstartedServer(r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.ProtoMajor != 2 {
			t.Error("expected actual HTTP2")
		}
		if _, ok := w.(http.Hijacker); ok {
			t.Error("HTTP2 falsely hijackable")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		if e := http.NewResponseController(w).Flush(); e != nil {
			t.Error(e)
		}
		io.WriteString(w, "data: second\n\n")
	})))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	resp, e := server.Client().Get(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	body, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || resp.ProtoMajor != 2 || string(body) != "data: first\n\ndata: second\n\n" {
		t.Fatal(resp, e, string(body))
	}
	if s := r.Snapshot().Series; s[0].ResponseBytes != uint64(len(body)) {
		t.Fatal(s)
	}
}
func TestPanicAndRedactedBoundedLogs(t *testing.T) {
	var output bytes.Buffer
	p := Defaults()
	p.StructuredLogging = true
	p.MaximumLogsPerSecond = 1
	r, _ := New(p, slog.New(slog.NewJSONHandler(&output, nil)))
	handler := r.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private body") }))
	func() {
		defer func() {
			if recover() != "private body" {
				t.Fatal("panic changed")
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://example.invalid/private?credential=secret", nil))
	}()
	r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://example.invalid/another-secret", nil))
	if strings.Contains(output.String(), "secret") || strings.Contains(output.String(), "private body") || r.Snapshot().SkippedLogs != 1 {
		t.Fatal(output.String(), r.Snapshot())
	}
	if r.Snapshot().Inflight != 0 {
		t.Fatal("leaked inflight")
	}
}
func TestPolicyFalseInvalidAndCounterOverflow(t *testing.T) {
	p := Defaults()
	p.StructuredLogging = false
	if _, e := New(p, nil); e != nil {
		t.Fatal(e)
	}
	p.LatencyMilliseconds = []float64{2, 1}
	if p.Validate() == nil {
		t.Fatal("unordered accepted")
	}
	flag := false
	if add(MaximumExactCounter, 1, &flag) != MaximumExactCounter || !flag {
		t.Fatal("overflow")
	}
	for _, v := range Runtime().Values {
		if v.Available && (v.Value == nil || *v.Value > MaximumExactCounter) {
			t.Fatal(v)
		}
	}
}
