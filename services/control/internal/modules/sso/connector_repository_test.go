package sso

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestConnectorRepositoryInputsAndRecoveryBeforeStorage(t *testing.T) {
	origin := "https://provider.example.invalid"
	// Explicit no-storage/keyring unit double: a valid input must stop before
	// encryption/SQL;this object cannot perform a successful product mutation.
	s := &Service{Identity: &identity.Service{}, enabled: true, keys: &Keyring{}, policy: config.DefaultSSOPolicy(), approved: map[string]config.SSOProvider{origin: {Origin: origin}}}
	a := identity.Auth{}
	a.Me.Audience, a.Me.User.IsPlatformAdmin = "admin", true
	input := CreateConnector{ID: uuid.NewString(), Name: "Synthetic \u4e16\u754c", Provider: ProviderGitea, Origin: origin, ClientID: uuid.NewString(), ClientSecret: "synthetic-unit-only-secret", Reason: "Synthetic intent"}
	key, request := uuid.NewString(), uuid.NewString()
	for _, change := range []func(*CreateConnector){
		func(v *CreateConnector) { v.ID = uuid.Nil.String() },
		func(v *CreateConnector) { v.ClientID = "not-canonical" },
		func(v *CreateConnector) { v.Name = "\x00" },
		func(v *CreateConnector) { v.Provider = "unsupported" },
		func(v *CreateConnector) { v.ClientSecret = "bad\nsecret" },
		func(v *CreateConnector) { v.ClientSecret = strings.Repeat("x", maximumSecretBytes+1) },
		func(v *CreateConnector) { v.Reason = "\x00" },
	} {
		bad := input
		change(&bad)
		if out, e := s.CreateConnector(context.Background(), a, bad, key, request); e == nil || e.Error() != "VALIDATION_FAILED" || out != (Connector{}) {
			t.Fatal("invalid create reached storage or returned data", e)
		}
	}
	for _, ids := range [][2]string{{uuid.Nil.String(), request}, {key, ""}, {key, uuid.Nil.String()}} {
		if out, e := s.CreateConnector(context.Background(), a, input, ids[0], ids[1]); e == nil || e.Error() != "VALIDATION_FAILED" || out != (Connector{}) {
			t.Fatal("invalid intent/request identifier reached storage", e)
		}
	}
	bad := input
	bad.Origin = "https://unapproved.example.invalid"
	if _, e := s.CreateConnector(context.Background(), a, bad, key, request); e == nil || e.Error() != "SSO_PROVIDER_DESTINATION_REJECTED" {
		t.Fatal("unapproved provider reached storage", e)
	}
	update := UpdateConnector{Revision: 1, Name: "Updated \u4e16\u754c", Reason: "Synthetic update"}
	for _, change := range []func(*UpdateConnector){
		func(v *UpdateConnector) { v.Revision = 0 },
		func(v *UpdateConnector) { v.Revision = identity.MaximumAuthVersion },
		func(v *UpdateConnector) { v.ClientID = &input.ClientID },
		func(v *UpdateConnector) { v.ClientSecret = &input.ClientSecret },
		func(v *UpdateConnector) { v.Name = "\x00" },
	} {
		bad := update
		change(&bad)
		if out, e := s.UpdateConnector(context.Background(), a, input.ID, bad, key, request); e == nil || e.Error() != "VALIDATION_FAILED" || out != (Connector{}) {
			t.Fatal("invalid update reached storage or returned data", e)
		}
	}
	badActor := a
	badActor.Me.User.IsPlatformAdmin = false
	if _, e := s.CreateConnector(context.Background(), badActor, input, key, request); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal("nonAdmin create reached storage", e)
	}
	if _, e := s.UpdateConnector(context.Background(), badActor, input.ID, update, key, request); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal("nonAdmin update reached storage", e)
	}
	s.enabled, s.keys = false, nil
	if _, e := s.CreateConnector(context.Background(), a, input, key, request); e == nil || e.Error() != "SSO_DISABLED" {
		t.Fatal("disabled create opened keys/storage", e)
	}
	if out, e := s.UpdateConnector(context.Background(), a, input.ID, update, key, request); e == nil || e.Error() != "IDENTITY_STORAGE_REQUIRED" || out != (Connector{}) {
		t.Fatal("local disable recovery should need storage but no keyring", e)
	}
}

func TestConnectorIntentSnapshotStrictLegacyCompatibility(t *testing.T) {
	stamp := time.Date(2026, 10, 19, 1, 2, 3, 123456000, time.UTC)
	value := Connector{ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Name: "Legacy \u4e16\u754c", Provider: ProviderGitea, Origin: "https://provider.example.invalid", ClientID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Revision: 1, AuthVersion: 1, CreatedAt: stamp, UpdatedAt: stamp}
	raw, _ := json.Marshal(value)
	if out, e := decodeConnectorIntent(raw, value.ID); e != nil || out != value {
		t.Fatal("actual legacy DTO encoding must remain readable", e)
	}
	legacy := value
	legacy.CreatedAt = value.CreatedAt.In(time.FixedZone("Synthetic legacy offset", 8*60*60))
	legacy.UpdatedAt = value.UpdatedAt.In(time.FixedZone("Synthetic legacy offset", -7*60*60))
	encodedLegacy, _ := json.Marshal(legacy)
	if out, e := decodeConnectorIntent(encodedLegacy, value.ID); e != nil || out != value {
		t.Fatal("legacy RFC3339 offsets must preserve their instants as UTC", e)
	}
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	reordered, _ := json.Marshal(fields)
	if out, e := decodeConnectorIntent(reordered, value.ID); e != nil || out != value {
		t.Fatal("database JSON key ordering must not break replay", e)
	}
	for _, change := range []func(map[string]any){
		func(v map[string]any) { v["client_secret"] = "sensitive-unit-value" },
		func(v map[string]any) { delete(v, "id") },
		func(v map[string]any) { v["id"] = nil },
		func(v map[string]any) { v["id"] = uuid.NewString() },
		func(v map[string]any) { v["Client_ID"] = v["client_id"]; delete(v, "client_id") },
		func(v map[string]any) { v["enabled"] = "false" },
		func(v map[string]any) { v["revision"] = 0 },
		func(v map[string]any) { v["auth_version"] = identity.MaximumAuthVersion + 1 },
		func(v map[string]any) { v["name"] = "\x00" },
		func(v map[string]any) { v["origin"] = "http://provider.example.invalid" },
		func(v map[string]any) { v["created_at"] = "0999-01-01T00:00:00Z" },
		func(v map[string]any) { v["created_at"] = "2026-10-19T01:02:03.000000001Z" },
		func(v map[string]any) { v["updated_at"] = nil },
	} {
		var modified map[string]any
		json.Unmarshal(raw, &modified)
		change(modified)
		encoded, _ := json.Marshal(modified)
		if out, e := decodeConnectorIntent(encoded, value.ID); e == nil || e.Error() != "STORAGE_UNAVAILABLE" || out != (Connector{}) || strings.Contains(e.Error(), "sensitive") {
			t.Fatal("corrupt intent was accepted or exposed", e)
		}
	}
	duplicate := strings.Replace(string(raw), `"id":`, `"id":"`+value.ID+`","id":`, 1)
	for _, bad := range [][]byte{[]byte(duplicate), []byte("{}"), []byte(strings.Repeat("x", maximumConnectorIntentBytes+1)), {0xff}} {
		if out, e := decodeConnectorIntent(bad, value.ID); e == nil || out != (Connector{}) {
			t.Fatal("invalid or oversized intent returned data")
		}
	}
}

func TestConnectorSourceTargetLockOrderIsGlobalAndDeduplicated(t *testing.T) {
	a, b := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	one := connectorLocks([]connectorLock{{b, a}, {a, b}, {a, a}, {a, b}})
	two := connectorLocks([]connectorLock{{a, b}, {b, a}, {a, a}})
	if !reflect.DeepEqual(one, two) || len(one) != 3 || one[0] != (connectorLock{a, a}) || one[1] != (connectorLock{a, b}) || one[2] != (connectorLock{b, a}) {
		t.Fatal("source/target input order changed global row acquisition order")
	}
}
