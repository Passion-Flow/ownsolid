package artifactstores

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func syntheticWorkerAuthority() identity.Auth {
	hash := sha256.Sum256([]byte("explicit synthetic worker session digest"))
	var a identity.Auth
	a.Me.User.ID = "11111111-1111-4111-8111-111111111111"
	a.SessionID = "22222222-2222-4222-8222-222222222222"
	a.Me.Audience = "portal"
	a.Epoch = 3
	a.TokenHash = hash[:]
	return a
}

func TestStoredObjectWorkerAuthorityRoundTrip(t *testing.T) {
	a := syntheticWorkerAuthority()
	raw := actorBytes(a)
	got, err := actorFrom(raw)
	if err != nil || !reflect.DeepEqual(got, a) || !bytes.Equal(got.TokenHash, a.TokenHash) {
		t.Fatal("durable worker authority lost its exact authenticated session binding", err)
	}
	// Simulate database JSON key ordering/whitespace without replacing identity.
	var normalized any
	if err = json.Unmarshal(raw, &normalized); err != nil {
		t.Fatal(err)
	}
	raw, err = json.MarshalIndent(normalized, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	got, err = actorFrom(raw)
	if err != nil || !reflect.DeepEqual(got, a) {
		t.Fatal("normalized database authority changed the original actor", err)
	}
}

func TestStoredObjectWorkerAuthorityRefusals(t *testing.T) {
	raw := actorBytes(syntheticWorkerAuthority())
	for _, fault := range []string{"missing-hash", "short-hash", "hash-array", "hash-newline", "case-alias", "unknown", "duplicate", "null", "trailing", "invalid-actor", "invalid-session", "invalid-audience", "invalid-version", "invalid-epoch", "oversized", "legacy"} {
		t.Run(fault, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "missing-hash":
				delete(value, "token_hash")
			case "short-hash":
				value["token_hash"] = []byte{1}
			case "hash-array":
				value["token_hash"] = make([]int, sha256.Size)
			case "hash-newline":
				value["token_hash"] = value["token_hash"].(string) + "\n"
			case "case-alias":
				value["TOKEN_HASH"] = value["token_hash"]
				delete(value, "token_hash")
			case "unknown":
				value["untrusted"] = false
			case "null":
				value["token_hash"] = nil
			case "invalid-actor":
				value["actor_id"] = "00000000-0000-0000-0000-000000000000"
			case "invalid-session":
				value["session_id"] = "22222222222242228222222222222222"
			case "invalid-audience":
				value["audience"] = "unrecognized"
			case "invalid-version":
				value["format_version"] = 2
			case "invalid-epoch":
				value["epoch"] = 0
			}
			bad, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "duplicate":
				bad = append([]byte(`{"actor_id":"11111111-1111-4111-8111-111111111111",`), bad[1:]...)
			case "trailing":
				bad = append(bad, []byte(" {}")...)
			case "oversized":
				bad = append(bad, []byte(strings.Repeat(" ", 4096))...)
			case "legacy":
				bad = []byte(`{"Actor":"11111111-1111-4111-8111-111111111111","Session":"22222222-2222-4222-8222-222222222222","Audience":"portal","Epoch":3}`)
			}
			got, err := actorFrom(bad)
			if err == nil || !reflect.DeepEqual(got, identity.Auth{}) {
				t.Fatal("unproven durable authority exposed a usable actor", fault)
			}
		})
	}
}
