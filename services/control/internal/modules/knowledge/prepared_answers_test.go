package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This pure capability test must reach the proof boundary with a valid selected
// repository owner. The double supplies only its dialect; every SQL effect fails
// the test rather than manufacturing a successful transaction or database result.
type capabilityBoundaryStore struct {
	t       *testing.T
	dialect transaction.Dialect
}

func (s *capabilityBoundaryStore) Dialect() transaction.Dialect { return s.dialect }
func (s *capabilityBoundaryStore) Exec(context.Context, transaction.Statement) (int64, error) {
	s.t.Helper()
	s.t.Fatal("invalid private capability reached SQL Exec")
	return 0, nil
}
func (s *capabilityBoundaryStore) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	s.t.Helper()
	s.t.Fatal("invalid private capability reached SQL Query")
	return nil, nil
}
func (s *capabilityBoundaryStore) QueryRow(context.Context, transaction.Statement) transaction.Row {
	s.t.Helper()
	s.t.Fatal("invalid private capability reached SQL QueryRow")
	return nil
}
func (s *capabilityBoundaryStore) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	s.t.Helper()
	s.t.Fatal("invalid private capability started a transaction")
	return nil
}

func TestPreparedAnswerCapabilityDoesNotSerializePrivateInputsOrPermitReconstruction(t *testing.T) {
	service := &Service{}
	a := identity.Auth{Me: identity.Me{User: identity.User{ID: "synthetic-actor"}, Audience: "portal"}, SessionID: "synthetic-private-session", TokenHash: []byte("synthetic-hashed-session-token")}
	p := &PreparedAnswer{service: service, actor: a.Me.User.ID, audience: a.Me.Audience, session: a.SessionID, tokenFingerprint: sha256.Sum256(a.TokenHash), question: "synthetic-private-question", retrieved: SearchResult{Matches: []Match{{Text: "synthetic-private-source-body"}}}}
	raw, e := json.Marshal(p)
	if e != nil || string(raw) != "{}" {
		t.Fatal("private capability became serializable", e)
	}
	for _, secret := range []string{p.question, p.session, p.retrieved.Matches[0].Text} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("capability disclosed private input")
		}
	}
	var reconstructed PreparedAnswer
	if json.Unmarshal(raw, &reconstructed) != nil {
		t.Fatal("JSON contract test failed")
	}
	if _, e = service.CompletePreparedAnswer(context.Background(), a, &reconstructed, "", "synthetic-request"); e == nil || e.Error() != "KNOWLEDGE_AUTHORITY_INVALID" {
		t.Fatal("reconstructed browser value gained authority", e)
	}
	p.consumed.Store(true)
	if _, e = service.CompletePreparedAnswer(context.Background(), a, p, "", "synthetic-request"); e == nil || e.Error() != "KNOWLEDGE_AUTHORITY_INVALID" {
		t.Fatal("consumed retrieval capability reused", e)
	}
	for _, dialect := range []transaction.Dialect{transaction.PostgreSQL, transaction.MySQL} {
		t.Run(string(dialect), func(t *testing.T) {
			store := &capabilityBoundaryStore{t: t, dialect: dialect}
			owner := &identity.Service{Storage: store}
			boundService := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
			if e := boundService.borrowed(context.Background(), store); e != nil {
				t.Fatal("capability fixture did not reach borrowed repository boundary", e)
			}
			for _, completed := range []*CompletedAnswer{nil, {}} {
				if e := boundService.RecheckCompletedAnswerWithin(context.Background(), store, a, CompletionBinding{}, completed); e == nil || e.Error() != "KNOWLEDGE_AUTHORITY_INVALID" {
					t.Fatal("empty completed proof gained commit authority", e)
				}
			}
		})
	}
}
