package pagination

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const MaximumLimit = 100
const MaximumCursorBytes = 512

// LegacyDefaultLimit remains only for callers not yet ported to owned policy.
const LegacyDefaultLimit = 20
const minimumCursorYear = 1000
const maximumCursorYear = 9999

type cursorWire struct {
	Time  string `json:"time"`
	ID    string `json:"id"`
	Scope string `json:"scope"`
}

// ParseWithDefault is the configured strict entrance. Legacy Parse semantics
// remain unchanged for other components until their owning repository port.
func ParseWithDefault(values url.Values, scope string, defaultLimit int) (int, Cursor, string, error) {
	if defaultLimit < 1 || defaultLimit > MaximumLimit {
		return 0, Cursor{}, "", errors.New("PAGINATION_CONFIGURATION_INVALID")
	}
	query := url.Values{}
	for key, value := range values {
		query[key] = append([]string{}, value...)
	}
	if _, exists := query["limit"]; !exists {
		query.Set("limit", strconv.Itoa(defaultLimit))
	} else if len(query["limit"]) == 1 && query.Get("limit") == "" {
		return 0, Cursor{}, "", errors.New("VALIDATION_FAILED")
	}
	if value, exists := query["cursor"]; exists && len(value) == 1 && value[0] == "" {
		return 0, Cursor{}, "", errors.New("INVALID_CURSOR")
	}
	limit, cursor, fingerprint, e := Parse(query, scope)
	if e != nil {
		return 0, Cursor{}, "", e
	}
	if value := query.Get("cursor"); value != "" {
		raw, e := base64.RawURLEncoding.Strict().DecodeString(value)
		if e != nil || base64.RawURLEncoding.EncodeToString(raw) != value {
			return 0, Cursor{}, "", errors.New("INVALID_CURSOR")
		}
		var wire cursorWire
		if strict.DecodeStrict(raw, &wire) != nil {
			return 0, Cursor{}, "", errors.New("INVALID_CURSOR")
		}
		id, e := uuid.Parse(wire.ID)
		if e != nil || id == uuid.Nil || id.String() != wire.ID {
			return 0, Cursor{}, "", errors.New("INVALID_CURSOR")
		}
		stamp, e := time.Parse(time.RFC3339Nano, wire.Time)
		if e != nil || stamp.IsZero() || stamp.UTC().Year() < minimumCursorYear || stamp.UTC().Year() > maximumCursorYear {
			return 0, Cursor{}, "", errors.New("INVALID_CURSOR")
		}
		cursor.Time = stamp.UTC()
	}
	return limit, cursor, fingerprint, nil
}
