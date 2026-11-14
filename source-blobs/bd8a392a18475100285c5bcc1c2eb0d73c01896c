package mysqlstore

import (
	"crypto/sha256"
	"encoding/hex"
)

type inputBinding struct{ passwordSHA256, caSHA256 string }

func (inputBinding) String() string   { return "MYSQL_INPUT_BINDING_REDACTED" }
func (inputBinding) GoString() string { return "MYSQL_INPUT_BINDING_REDACTED" }
func inputSHA256(raw []byte) string   { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// InputMatches compares the immutable typed configuration and exact private
// bytes actually used by Open's connector. It does not reopen a file, connect,
// authenticate, inspect schema or return a credential/fingerprint/DSN.
func (s *Store) InputMatches(c Configuration, passwordSHA256, caSHA256 string) bool {
	return s != nil && s.pool != nil && s.configuration == c && len(passwordSHA256) == sha256.Size*2 && len(caSHA256) == sha256.Size*2 && s.inputs.passwordSHA256 == passwordSHA256 && s.inputs.caSHA256 == caSHA256
}
