package taskbus

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func syntheticEnvelope() Envelope {
	now := time.Date(2026, 10, 22, 0, 0, 1, 123456000, time.UTC)
	return Envelope{DeliveryID: uuid.NewString(), Event: Event{Schema: EventSchema, ID: uuid.NewString(), SiteID: "default", CellID: "default", NamespaceID: uuid.NewString(), TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), NodeID: uuid.NewString(), TaskID: uuid.NewString(), Kind: NodeTaskQueued, Operation: wire.CollectFacts, TaskCreatedAt: now.Add(-time.Second), CreatedAt: now}}
}

func TestTaskEventCanonicalEnvelopeScopeAndStableLogicalDigest(t *testing.T) {
	e := syntheticEnvelope()
	raw, err := e.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeEnvelope(raw)
	if err != nil || got != e {
		t.Fatal("canonical event did not round trip", err)
	}
	subject, err := e.Subject()
	if err != nil || !e.MatchesSubject(subject) {
		t.Fatal("exact subject refused", err)
	}
	want := "projectone.tasks.v1.default.default." + e.Event.NamespaceID + "." + e.Event.TenantID + "." + e.Event.WorkspaceID + "." + e.Event.NodeID + ".queued." + e.Event.ID + "." + e.DeliveryID
	if subject != want {
		t.Fatal("wire subject contract changed")
	}
	filter, err := FilterSubject(e.Event.NamespaceID, e.Event.TenantID)
	if err != nil || filter != "projectone.tasks.v1.default.default."+e.Event.NamespaceID+"."+e.Event.TenantID+".*.*.queued.*.*" {
		t.Fatal("namespace filter changed", err)
	}
	original, err := e.Event.Digest()
	if err != nil {
		t.Fatal(err)
	}
	e.DeliveryID = uuid.NewString()
	changedRaw, err := e.Canonical()
	if err != nil || bytes.Equal(raw, changedRaw) {
		t.Fatal("delivery generation not bound", err)
	}
	next, err := e.Event.Digest()
	if err != nil || original != next {
		t.Fatal("transport retry altered logical identity", err)
	}
	for _, test := range []struct {
		mutate         func(*Event)
		subjectChanges bool
	}{
		{func(v *Event) { v.NamespaceID = uuid.NewString() }, true},
		{func(v *Event) { v.TenantID = uuid.NewString() }, true},
		{func(v *Event) { v.WorkspaceID = uuid.NewString() }, true},
		{func(v *Event) { v.NodeID = uuid.NewString() }, true},
		{func(v *Event) { v.ID = uuid.NewString() }, true},
		{func(v *Event) { v.TaskID = uuid.NewString() }, false},
		{func(v *Event) { v.Operation = wire.ComponentOperation("restart") }, false},
	} {
		changed := e
		test.mutate(&changed.Event)
		digest, err := changed.Event.Digest()
		if err != nil || digest == original {
			t.Fatal("source identity change escaped logical digest", err)
		}
		if test.subjectChanges && changed.MatchesSubject(mustSubject(t, e)) {
			t.Fatal("subject scope change escaped correlation")
		}
	}
}

func TestTaskEventRejectsDuplicateUnknownNoncanonicalAndAuthorityFields(t *testing.T) {
	e := syntheticEnvelope()
	raw, _ := e.Canonical()
	cases := [][]byte{
		append([]byte(" "), raw...), append(append([]byte{}, raw...), []byte("{}")...),
		bytes.Replace(raw, []byte(`"delivery_id":`), []byte(`"delivery_id":"`+e.DeliveryID+`","delivery_id":`), 1),
		bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"lease_token":"synthetic-forbidden-token"`), 1),
		bytes.Replace(raw, []byte(`"schema":1`), []byte(`"Schema":1`), 1),
		bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":true`), 1),
		bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":null`), 1),
		bytes.Replace(raw, []byte("Z\""), []byte("+00:00\""), 1),
		bytes.Repeat([]byte("x"), MaximumEnvelopeBytes+1), nil,
	}
	for _, raw := range cases {
		out, err := DecodeEnvelope(raw)
		if err == nil || out != (Envelope{}) {
			t.Fatal("invalid envelope exposed projection")
		}
	}
	foreign := e
	foreign.Event.WorkspaceID = uuid.NewString()
	foreignRaw, _ := foreign.Canonical()
	if _, err := DecodeEnvelope(foreignRaw); err != nil {
		t.Fatal("valid structure unexpectedly authorizes scope itself", err)
	}
	if foreign.MatchesSubject(mustSubject(t, e)) {
		t.Fatal("foreign Workspace subject accepted")
	}
}

func mustSubject(t *testing.T, e Envelope) string {
	t.Helper()
	value, err := e.Subject()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestTaskEventUsesOnlyExistingInitialCatalogAndPreciseTimes(t *testing.T) {
	for _, operation := range wire.Capabilities(wire.SystemdMaintenanceProfile) {
		if !InitialOperation(operation) {
			t.Fatalf("real maintenance capability refused: %s", operation)
		}
	}
	for _, bad := range []string{"shell.v1", wire.TrainingRunOperation, wire.ModelPreparationRunOperation, wire.InferenceOperation("deploy"), "node_exporter.unknown.v1"} {
		if InitialOperation(bad) {
			t.Fatal("unsupported event family admitted", bad)
		}
	}
	for _, mutate := range []func(*Envelope){
		func(v *Envelope) { v.DeliveryID = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA" },
		func(v *Envelope) { v.Event.ID = uuid.Nil.String() }, func(v *Envelope) { v.Event.TenantID = "*" },
		func(v *Envelope) { v.Event.Kind = "node.task.execute.v1" }, func(v *Envelope) { v.Event.Schema = 2 },
		func(v *Envelope) { v.Event.CreatedAt = v.Event.TaskCreatedAt.Add(-time.Microsecond) },
		func(v *Envelope) { v.Event.CreatedAt = v.Event.CreatedAt.Add(time.Nanosecond) },
		func(v *Envelope) { v.Event.TaskCreatedAt = v.Event.TaskCreatedAt.In(time.FixedZone("test", 3600)) },
	} {
		v := syntheticEnvelope()
		mutate(&v)
		if raw, err := v.Canonical(); err == nil || raw != nil {
			t.Fatal("invalid contract produced bytes")
		}
	}
	// A valid parsed JSON value must not be mistaken for canonical transport data.
	raw, _ := json.Marshal(map[string]any{"delivery_id": uuid.NewString(), "event": nil})
	if _, err := DecodeEnvelope(raw); err == nil {
		t.Fatal("null event accepted")
	}
}
