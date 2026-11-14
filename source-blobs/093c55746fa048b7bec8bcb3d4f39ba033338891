package sso

import (
	"crypto/rand"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type damengSecretFixtureRow struct {
	scope  secretScope
	sealed sealedSecret
}
type damengSecretFixtureRows struct {
	values []damengSecretFixtureRow
	index  int
	err    error
}

func (r *damengSecretFixtureRows) Next() bool { r.index++; return r.index <= len(r.values) }
func (r *damengSecretFixtureRows) Err() error { return r.err }
func (*damengSecretFixtureRows) Close()       {}
func (r *damengSecretFixtureRows) Scan(dest ...any) error {
	v := r.values[r.index-1]
	*dest[0].(*string), *dest[1].(*string), *dest[2].(*string) = v.scope.tenant, v.scope.connector, v.scope.record
	*dest[3].(*string) = v.sealed.keyID
	*dest[4].(*[]byte), *dest[5].(*[]byte) = v.sealed.nonce, v.sealed.ciphertext
	return nil
}

func TestDamengSSOSecretPageRefusesCorruptionCapacityAndNonprogress(t *testing.T) {
	var key [32]byte
	if _, e := rand.Read(key[:]); e != nil {
		t.Fatal(e)
	}
	s := &Service{keys: &Keyring{active: "test", keys: map[string][32]byte{"test": key}}}
	id := uuid.NewString()
	scope := secretScope{uuid.NewString(), id, id, clientSecretPurpose}
	sealed, e := s.keys.seal(scope, []byte("synthetic-fixture-secret"))
	if e != nil {
		t.Fatal(e)
	}
	value := damengSecretFixtureRow{scope, sealed}
	rows := &damengSecretFixtureRows{values: []damengSecretFixtureRow{value}}
	if n, last, e := s.verifyDamengSecretPage(rows, clientSecretPurpose, "", 2, 2); e != nil || n != 1 || last != id {
		t.Fatal("valid encrypted page did not advance", e)
	}
	bad := value
	bad.sealed.ciphertext = append([]byte(nil), sealed.ciphertext...)
	bad.sealed.ciphertext[0] ^= 1
	invalidPlain := value
	invalidPlain.sealed, e = s.keys.seal(scope, []byte("synthetic fixture secret"))
	if e != nil {
		t.Fatal(e)
	}
	for _, scenario := range []struct {
		rows             *damengSecretFixtureRows
		last             string
		limit, remaining int
	}{
		{&damengSecretFixtureRows{values: []damengSecretFixtureRow{bad}}, "", 2, 2},
		{&damengSecretFixtureRows{values: []damengSecretFixtureRow{invalidPlain}}, "", 2, 2},
		{&damengSecretFixtureRows{values: []damengSecretFixtureRow{value}}, id, 2, 2},
		{&damengSecretFixtureRows{values: []damengSecretFixtureRow{value, value}}, "", 2, 2},
		{&damengSecretFixtureRows{values: []damengSecretFixtureRow{value}}, "", 1, 0},
		{&damengSecretFixtureRows{values: []damengSecretFixtureRow{value}, err: errors.New("private query failure")}, "", 2, 2},
	} {
		if n, last, e := s.verifyDamengSecretPage(scenario.rows, clientSecretPurpose, scenario.last, scenario.limit, scenario.remaining); e == nil || n != 0 || last != "" {
			t.Fatal("failed inventory page released a continuation or accepted ciphertext")
		}
	}
}
