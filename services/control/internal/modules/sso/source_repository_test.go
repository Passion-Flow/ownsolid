package sso

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestSourceRepositoryCanonicalAuthorityInputs(t *testing.T) {
	user := uuid.NewString()
	source := identity.SessionSource{TenantID: uuid.NewString(), ConnectorID: uuid.NewString(), BindingID: uuid.NewString(), FlowID: uuid.NewString(), AuthVersion: 1}
	if !sourceValid(source, user) {
		t.Fatal("canonical local source refused")
	}
	for _, change := range []func(*identity.SessionSource){
		func(s *identity.SessionSource) { s.TenantID = "" },
		func(s *identity.SessionSource) {
			s.ConnectorID = strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
		},
		func(s *identity.SessionSource) { s.BindingID = uuid.Nil.String() },
		func(s *identity.SessionSource) { s.FlowID = "other-customer-value" },
		func(s *identity.SessionSource) { s.AuthVersion = 0 },
		func(s *identity.SessionSource) { s.AuthVersion = identity.MaximumAuthVersion + 1 },
	} {
		bad := source
		change(&bad)
		if sourceValid(bad, user) {
			t.Fatal("invalid source accepted")
		}
	}
	source.AuthVersion = identity.MaximumAuthVersion
	if !sourceValid(source, user) || sourceValid(source, uuid.Nil.String()) || sourceValid(source, "other-customer-value") {
		t.Fatal("authority version/user domain mismatch")
	}
}

func TestSourceRepositoryDisabledRecoveryUsesNoStorageOrAssets(t *testing.T) {
	accounts := &identity.Service{}
	accounts.Config.SSO.KeyringFile = filepath.Join(t.TempDir(), "missing-keyring.json")
	s, e := New(accounts)
	if e != nil || s.enabled {
		t.Fatal("disabled mode opened missing assets/storage", e)
	}
	defer s.Close()
	source := identity.SessionSource{TenantID: uuid.NewString(), ConnectorID: uuid.NewString(), BindingID: uuid.NewString(), FlowID: uuid.NewString(), AuthVersion: 1}
	if e = s.CheckSessionSource(context.Background(), nil, source, uuid.NewString()); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatal("disabled read source was accepted", e)
	}
	if e = s.CheckSessionSourceWithin(context.Background(), nil, source, uuid.NewString()); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatal("disabled transaction source was accepted", e)
	}
	if _, e = New(nil); e == nil || e.Error() != "SSO_CONFIGURATION_REQUIRED" {
		t.Fatal("nil component was accepted", e)
	}
	if _, e = accounts.Repository(); e == nil || e.Error() != "IDENTITY_STORAGE_REQUIRED" {
		t.Fatal("missing store was accepted", e)
	}
}
