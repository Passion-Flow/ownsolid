//go:build repository_integration && task_events_integration && task_events_native_integration

package taskdeliveries

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/taskbusfixture"
)

func init() { ownedNativeTaskEventProfile = actualOwnedNativeTaskEventProfile }

func actualOwnedNativeTaskEventProfile(t *testing.T) (string, func(), func(func())) {
	t.Helper()
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" {
		t.Fatal("NATIVE_TASK_EVENTS_DECLARED_POSTGRES_PROFILE_REQUIRED")
	}
	policy := taskbus.Configuration{ConnectSeconds: 1, RequestSeconds: 2, PullSeconds: 1, AckWaitSeconds: 8, WorkerPollSeconds: 1, RetrySeconds: 1, ReconnectSeconds: 1, MaximumReconnectAttempts: 3, MaximumPublishAttempts: 3, MaximumDeliveries: 3, MaximumAckPending: 2, StreamMaximumMessages: 100, StreamMaximumBytes: 1 << 20, StreamRetentionSeconds: 60, DuplicateWindowSeconds: 2, RetryLifetimeSeconds: 12, HistoryRetentionSeconds: 90, CleanupBatch: 8, ShutdownSeconds: 4}
	if strings.HasSuffix(t.Name(), "/reject_cross_event_msgid_alias") {
		policy.DuplicateWindowSeconds = 2*policy.RequestSeconds + 1
	}
	if strings.HasSuffix(t.Name(), "/exhausted_owner_requires_fresh_admission") {
		policy.RetryLifetimeSeconds = 30
		policy.HistoryRetentionSeconds = 98
	}
	fixture := taskbusfixture.New(t, policy)
	own := fixture.Profiles[0]
	entry := map[string]any{"tenant_id": own.Tenant, "configuration": own.Configuration}
	if strings.HasSuffix(t.Name(), "/private_role_namespace_isolation") {
		peer := fixture.Profiles[1]
		ctx, cancel := context.WithTimeout(t.Context(), time.Duration(peer.Configuration.ShutdownSeconds)*time.Second)
		defer cancel()
		bus, e := taskbus.NewJetStream(ctx, peer.Configuration, peer.Tenant)
		if e != nil {
			t.Fatal("actual peer admission", e)
		}
		now := time.Now().UTC().Truncate(time.Microsecond)
		canary := taskbus.Envelope{Event: taskbus.Event{Schema: taskbus.EventSchema, ID: uuid.NewString(), NamespaceID: peer.Configuration.NamespaceID, TenantID: peer.Tenant, WorkspaceID: uuid.NewString(), NodeID: uuid.NewString(), TaskID: uuid.NewString(), Kind: taskbus.NodeTaskQueued, Operation: "collect_facts.v1", TaskCreatedAt: now, CreatedAt: now}, DeliveryID: uuid.NewString()}
		_, publish := bus.Publish(ctx, canary)
		_, found, read := bus.Lookup(ctx, canary)
		closed := bus.Close()
		if publish != nil || read != nil || !found || closed != nil {
			t.Fatal("actual existing peer canary required", publish, read, closed)
		}
		entry["peer"] = map[string]any{"tenant_id": peer.Tenant, "configuration": peer.Configuration, "canary": canary}
	}
	raw, e := json.Marshal(map[string]any{"profiles": map[string]any{"postgresql/" + t.Name(): entry}})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(fixture.Root, "task-event-profile.json")
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = file.Write(raw); e != nil {
		file.Close()
		t.Fatal(e)
	}
	if e = file.Sync(); e != nil {
		file.Close()
		t.Fatal(e)
	}
	if e = file.Close(); e != nil {
		t.Fatal(e)
	}
	return path, fixture.Restart, fixture.RestartWithInterruption
}
