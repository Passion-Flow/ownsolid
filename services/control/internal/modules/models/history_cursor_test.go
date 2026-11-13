package models

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
)

func TestModelHistoryCursorIntegrityAndScope(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	a := identity.Auth{Me: identity.Me{User: identity.User{ID: uuid.NewString()}, Audience: "admin"}, SessionID: uuid.NewString(), Epoch: 1}
	tenant, workspace, family, anchor := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	scope := historyScope(a, tenant, workspace, "site-a", "cell-a", family, anchor, "")
	stamp, id := time.Date(2026, 11, 13, 1, 2, 3, 123456000, time.UTC), uuid.NewString()
	token, err := encodeHistoryCursor(stamp, id, scope, key)
	if err != nil || len(token) > HistoryMaximumCursorBytes {
		t.Fatal("bounded cursor", err)
	}
	value, err := decodeHistoryCursor(token, scope, key)
	if err != nil || value.ID != id || !value.Time.Equal(stamp) {
		t.Fatal("exact tuple", err)
	}
	for _, change := range []string{"account", "session", "epoch", "audience", "tenant", "workspace", "site", "cell", "family", "anchor", "filter"} {
		t.Run(change, func(t *testing.T) {
			actor, te, ws, site, cell, fa, an, filter := a, tenant, workspace, "site-a", "cell-a", family, anchor, ""
			switch change {
			case "account":
				actor.Me.User.ID = uuid.NewString()
			case "session":
				actor.SessionID = uuid.NewString()
			case "epoch":
				actor.Epoch++
			case "audience":
				actor.Me.Audience = "portal"
			case "tenant":
				te = uuid.NewString()
			case "workspace":
				ws = uuid.NewString()
			case "site":
				site = "site-b"
			case "cell":
				cell = "cell-b"
			case "family":
				fa = uuid.NewString()
			case "anchor":
				an = uuid.NewString()
			case "filter":
				filter = "retired"
			}
			got, err := decodeHistoryCursor(token, historyScope(actor, te, ws, site, cell, fa, an, filter), key)
			if err == nil || err.Error() != "INVALID_CURSOR" || got != (historyCursor{}) {
				t.Fatal("scope rebound", got, err)
			}
		})
	}
	for _, input := range []string{"", token + "=", token + ".extra", strings.Repeat("a", HistoryMaximumCursorBytes+1), "!" + token[1:]} {
		if _, err := decodeHistoryCursor(input, scope, key); err == nil {
			t.Fatal("malformed cursor accepted")
		}
	}
	other := make([]byte, 32)
	if _, err = rand.Read(other); err != nil {
		t.Fatal(err)
	}
	if _, err = decodeHistoryCursor(token, scope, other); err == nil {
		t.Fatal("rotated key accepted")
	}
	// Even a correctly signed noncanonical or invalid payload is not a v1 cursor.
	raw, _ := json.Marshal(value)
	for _, payload := range [][]byte{append([]byte(" "), raw...), []byte(strings.Replace(string(raw), `"version":1`, `"version":2`, 1)), []byte(strings.Replace(string(raw), id, "00000000-0000-0000-0000-000000000000", 1)), []byte(strings.Replace(string(raw), `"version":1`, `"version":1,"extra":true`, 1))} {
		input := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(historySignature(payload, key))
		if _, err := decodeHistoryCursor(input, scope, key); err == nil {
			t.Fatal("invalid canonical payload accepted")
		}
	}
	for _, stamp := range []time.Time{time.Time{}, stamp.Add(time.Nanosecond), stamp.In(time.FixedZone("offset", 3600))} {
		if _, err := encodeHistoryCursor(stamp, id, scope, key); err == nil {
			t.Fatal("unrepresentable tuple accepted")
		}
	}
}
