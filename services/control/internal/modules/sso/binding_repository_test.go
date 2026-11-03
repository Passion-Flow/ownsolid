package sso

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func unitBindingActor() identity.Auth {
	a := identity.Auth{SessionID: uuid.NewString(), TokenHash: make([]byte, 32), Epoch: 1}
	a.Me.User.ID, a.Me.Audience = uuid.NewString(), "portal"
	return a
}

func TestBindingRepositoryInputsAndDisabledRecoveryBeforeStorage(t *testing.T) {
	// Explicit no-storage unit object:it cannot return a successful mutation or
	// inventory. Disabled recovery must need neither keys nor a provider socket.
	s := &Service{Identity: &identity.Service{}, policy: config.DefaultSSOPolicy()}
	a := unitBindingActor()
	id, key, request := uuid.NewString(), uuid.NewString(), uuid.NewString()
	input := UnlinkBinding{Revision: 1, Password: "synthetic-unit-only-password", Reason: "Synthetic local recovery"}
	for _, change := range []func(*UnlinkBinding){func(v *UnlinkBinding) { v.Revision = 0 }, func(v *UnlinkBinding) { v.Revision = identity.MaximumAuthVersion }, func(v *UnlinkBinding) { v.Reason = "\x00" }, func(v *UnlinkBinding) { v.Reason = " " }} {
		bad := input
		change(&bad)
		if out, e := s.Unlink(context.Background(), a, id, bad, key, request); e == nil || e.Error() != "VALIDATION_FAILED" || out != (Binding{}) {
			t.Fatal("invalid unlink reached password/storage or returned data", e)
		}
	}
	for _, identifiers := range [][3]string{{uuid.Nil.String(), key, request}, {id, "", request}, {id, key, uuid.Nil.String()}} {
		if out, e := s.Unlink(context.Background(), a, identifiers[0], input, identifiers[1], identifiers[2]); e == nil || e.Error() != "VALIDATION_FAILED" || out != (Binding{}) {
			t.Fatal("invalid unlink identity reached password/storage", e)
		}
	}
	bad := a
	bad.TokenHash = nil
	if out, e := s.Unlink(context.Background(), bad, id, input, key, request); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || out != (Binding{}) {
		t.Fatal("invalid current actor reached password/storage", e)
	}
	if out, e := s.Unlink(context.Background(), a, id, input, key, request); e == nil || e.Error() != "IDENTITY_STORAGE_REQUIRED" || out != (Binding{}) {
		t.Fatal("disabled local unlink must require storage but no keyring", e)
	}
	if items, e := s.ListBindings(context.Background(), a); e == nil || e.Error() != "IDENTITY_STORAGE_REQUIRED" || items == nil || len(items) != 0 {
		t.Fatal("disabled local inventory must fail empty without private assets", e)
	}
	var missing *Service
	if items, e := missing.ListBindings(context.Background(), a); e == nil || e.Error() != "SSO_CONFIGURATION_REQUIRED" || items == nil || len(items) != 0 {
		t.Fatal("missing service must fail with empty inventory", e)
	}
}

func TestBindingIntentStrictLegacySnapshotCompatibility(t *testing.T) {
	stamp := time.Date(2026, 10, 19, 1, 2, 3, 123456000, time.UTC)
	value := Binding{ID: uuid.NewString(), ConnectorID: uuid.NewString(), Name: "Legacy binding \u4e16\u754c", Provider: ProviderGitea, Subject: "9223372036854775807", Revision: 2, CreatedAt: stamp, UpdatedAt: stamp.Add(time.Second)}
	raw, _ := json.Marshal(value)
	if out, e := decodeBindingIntent(raw, value.ID); e != nil || out != value {
		t.Fatal("legacy accepted unlink snapshot must remain readable", e)
	}
	legacy := value
	legacy.CreatedAt = legacy.CreatedAt.In(time.FixedZone("Synthetic historical offset", 8*60*60))
	legacy.UpdatedAt = legacy.UpdatedAt.In(time.FixedZone("Synthetic historical offset", -7*60*60))
	encoded, _ := json.Marshal(legacy)
	if out, e := decodeBindingIntent(encoded, value.ID); e != nil || out != value {
		t.Fatal("historical snapshot offsets lost their UTC instants", e)
	}
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	reordered, _ := json.Marshal(fields)
	if out, e := decodeBindingIntent(reordered, value.ID); e != nil || out != value {
		t.Fatal("database JSON key ordering changed accepted snapshot", e)
	}
	for _, change := range []func(map[string]any){
		func(v map[string]any) { v["password"] = "sensitive-unit-material" },
		func(v map[string]any) { delete(v, "subject") },
		func(v map[string]any) { v["id"] = uuid.NewString() },
		func(v map[string]any) { v["connector_id"] = uuid.Nil.String() },
		func(v map[string]any) { v["Name"] = v["name"]; delete(v, "name") },
		func(v map[string]any) { v["active"] = true },
		func(v map[string]any) { v["active"] = "false" },
		func(v map[string]any) { v["subject"] = "0" },
		func(v map[string]any) { v["subject"] = "01" },
		func(v map[string]any) { v["subject"] = "9223372036854775808" },
		func(v map[string]any) { v["subject"] = 1 },
		func(v map[string]any) { v["revision"] = 0 },
		func(v map[string]any) { v["provider"] = "unknown" },
		func(v map[string]any) { v["name"] = "\x00" },
		func(v map[string]any) { v["created_at"] = "0999-01-01T00:00:00Z" },
		func(v map[string]any) { v["updated_at"] = "2026-10-19T01:02:03.123456001Z" },
		func(v map[string]any) { v["updated_at"] = stamp.Add(-time.Second).Format(time.RFC3339Nano) },
		func(v map[string]any) { v["updated_at"] = nil },
	} {
		var bad map[string]any
		json.Unmarshal(raw, &bad)
		change(bad)
		encoded, _ := json.Marshal(bad)
		if out, e := decodeBindingIntent(encoded, value.ID); e == nil || e.Error() != "STORAGE_UNAVAILABLE" || out != (Binding{}) || strings.Contains(e.Error(), "sensitive") {
			t.Fatal("invalid unlink snapshot accepted or exposed private bytes", e)
		}
	}
	duplicate := strings.Replace(string(raw), `"id":`, `"id":"`+value.ID+`","id":`, 1)
	for _, bad := range [][]byte{[]byte(duplicate), []byte("{}"), []byte(strings.Repeat("x", maximumBindingIntentBytes+1)), {0xff}} {
		if out, e := decodeBindingIntent(bad, value.ID); e == nil || out != (Binding{}) {
			t.Fatal("invalid bounded unlink snapshot returned data")
		}
	}
}

func TestSSOActorBoundaryRejectsMalformedCurrentAuthority(t *testing.T) {
	for _, audience := range []string{"portal", "admin"} {
		a := unitBindingActor()
		a.Me.Audience = audience
		if !validSSOActor(a) {
			t.Fatal("valid current actor shape rejected")
		}
		for _, change := range []func(*identity.Auth){func(v *identity.Auth) { v.Me.User.ID = uuid.Nil.String() }, func(v *identity.Auth) { v.SessionID = "invalid" }, func(v *identity.Auth) { v.TokenHash = make([]byte, 31) }, func(v *identity.Auth) { v.Epoch = 0 }, func(v *identity.Auth) { v.Me.Audience = "other" }} {
			bad := a
			change(&bad)
			if validSSOActor(bad) {
				t.Fatal("malformed actor accepted at shared SSO frame")
			}
		}
	}
}
