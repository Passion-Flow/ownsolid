package models

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

// HistoryMaximumCursorBytes is the immutable v1 navigation token ceiling.
const HistoryMaximumCursorBytes = 512

const historyCursorPurpose = "ownsolid.models.history.v1\x00"

type historyCursor struct {
	Version int       `json:"version"`
	Time    time.Time `json:"time"`
	ID      string    `json:"id"`
	Scope   string    `json:"scope"`
}

func historyScope(a identity.Auth, tenant, workspace, family, anchor, state string) string {
	raw, _ := json.Marshal([]any{a.Me.User.ID, a.SessionID, a.Epoch, a.Me.Audience, tenant, workspace, family, anchor, state})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func historyKeyValid(key []byte) bool {
	return len(key) >= security.MinimumHMACKeyBytes && len(key) <= security.MaximumHMACKeyBytes
}

func historySignature(raw, key []byte) []byte {
	signature := hmac.New(sha256.New, key)
	signature.Write([]byte(historyCursorPurpose))
	signature.Write(raw)
	return signature.Sum(nil)
}

func historyStampValid(stamp time.Time) bool {
	_, offset := stamp.Zone()
	return !stamp.IsZero() && offset == 0 && stamp.Year() >= 1000 && stamp.Year() <= 9999 && stamp.Equal(stamp.Truncate(time.Microsecond))
}

func encodeHistoryCursor(stamp time.Time, id, scope string, key []byte) (string, error) {
	if !historyKeyValid(key) || !historyStampValid(stamp) || !nonzeroModelID(id) || len(scope) != sha256.Size*2 {
		return "", fail("MODEL_HISTORY_INVALID")
	}
	raw, err := json.Marshal(historyCursor{Version: 1, Time: stamp.UTC(), ID: id, Scope: scope})
	if err != nil {
		return "", fail("MODEL_HISTORY_INVALID")
	}
	token := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(historySignature(raw, key))
	if len(token) > HistoryMaximumCursorBytes {
		return "", fail("MODEL_HISTORY_INVALID")
	}
	return token, nil
}

func decodeHistoryCursor(token, scope string, key []byte) (historyCursor, error) {
	var value historyCursor
	bad := fail("INVALID_CURSOR")
	if !historyKeyValid(key) || len(token) == 0 || len(token) > HistoryMaximumCursorBytes {
		return value, bad
	}
	body, signature, ok := strings.Cut(token, ".")
	if !ok {
		return value, bad
	}
	decode := func(text string) ([]byte, error) {
		raw, err := base64.RawURLEncoding.Strict().DecodeString(text)
		if err != nil || base64.RawURLEncoding.EncodeToString(raw) != text {
			return nil, bad
		}
		return raw, nil
	}
	raw, err := decode(body)
	if err != nil {
		return value, bad
	}
	sig, err := decode(signature)
	if err != nil || !hmac.Equal(sig, historySignature(raw, key)) || datawire.DecodeStrict(raw, &value) != nil {
		return historyCursor{}, bad
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) || value.Version != 1 || value.Scope != scope ||
		!nonzeroModelID(value.ID) || !historyStampValid(value.Time) {
		return historyCursor{}, bad
	}
	return value, nil
}
