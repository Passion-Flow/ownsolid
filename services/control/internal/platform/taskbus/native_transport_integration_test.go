//go:build task_events_native_integration

package taskbus_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/taskbusfixture"
)

func TestActualNativeTaskBusTransport(t *testing.T) {
	policy := taskbus.Configuration{ConnectSeconds: 1, RequestSeconds: 2, PullSeconds: 1, AckWaitSeconds: 8, WorkerPollSeconds: 1, RetrySeconds: 1, ReconnectSeconds: 1, MaximumReconnectAttempts: 3, MaximumPublishAttempts: 3, MaximumDeliveries: 3, MaximumAckPending: 2, StreamMaximumMessages: 100, StreamMaximumBytes: 1 << 20, StreamRetentionSeconds: 60, DuplicateWindowSeconds: 2, RetryLifetimeSeconds: 12, HistoryRetentionSeconds: 90, CleanupBatch: 8, ShutdownSeconds: 4}
	f := taskbusfixture.New(t, policy)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	var buses [2]*taskbus.JetStream
	t.Cleanup(func() {
		for _, b := range buses {
			if b != nil {
				if e := b.Close(); e != nil {
					t.Error(e)
				}
			}
		}
	})
	if !t.Run("exact_profile_mtls_nkeys", func(t *testing.T) {
		for i, p := range f.Profiles {
			b, e := taskbus.NewJetStream(ctx, p.Configuration, p.Tenant)
			if e != nil {
				t.Fatal("actual exact private profile admission", e)
			}
			buses[i] = b
			status, e := b.Inspect(ctx)
			if e != nil || status.Identity == "" || status.Messages != 0 || status.Pending != 0 || status.AckPending != 0 {
				t.Fatal("actual empty profile readback", status, e)
			}
		}
	}) {
		return
	}
	var canaries [2]taskbus.Envelope
	if !t.Run("canonical_publish_lookup_pull_double_ack", func(t *testing.T) {
		for i, p := range f.Profiles {
			now := time.Now().UTC().Truncate(time.Microsecond)
			canaries[i] = taskbus.Envelope{Event: taskbus.Event{Schema: taskbus.EventSchema, ID: uuid.NewString(), SiteID: "default", CellID: "default", NamespaceID: p.Configuration.NamespaceID, TenantID: p.Tenant, WorkspaceID: uuid.NewString(), NodeID: uuid.NewString(), TaskID: uuid.NewString(), Kind: taskbus.NodeTaskQueued, Operation: "collect_facts.v1", TaskCreatedAt: now, CreatedAt: now}, DeliveryID: uuid.NewString()}
			published, e := buses[i].Publish(ctx, canaries[i])
			if e != nil || published.Sequence != 1 || published.Identity != buses[i].Identity() {
				t.Fatal("actual canonical publish", published, e)
			}
			read, found, e := buses[i].Lookup(ctx, canaries[i])
			if e != nil || !found || read.Sequence != published.Sequence {
				t.Fatal("actual immutable readback", read, found, e)
			}
			message, e := buses[i].Next(ctx)
			if e != nil || message == nil || message.Sequence != published.Sequence {
				t.Fatal("actual pull", message, e)
			}
			raw, e := canaries[i].Canonical()
			if e != nil || string(raw) != string(message.Body) {
				t.Fatal("actual canonical pull bytes", e)
			}
			if e = message.Confirm(ctx); e != nil {
				t.Fatal("actual DoubleAck", e)
			}
			status, e := buses[i].Inspect(ctx)
			if e != nil || status.Messages != 1 || status.Pending != 0 || status.AckPending != 0 {
				t.Fatal("actual Limits retention and confirmed ACK", status, e)
			}
		}
	}) {
		return
	}
	if !t.Run("actual_role_and_peer_namespace_isolation", func(t *testing.T) {
		if e := buses[0].VerifyPeerIsolation(ctx, canaries[0], buses[1], canaries[1]); e != nil {
			t.Fatal(e)
		}
	}) {
		return
	}
	if !t.Run("actual_wrong_tls_hostname_refused", func(t *testing.T) {
		f.VerifyTLSNegatives(t)
		c := f.Profiles[0].Configuration
		u, e := url.Parse(c.Endpoint)
		if e != nil {
			t.Fatal(e)
		}
		u.Host = strings.Replace(u.Host, "localhost", "wrong-native-host.example.invalid", 1)
		c.Endpoint = u.String()
		b, e := taskbus.NewJetStream(ctx, c, f.Profiles[0].Tenant)
		if b != nil {
			b.Close()
			t.Fatal("actual invalid hostname admitted")
		}
		if e == nil || e.Error() != "TASK_BUS_UNAVAILABLE" {
			t.Fatal("actual TLS verification refusal required", e)
		}
	}) {
		return
	}
	t.Run("actual_same_store_process_restart", func(t *testing.T) {
		identities := [2]string{buses[0].Identity(), buses[1].Identity()}
		f.Restart()
		for i, b := range buses {
			if e := b.Reconnect(ctx); e != nil || b.Identity() != identities[i] {
				t.Fatal("actual restart identity", e)
			}
			read, found, e := b.Lookup(ctx, canaries[i])
			if e != nil || !found || read.Sequence != 1 {
				t.Fatal("actual persisted canary after restart", read, found, e)
			}
		}
	})
}
