//go:build telemetry_integration

package telemetrystore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// These tests run against a real log store at OWNSOLID_TEST_LOGS_ADDRESS. The store
// shows new records about a second after writing, so reads poll.
func storeClient(t *testing.T) *Client {
	t.Helper()
	address := os.Getenv("OWNSOLID_TEST_LOGS_ADDRESS")
	if address == "" {
		t.Fatal("OWNSOLID_TEST_LOGS_ADDRESS must name a log store")
	}
	c, e := NewClient(address, 5*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func uniqueScope(t *testing.T, tenant string) Scope {
	t.Helper()
	return Scope{TenantID: tenant, WorkspaceID: "w-" + t.Name(), NodeID: fmt.Sprintf("n-%d", time.Now().UnixNano()), Component: "node_exporter"}
}

func awaitRecords(t *testing.T, c *Client, s Scope, want int) []Record {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		got, e := c.Recent(context.Background(), s, 16)
		if e != nil {
			t.Fatal(e)
		}
		if len(got) >= want || time.Now().After(deadline) {
			if len(got) != want {
				t.Fatalf("%d records, want %d: %+v", len(got), want, got)
			}
			return got
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestRealStoreKeepsOrderBodiesAndTombstones(t *testing.T) {
	c := storeClient(t)
	s := uniqueScope(t, "tenant-a")
	base := time.Now().UTC().Truncate(time.Millisecond)
	body := json.RawMessage(`{"source":"systemd_journal","entries":[{"message":"quote \" backslash \\ unicode \u00e9"}]}`)
	for i, r := range []Record{
		{Scope: s, Time: base, TaskID: "first", Body: body},
		{Scope: s, Time: base.Add(time.Second), TaskID: "second", Body: body},
		{Scope: s, Time: base.Add(2 * time.Second), TaskID: "third", Removed: true},
	} {
		if e := c.Insert(context.Background(), r); e != nil {
			t.Fatal(i, e)
		}
	}
	got := awaitRecords(t, c, s, 3)
	if got[0].TaskID != "third" || !got[0].Removed || got[1].TaskID != "second" || got[2].TaskID != "first" {
		t.Fatalf("order or tombstone: %+v", got)
	}
	if string(got[1].Body) != string(body) {
		t.Fatalf("body changed:\n got %s\nwant %s", got[1].Body, body)
	}
	if !got[1].Time.Equal(base.Add(time.Second)) {
		t.Fatalf("time changed: %s", got[1].Time)
	}
	if limited, e := c.Recent(context.Background(), s, 1); e != nil || len(limited) != 1 || limited[0].TaskID != "third" {
		t.Fatalf("limit: %+v %v", limited, e)
	}
}

func TestRealStoreKeepsTenantsApart(t *testing.T) {
	c := storeClient(t)
	mine := uniqueScope(t, "tenant-a")
	other := mine
	other.TenantID = "tenant-b"
	for _, s := range []Scope{mine, other} {
		if e := c.Insert(context.Background(), Record{Scope: s, Time: time.Now(), TaskID: s.TenantID, Body: json.RawMessage(`{}`)}); e != nil {
			t.Fatal(e)
		}
	}
	got := awaitRecords(t, c, mine, 1)
	if got[0].TaskID != "tenant-a" {
		t.Fatalf("read another tenant's record: %+v", got)
	}
	// A value that tries to widen the selector must match nothing.
	hostile := mine
	hostile.TenantID = `tenant-a",tenant_id=~".*`
	if leaked, e := c.Recent(context.Background(), hostile, 16); e != nil || len(leaked) != 0 {
		t.Fatalf("hostile scope returned %+v, %v", leaked, e)
	}
}

func TestRealStoreReturnsNothingForAnUnknownScope(t *testing.T) {
	c := storeClient(t)
	if got, e := c.Recent(context.Background(), uniqueScope(t, "nobody"), 16); e != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, e)
	}
}
