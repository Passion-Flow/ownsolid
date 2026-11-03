package privateclusters

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestOriginAndCursorNeverGrantReplacementAuthority(t *testing.T) {
	c := syntheticClient()
	request := syntheticRun(t, c)
	member := "77777777-7777-4777-8777-777777777777"
	a := identity.Auth{SessionID: "88888888-8888-4888-8888-888888888888", Epoch: 1, TokenHash: []byte(strings.Repeat("a", 32)), Me: identity.Me{User: identity.User{ID: "99999999-9999-4999-8999-999999999999"}, Audience: "portal"}}
	r := Run{actor: a.Me.User.ID, session: a.SessionID, epoch: a.Epoch, tokenHash: append([]byte{}, a.TokenHash...), membership: &member, request: request}
	if e := original(r, a, &member); e != nil {
		t.Fatal(e)
	}
	replacement := member[:len(member)-1] + "8"
	if e := original(r, a, &replacement); e == nil {
		t.Fatal("replacement membership revived origin")
	}
	a.TokenHash[0] = 'b'
	if e := original(r, a, &member); e == nil {
		t.Fatal("replacement session token revived origin")
	}
	at := time.Date(2026, 10, 22, 1, 0, 0, 123456000, time.UTC)
	raw := nextCursor(at, r.request.ID)
	got, id, e := decodeCursor(*raw)
	if e != nil || !got.Equal(at) || id != r.request.ID {
		t.Fatal("UTC keyset round trip")
	}
	if _, _, e = decodeCursor(*raw + "="); e == nil {
		t.Fatal("noncanonical keyset accepted")
	}
	// Secrets and historical origin facts are private even on a retained projection.
	b, _ := json.Marshal(r)
	for _, value := range []string{a.SessionID, strings.Repeat("a", 32), member, "tokenHash", "actor_epoch"} {
		if strings.Contains(string(b), value) {
			t.Fatal("private origin projection")
		}
	}
}
func TestUnknownAndVendorErrorsRemainClosed(t *testing.T) {
	for _, e := range []error{errors.New("mysql vendor SQL token secret"), &transaction.Error{Kind: transaction.CommitUnknown}, remoteError("PRIVATE_CLUSTER_REQUEST_INTERRUPTED", true)} {
		result := safeError(e)
		if result == nil || strings.Contains(result.Error(), "secret") {
			t.Fatal("error boundary")
		}
	}
	if knownRejected(remoteError("PRIVATE_CLUSTER_RESOURCE_CONFLICT", false)) || knownRejected(remoteError("PRIVATE_CLUSTER_REQUEST_INTERRUPTED", true)) || knownRejected(remoteError("PRIVATE_CLUSTER_REDIRECT_REFUSED", false)) || !knownRejected(remoteError("PRIVATE_CLUSTER_ACCESS_FORBIDDEN", false)) {
		t.Fatal("unknown create treated as safe retry")
	}
}

func TestPrivateClusterCommitUncertaintyPrecedesCancellation(t *testing.T) {
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	rollback := &transaction.Error{Kind: transaction.CommitRolledBack}
	for _, e := range []error{
		errors.Join(context.Canceled, unknown), errors.Join(context.DeadlineExceeded, unknown),
		errors.Join(rollback, context.Canceled, unknown), errors.Join(unknown, rollback),
	} {
		if got := safeError(e); got == nil || got.Error() != string(transaction.CommitUnknown) {
			t.Fatalf("uncertain commit became retryable: %v", got)
		}
	}
	if got := safeError(errors.Join(context.Canceled, rollback)); got == nil || got.Error() != string(transaction.CommitRolledBack) {
		t.Fatalf("known rollback lost: %v", got)
	}
}
