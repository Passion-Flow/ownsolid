// Package databaseaccess prepares and verifies isolated PostgreSQL runtime access.
package databaseaccess

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

const credentialBytes = 32
const scramSaltBytes = 16
const scramKeyBytes = 32
const minimumSCRAMIterations = 4096
const maximumSCRAMIterations = 1000000
const scramMechanism = "SCRAM-SHA-256"

// NewCredential is for the protected installation secret producer. The returned
// secret must never enter arguments, environment, logs or ordinary evidence.
func NewCredential() (string, error) {
	b := make([]byte, credentialBytes)
	if _, e := rand.Read(b); e != nil {
		return "", errors.New("RUNTIME_CREDENTIAL_GENERATION_FAILED")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func credentialValid(password string) bool {
	b, e := base64.RawURLEncoding.Strict().DecodeString(password)
	return e == nil && len(b) == credentialBytes && base64.RawURLEncoding.EncodeToString(b) == password
}
func scramKeys(password string, salt []byte, iterations int) ([]byte, []byte, error) {
	if iterations < minimumSCRAMIterations || iterations > maximumSCRAMIterations || len(salt) != scramSaltBytes {
		return nil, nil, errors.New("RUNTIME_SCRAM_PARAMETERS_UNSUPPORTED")
	}
	salted, e := pbkdf2.Key(sha256.New, password, salt, iterations, scramKeyBytes)
	if e != nil {
		return nil, nil, errors.New("RUNTIME_SCRAM_DERIVATION_FAILED")
	}
	mac := hmac.New(sha256.New, salted)
	mac.Write([]byte("Client Key"))
	stored := sha256.Sum256(mac.Sum(nil))
	mac = hmac.New(sha256.New, salted)
	mac.Write([]byte("Server Key"))
	return stored[:], mac.Sum(nil), nil
}
func newVerifier(password string, iterations int) (string, error) {
	if !credentialValid(password) {
		return "", errors.New("RUNTIME_CREDENTIAL_FORMAT_INVALID")
	}
	salt := make([]byte, scramSaltBytes)
	if _, e := rand.Read(salt); e != nil {
		return "", errors.New("RUNTIME_CREDENTIAL_GENERATION_FAILED")
	}
	stored, server, e := scramKeys(password, salt, iterations)
	if e != nil {
		return "", e
	}
	encode := base64.StdEncoding.EncodeToString
	return scramMechanism + "$" + strconv.Itoa(iterations) + ":" + encode(salt) + "$" + encode(stored) + ":" + encode(server), nil
}
func verifyCredential(verifier, password string) bool {
	if !credentialValid(password) || len(verifier) > 256 {
		return false
	}
	parts := strings.Split(verifier, "$")
	if len(parts) != 3 || parts[0] != scramMechanism {
		return false
	}
	parameters := strings.Split(parts[1], ":")
	keys := strings.Split(parts[2], ":")
	if len(parameters) != 2 || len(keys) != 2 {
		return false
	}
	iterations, e := strconv.Atoi(parameters[0])
	if e != nil || strconv.Itoa(iterations) != parameters[0] {
		return false
	}
	decode := base64.StdEncoding.Strict().DecodeString
	salt, e := decode(parameters[1])
	if e != nil {
		return false
	}
	stored, e := decode(keys[0])
	if e != nil || len(stored) != scramKeyBytes {
		return false
	}
	server, e := decode(keys[1])
	if e != nil || len(server) != scramKeyBytes {
		return false
	}
	expectedStored, expectedServer, e := scramKeys(password, salt, iterations)
	return e == nil && hmac.Equal(stored, expectedStored) && hmac.Equal(server, expectedServer)
}
