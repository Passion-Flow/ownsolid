//go:build cgo && odbc_manager

package dameng

import (
	"os"
	"testing"
)

// Synthetic admitted metadata exercises the policy seam only. These tests
// allocate no native handles and do not assert a DM login or socket protocol.
func schemaAdmittedFixture() *Store {
	sc := schemaTestConfiguration()
	c := connectorConfigurationFixture()
	c.User = sc.Schema
	c.Schema = sc.Schema
	c.Policy.MaximumRows = sc.MaximumMetadataRows
	c.Policy.MaximumCellBytes = sc.MaximumMetadataBytes
	s := &Store{configuration: c}
	s.admitted.Store(true)
	return s
}
func TestDamengSchemaStoreAdmissionPolicy(t *testing.T) {
	c := schemaTestConfiguration()
	s := schemaAdmittedFixture()
	if s.ValidateSchemaUse(c, false, 8192) != nil || s.ValidateSchemaUse(c, true, 8192) == nil {
		t.Fatal("owner/runtime identity boundary")
	}
	s.configuration.User = c.RuntimeUser
	if s.ValidateSchemaUse(c, true, 8192) != nil {
		t.Fatal("runtime policy refused")
	}
	for _, mutate := range []func(*Store){
		func(s *Store) { s.admitted.Store(false) }, func(s *Store) { s.configuration.Schema = "OTHER" },
		func(s *Store) { s.configuration.Policy.MaximumRows++ }, func(s *Store) { s.configuration.Policy.MaximumCellBytes++ },
		func(s *Store) { s.configuration.Policy.MaximumSQLBytes = 1 }, func(s *Store) { s.configuration.Policy.MaximumColumns = 1 },
		func(s *Store) { s.configuration.Policy.QuerySeconds = 1 }, func(s *Store) { s.configuration.Policy.ConnectionSeconds = 1 },
	} {
		s := schemaAdmittedFixture()
		mutate(s)
		if s.ValidateSchemaUse(c, false, 8192) == nil {
			t.Fatal("schema limits bypassed")
		}
	}
	if (*Store)(nil).ValidateSchemaUse(c, false, 8192) == nil {
		t.Fatal("nil owner accepted")
	}
}
func TestDamengSchemaTargetBindingCannotCrossIdentities(t *testing.T) {
	a, b := schemaAdmittedFixture(), schemaAdmittedFixture()
	if a.SameSchemaTarget(b) || (*Store)(nil).SameSchemaTarget(a) {
		t.Fatal("unbound identity accepted")
	}
	info, e := os.Stat(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	// FileInfo is a synthetic inode stand-in, not a socket admission claim.
	a.manager = &ODBCManager{connection: &connectionBinding{configuration: a.configuration, socket: info, manifestDigest: "fixture-digest"}}
	b.manager = &ODBCManager{connection: &connectionBinding{configuration: b.configuration, socket: info, manifestDigest: "fixture-digest"}}
	if !a.SameSchemaTarget(b) {
		t.Fatal("same immutable identity refused")
	}
	b.manager.connection.manifestDigest = "different"
	if a.SameSchemaTarget(b) {
		t.Fatal("native inventory drift accepted")
	}
	b.manager.connection.manifestDigest = "fixture-digest"
	b.manager.connection.socket = nil
	if a.SameSchemaTarget(b) {
		t.Fatal("missing inode accepted")
	}
}
