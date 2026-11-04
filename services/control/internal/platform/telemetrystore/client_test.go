package telemetrystore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var scope = Scope{TenantID: "t1", WorkspaceID: "w1", NodeID: "n1", Component: "node_exporter"}

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, e := NewClient(server.URL, 2*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func TestNewClientRefusesBadAddresses(t *testing.T) {
	for _, address := range []string{"", "ftp://x", "http://", "http://x/path", "http://u:p@x", "http://x?y=1", "https://x"} {
		if _, e := NewClient(address, time.Second); e == nil {
			t.Errorf("accepted %q", address)
		}
	}
	if _, e := NewClient("http://172.31.254.4:9428", 0); e == nil {
		t.Error("accepted a zero timeout")
	}
}

func TestInsertSendsOneScopedLine(t *testing.T) {
	var got wireRecord
	var path, fields string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		path, fields = r.URL.Path, r.URL.Query().Get("_stream_fields")
		raw, _ := io.ReadAll(r.Body)
		if e := json.Unmarshal(raw, &got); e != nil {
			t.Error(e)
		}
	})
	when := time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
	if e := c.Insert(context.Background(), Record{Scope: scope, Time: when, TaskID: "task", Body: json.RawMessage(`{"a":1}`)}); e != nil {
		t.Fatal(e)
	}
	if path != insertPath || fields != streamFields || got.Message != `{"a":1}` || got.TenantID != "t1" || got.TaskID != "task" || !got.Time.Equal(when) {
		t.Fatalf("%s %s %+v", path, fields, got)
	}
}

func TestInsertRefusesAnIncompleteRecord(t *testing.T) {
	c := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("incomplete record reached the store") })
	for _, r := range []Record{{Time: time.Now()}, {Scope: scope}} {
		if e := c.Insert(context.Background(), r); e == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
}

func TestRecentReturnsRecordsInOrderAndMarksTombstones(t *testing.T) {
	line := func(message, task, extra string) string {
		return `{"_time":"2026-11-04T08:00:00Z","_msg":"` + message + `","tenant_id":"t1","workspace_id":"w1","node_id":"n1","component":"node_exporter","task_id":"` + task + `"` + extra + "}\n"
	}
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, line("removed", "newest", `,"removed":"true"`)+line(`{\"a\":1}`, "older", ""))
	})
	got, e := c.Recent(context.Background(), scope, 4)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 2 || !got[0].Removed || got[0].Body != nil || got[0].TaskID != "newest" || got[1].Removed || string(got[1].Body) != `{"a":1}` {
		t.Fatalf("got %+v", got)
	}
	empty := testClient(t, func(http.ResponseWriter, *http.Request) {})
	if got, e = empty.Recent(context.Background(), scope, 4); e != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, e)
	}
}

func TestRecentRefusesARecordFromAnotherTenant(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"_time":"2026-11-04T08:00:00Z","_msg":"{}","tenant_id":"other","workspace_id":"w1","node_id":"n1","component":"node_exporter"}`+"\n")
	})
	if got, e := c.Recent(context.Background(), scope, 1); e == nil || got != nil {
		t.Fatalf("got %+v, %v", got, e)
	}
}

func TestRecentQuotesEveryScopeValue(t *testing.T) {
	var query string
	c := testClient(t, func(_ http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		query = r.Form.Get("query")
	})
	hostile := Scope{TenantID: `t",tenant_id="other`, WorkspaceID: "w1", NodeID: "n1", Component: "c"}
	if _, e := c.Recent(context.Background(), hostile, 1); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(query, `tenant_id="t\",tenant_id=\"other"`) {
		t.Fatalf("value was not quoted: %s", query)
	}
	if _, e := c.Recent(context.Background(), Scope{TenantID: "t1"}, 1); e == nil {
		t.Fatal("an incomplete scope reached the store")
	}
	if _, e := c.Recent(context.Background(), scope, 0); e == nil {
		t.Fatal("a zero limit reached the store")
	}
}

func TestStoreFailureIsUnavailable(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	if _, e := c.Recent(context.Background(), scope, 1); !errors.Is(e, ErrUnavailable) {
		t.Fatalf("got %v", e)
	}
	stopped, _ := NewClient("http://127.0.0.1:1", time.Second)
	if e := stopped.Insert(context.Background(), Record{Scope: scope, Time: time.Now()}); !errors.Is(e, ErrUnavailable) {
		t.Fatalf("got %v", e)
	}
}

func TestRecentDoesNotFollowRedirects(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "http://192.0.2.1/", http.StatusFound)
	})
	if _, e := c.Recent(context.Background(), scope, 1); !errors.Is(e, ErrUnavailable) {
		t.Fatalf("got %v", e)
	}
}

// fakeStore fails its first failures calls and records the rest.
type fakeStore struct {
	mu       sync.Mutex
	failures int
	calls    int
	stored   []Record
}

func (f *fakeStore) Insert(ctx context.Context, r Record) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return ErrUnavailable
	}
	f.stored = append(f.stored, r)
	return nil
}

func (f *fakeStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stored)
}

func quietLogger(buffer *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buffer, nil))
}

func TestWriterRetriesThenDelivers(t *testing.T) {
	store := &fakeStore{failures: 2}
	var log bytes.Buffer
	w := NewWriter(store, 4, quietLogger(&log))
	w.delay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx)
	w.Enqueue(Record{Scope: scope, Time: time.Now()})
	deadline := time.Now().Add(5 * time.Second)
	for store.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	w.Wait()
	if store.count() != 1 || w.Failed() != 0 {
		t.Fatalf("stored %d, failed %d", store.count(), w.Failed())
	}
}

func TestWriterGivesUpAfterBoundedAttempts(t *testing.T) {
	store := &fakeStore{failures: 100}
	var log bytes.Buffer
	w := NewWriter(store, 4, quietLogger(&log))
	w.delay = time.Millisecond
	w.deliver(context.Background(), Record{Scope: scope, Time: time.Now()})
	if store.calls != writeAttempts || w.Failed() != 1 || !strings.Contains(log.String(), "telemetry_write_failed") {
		t.Fatalf("calls %d failed %d log %s", store.calls, w.Failed(), log.String())
	}
}

func TestEnqueueNeverBlocksAndCountsDrops(t *testing.T) {
	var log bytes.Buffer
	w := NewWriter(&fakeStore{}, 2, quietLogger(&log))
	finished := make(chan struct{})
	go func() {
		for range 5 {
			w.Enqueue(Record{Scope: scope, Time: time.Now()})
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue blocked with nothing draining the queue")
	}
	if w.Dropped() != 3 || !strings.Contains(log.String(), "telemetry_queue_full") {
		t.Fatalf("dropped %d log %s", w.Dropped(), log.String())
	}
}

func TestRunDrainsQueuedRecordsOnShutdown(t *testing.T) {
	store := &fakeStore{}
	var log bytes.Buffer
	w := NewWriter(store, 8, quietLogger(&log))
	for range 5 {
		w.Enqueue(Record{Scope: scope, Time: time.Now()})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Run(ctx)
	if store.count() != 5 {
		t.Fatalf("drained %d of 5", store.count())
	}
}
