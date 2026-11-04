package middleware

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestPrivateActorSnapshotRetainsOnlyRequiredCurrentAuthority(t *testing.T) {
	a := identity.Auth{}
	a.Me.User.ID = "11111111-1111-1111-1111-111111111111"
	a.Me.User.Email = "private@example.invalid"
	a.Me.Audience = "portal"
	a.SessionID = "22222222-2222-2222-2222-222222222222"
	a.TokenHash = []byte(strings.Repeat("x", 32))
	a.Epoch = 7
	saved := snapshot(a)
	raw, _ := json.Marshal(saved)
	restored := saved.auth()
	if strings.Contains(string(raw), "private@example.invalid") || !actor(restored) || restored.Epoch != 7 || restored.SessionID != a.SessionID {
		t.Fatal("snapshot lost authority or retained unrelated private data")
	}
	a.TokenHash[0] = 'y'
	if saved.TokenHash[0] == 'y' {
		t.Fatal("snapshot shared mutable credential hash")
	}
}
