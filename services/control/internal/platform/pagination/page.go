package pagination

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"errors"
	"github.com/google/uuid"
)

type Cursor struct {
	Time  time.Time `json:"time"`
	ID    string    `json:"id"`
	Scope string    `json:"scope"`
}

func Parse(values url.Values, scope string) (int, Cursor, string, error) {
	limit := LegacyDefaultLimit
	var cursor Cursor
	for key, v := range values {
		if (key != "limit" && key != "cursor") || len(v) != 1 {
			return 0, cursor, "", errors.New("VALIDATION_FAILED")
		}
	}
	if v := values.Get("limit"); v != "" {
		var e error
		limit, e = strconv.Atoi(v)
		if e != nil || limit < 1 || limit > MaximumLimit {
			return 0, cursor, "", errors.New("VALIDATION_FAILED")
		}
	}
	h := sha256.Sum256([]byte(scope))
	fingerprint := hex.EncodeToString(h[:])
	if raw := values.Get("cursor"); raw != "" {
		if len(raw) > MaximumCursorBytes {
			return 0, cursor, "", errors.New("INVALID_CURSOR")
		}
		b, e := base64.RawURLEncoding.DecodeString(raw)
		if e != nil || json.Unmarshal(b, &cursor) != nil || cursor.Scope != fingerprint || cursor.Time.IsZero() {
			return 0, cursor, "", errors.New("INVALID_CURSOR")
		}
		if _, e = uuid.Parse(cursor.ID); e != nil {
			return 0, cursor, "", errors.New("INVALID_CURSOR")
		}
	}
	return limit, cursor, fingerprint, nil
}
func Encode(stamp time.Time, id, scope string) *string {
	b, _ := json.Marshal(Cursor{stamp, id, scope})
	v := base64.RawURLEncoding.EncodeToString(b)
	return &v
}
