package scope

import (
	"context"
	"strings"
	"testing"
)

func TestVolumeJournalRejectsForgedTransitionsAndContext(t *testing.T) {
	c, client := storageFixture(t)
	if _, e := initializeStorage(context.Background(), c, client, false); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	index := -1
	for i, event := range l.events {
		if event.Action == "volume_prepare" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("missing real journal effects")
	}
	// Prefixes are the actual protected journal written by the implementation.
	cases := []struct {
		name   string
		offset int
		mutate func(*Event)
	}{
		{"fresh-skipped-phase", 0, func(e *Event) { e.Volume.Phase = phaseVolumeCreated }},
		{"unknown-role", 0, func(e *Event) { e.Volume.Key = "foreign" }},
		{"wrong-descriptor", 0, func(e *Event) { e.Volume.DescriptorSHA256 = strings.Repeat("a", 64) }},
		{"wrong-image", 0, func(e *Event) { e.Volume.ImageID = "sha256:" + strings.Repeat("b", 64) }},
		{"prior-operation-reuse", 0, func(e *Event) { e.OperationID = l.events[0].OperationID }},
		{"early-container-id", 0, func(e *Event) { e.Volume.UtilityID = strings.Repeat("d", 64) }},
		{"phase-jump", 1, func(e *Event) { e.Volume.Phase = phaseUtilityCreated; e.Volume.UtilityID = strings.Repeat("d", 64) }},
		{"changed-generation", 1, func(e *Event) { e.Volume.ResourceOperationID = strings.Repeat("a", 32) }},
		{"changed-uid", 1, func(e *Event) { e.Volume.UID++ }},
		{"missing-container-id", 2, func(e *Event) { e.Volume.UtilityID = "" }},
		{"changed-container-id", 3, func(e *Event) { e.Volume.UtilityID = strings.Repeat("e", 64) }},
		{"false-terminal", 3, func(e *Event) { e.Outcome = "completed"; e.Volume.Phase = phasePrepared }},
		{"uncertain-advance", 3, func(e *Event) { e.Outcome = "uncertain"; e.Code = "TEST_UNKNOWN" }},
		{"foreign-reference", 3, func(e *Event) {
			e.Action = "volume_reconcile"
			e.Outcome = "completed"
			e.OperationID = strings.Repeat("f", 32)
			e.ReferenceOperationID = strings.Repeat("a", 32)
		}},
		{"reconcile-operation-reuse", 3, func(e *Event) {
			e.Action = "volume_reconcile"
			e.Outcome = "completed"
			e.ReferenceOperationID = e.OperationID
		}},
		{"reconcile-backwards", 4, func(e *Event) {
			e.Action = "volume_reconcile"
			e.Outcome = "completed"
			e.ReferenceOperationID = e.OperationID
			e.OperationID = strings.Repeat("f", 32)
			e.Volume.Phase = phaseUtilityCreated
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			event := l.events[index+test.offset]
			record := *event.Volume
			event.Volume = &record
			test.mutate(&event)
			if e := validateImageEvent(l.events[:index+test.offset], event); e == nil {
				t.Fatal("forged transition accepted")
			}
		})
	}
	final := l.events[len(l.events)-1]
	final.Outcome = "started"
	final.OperationID = strings.Repeat("f", 32)
	if validateVolumeEvent(l.events, final) == nil {
		t.Fatal("prepared role restarted")
	}
}
func TestStorageCapacityAndMissingPreparedVolumeFailWithoutEffects(t *testing.T) {
	t.Run("capacity", func(t *testing.T) {
		c, client := storageFixture(t)
		// Capacity is scope configuration, so exercise the admission helper through
		// the existing owner config rather than changing its immutable fingerprint.
		l, e := open(c, true)
		if e != nil {
			t.Fatal(e)
		}
		defer l.close()
		input, _, _ := inputsState(l.events)
		p, e := l.readInputs(context.Background(), *input)
		if e != nil {
			t.Fatal(e)
		}
		role := p.descriptor.Roles[0]
		l.config.MaximumJournalBytes = 1
		r := VolumeRecord{Key: role.Key, DescriptorSHA256: input.DescriptorSHA256, PayloadSHA256: role.PayloadSHA256, ImageID: input.ImageID, UID: role.UID, GID: role.GID, ResourceOperationID: strings.Repeat("a", 32), Phase: phaseReserved}
		// No SDK method may run if intent validation/durability cannot succeed.
		if e = l.prepareVolume(context.Background(), client, r, nil); e == nil || e.Error() != "OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED" || client.creates != 0 {
			t.Fatal("effect before durable valid intent", e)
		}
	})
	t.Run("missing-prepared", func(t *testing.T) {
		c, client := storageFixture(t)
		if _, e := initializeStorage(context.Background(), c, client, false); e != nil {
			t.Fatal(e)
		}
		client.volume = false
		if _, e := initializeStorage(context.Background(), c, client, false); e == nil || client.creates != 1 || client.puts != 1 {
			t.Fatal("missing prepared volume silently recreated", e)
		}
	})
}
