package datasets

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/mongodatasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type mongoPostgresGrantExecutor struct{ transaction.Executor }

func (mongoPostgresGrantExecutor) Dialect() transaction.Dialect { return transaction.PostgreSQL }

func TestMongoImportPostgresGrantPinsCredentialsAndEpochAcrossJSONB(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	uri := filepath.Join(root, "uri")
	if err := os.WriteFile(uri, []byte("mongodb://fixture:synthetic-first@127.0.0.1/training?authSource=admin"), 0600); err != nil {
		t.Fatal(err)
	}
	source := config.MongoDBSource{ID: "m21", TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), URIFile: uri, TLS: false, Database: "training", Collection: "m21_examples", Fields: []string{"question", "answer"}}
	policy := config.DefaultMongoDBConfiguration()
	policy.Enabled = true
	policy.Sources = []config.MongoDBSource{source}
	s := &Service{Identity: &identity.Service{Config: config.Config{MongoDB: policy}}}
	w := workspaces.Workspace{ID: source.WorkspaceID, TenantID: source.TenantID}
	a := identity.Auth{Epoch: 17}
	a.Me.Audience = "admin"
	a.Me.User.IsPlatformAdmin = true
	q := mongoPostgresGrantExecutor{}
	captured, err := mongodatasets.CaptureSource(source, policy)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := s.mongoGrant(context.Background(), q, a, w, source, captured)
	if err != nil {
		t.Fatal(err)
	}
	// JSONB changes object-key ordering. Equality must compare typed provenance,
	// rather than depending on the serializer used before the SQL roundtrip.
	var reordered map[string]any
	if err = json.Unmarshal(grant, &reordered); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	receipt := MongoDBImport{SourceID: source.ID, sourceGrant: raw}
	if err = s.mongoRecheckReceipt(context.Background(), q, a, w, receipt); err != nil {
		t.Fatal("JSONB provenance roundtrip", err)
	}
	newer := a
	newer.Epoch++
	if err = s.mongoRecheckReceipt(context.Background(), q, newer, w, receipt); err == nil || err.Error() != "MONGODB_SOURCE_CHANGED" {
		t.Fatal("later identity epoch adopted old receipt", err)
	}
	if err = os.WriteFile(uri, []byte("mongodb://fixture:synthetic-second@127.0.0.1/training?authSource=admin"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.mongoGrant(context.Background(), q, a, w, source, captured); err == nil || err.Error() != "MONGODB_SOURCE_CHANGED" {
		t.Fatal("credential changed during captured read", err)
	}
	if err = s.mongoRecheckReceipt(context.Background(), q, a, w, receipt); err == nil || err.Error() != "MONGODB_SOURCE_CHANGED" {
		t.Fatal("credential replacement adopted old receipt", err)
	}
}
