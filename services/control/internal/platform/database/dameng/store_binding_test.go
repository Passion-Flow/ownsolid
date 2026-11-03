//go:build cgo && odbc_manager

package dameng

import (
	"context"
	"testing"
	"time"
)

func TestStoreBindingDoesNotEquateTargetWithPrincipalOrCredential(t *testing.T) {
	c := connectorConfigurationFixture()
	s := &Store{configuration: c, manager: &ODBCManager{connection: &connectionBinding{configuration: c}}}
	s.admitted.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, mutate := range []func(*ConnectionConfiguration){
		func(c *ConnectionConfiguration) { c.User = "OTHER_USER" },
		func(c *ConnectionConfiguration) { c.PasswordFile = "/private/another-password" },
		func(c *ConnectionConfiguration) { c.Policy.MaximumRows++ },
	} {
		changed := c
		mutate(&changed)
		if err := s.VerifyConfiguration(ctx, changed); FailureCode(err) != "DAMENG_STORE_CONFIGURATION_MISMATCH" {
			t.Fatal("different admitted identity/reference/policy accepted", err)
		}
	}
	s.admitted.Store(false)
	if err := s.VerifyConfiguration(ctx, c); FailureCode(err) != "DAMENG_STORE_CONFIGURATION_MISMATCH" {
		t.Fatal("unadmitted store accepted", err)
	}
	s.admitted.Store(true)
	s.manager.closing = true
	if err := s.VerifyConfiguration(ctx, c); FailureCode(err) != "DAMENG_STORE_OWNER_CLOSING" {
		t.Fatal("closing owner accepted", err)
	}
}
