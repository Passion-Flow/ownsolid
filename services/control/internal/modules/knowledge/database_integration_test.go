//go:build knowledge_database_integration

package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

// One-hot vectors below are explicit database-integrity fixtures, not model quality evidence.
func unitVector() string { return "[1," + strings.Repeat("0,", 382) + "0]" }
func sqlCode(t *testing.T, e error, code string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(e, &pg) || pg.Code != code {
		t.Fatalf("wanted SQLSTATE %s, observed %v", code, e)
	}
}
func TestM12ActualMigration12ImmutableSourceVectorAndCompositeScope(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	ctx := context.Background()
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	base, generation, document, source, attempt := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	workspace := a.Me.Workspace.ID
	var tenant string
	if e := s.DB.QueryRow(ctx, "SELECT tenant_id::text FROM workspaces WHERE id=$1", workspace).Scan(&tenant); e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `INSERT INTO knowledge_bases(id,tenant_id,workspace_id,name,name_key,active_generation_id,created_by,creation_key,creation_fingerprint) VALUES($1,$2,$3,'Synthetic facts','synthetic facts',$4,$5,$6,$7)`, base, tenant, workspace, generation, a.Me.User.ID, uuid.NewString(), make([]byte, 32)); e != nil {
		t.Fatal("base schema", e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO knowledge_generations(id,tenant_id,workspace_id,base_id,catalog_sha256,dimension,chunk_tokens,overlap_tokens,state,requested_by,requested_audience,requested_epoch,expected_documents,activated_at) VALUES($1,$2,$3,$4,$5,384,192,32,'active',$6,'admin',$7,0,clock_timestamp())`, generation, tenant, workspace, base, CatalogDigest(), a.Me.User.ID, a.Epoch); e != nil {
		t.Fatal("generation schema", e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO knowledge_documents(id,tenant_id,workspace_id,base_id,name,name_key,state,pending_source_id,current_attempt_id,created_by) VALUES($1,$2,$3,$4,'facts.txt','facts.txt','pending',$5,$6,$7)`, document, tenant, workspace, base, source, attempt, a.Me.User.ID); e != nil {
		t.Fatal("document schema", e)
	}
	text := []byte("The project launch city is Lisbon.\r\nCabinet code TANGERINE-42.")
	if _, e = tx.Exec(ctx, `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by) VALUES($1,$2,$3,$4,$5,1,$6,$7,$8)`, source, tenant, workspace, base, document, text, hashBytes(text), a.Me.User.ID); e != nil {
		t.Fatal("source schema", e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO knowledge_attempts(id,tenant_id,workspace_id,base_id,document_id,source_id,generation_id,kind,attempt_number,expected_document_revision,requested_by,requested_audience,requested_epoch,state) VALUES($1,$2,$3,$4,$5,$6,$7,'ingest',1,1,$8,'admin',$9,'pending')`, attempt, tenant, workspace, base, document, source, generation, a.Me.User.ID, a.Epoch); e != nil {
		t.Fatal("attempt schema", e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO knowledge_chunks(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,attempt_id,ordinal,start_byte,end_byte,token_count,embedding) VALUES($1,$2,$3,$4,$5,$6,$7,0,0,$8,12,$9::public.vector(384))`, tenant, workspace, base, document, generation, source, attempt, len(text), unitVector()); e != nil {
		t.Fatal("actual pgvector persistence", e)
	}
	if _, e = tx.Exec(ctx, `UPDATE knowledge_attempts SET state='ready',chunk_count=1,finished_at=clock_timestamp() WHERE id=$1`, attempt); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE knowledge_documents SET state='ready',visible_source_id=$2,pending_source_id=NULL WHERE id=$1`, document, source); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal("deferred exact source/job scope", e)
	}
	var bytes []byte
	var digest string
	var dimensions int
	var distance float64
	if e = s.DB.QueryRow(ctx, `SELECT v.content,v.sha256,public.vector_dims(c.embedding),c.embedding OPERATOR(public.<=>) $2::public.vector FROM knowledge_source_versions v JOIN knowledge_chunks c ON c.source_id=v.id WHERE v.id=$1`, source, unitVector()).Scan(&bytes, &digest, &dimensions, &distance); e != nil || string(bytes) != string(text) || digest != hashBytes(text) || dimensions != 384 || distance != 0 {
		t.Fatal("actual source/vector roundtrip", e)
	}
	_, e = s.DB.Exec(ctx, `UPDATE knowledge_source_versions SET content=content WHERE id=$1`, source)
	sqlCode(t, e, "23514")
	_, e = s.DB.Exec(ctx, `UPDATE knowledge_chunks SET token_count=token_count WHERE attempt_id=$1`, attempt)
	sqlCode(t, e, "23514")
	_, e = s.DB.Exec(ctx, `UPDATE knowledge_generations SET catalog_sha256=$2 WHERE id=$1`, generation, strings.Repeat("b", 64))
	sqlCode(t, e, "23514")
	insertChunk := `INSERT INTO knowledge_chunks(tenant_id,workspace_id,base_id,document_id,generation_id,source_id,attempt_id,ordinal,start_byte,end_byte,token_count,embedding) VALUES($1,$2,$3,$4,$5,$6,$7,1,0,1,1,$8::public.vector(384))`
	_, e = s.DB.Exec(ctx, insertChunk, tenant, workspace, uuid.NewString(), document, generation, source, attempt, unitVector())
	sqlCode(t, e, "23503")
	_, e = s.DB.Exec(ctx, insertChunk, tenant, workspace, base, document, generation, source, attempt, "[1,0]")
	if e == nil {
		t.Fatal("wrong vector dimensions accepted")
	}
	zero := "[" + strings.Repeat("0,", 383) + "0]"
	_, e = s.DB.Exec(ctx, insertChunk, tenant, workspace, base, document, generation, source, attempt, zero)
	sqlCode(t, e, "23514")
	badSource := `INSERT INTO knowledge_source_versions(id,tenant_id,workspace_id,base_id,document_id,source_version,content,sha256,created_by) VALUES($1,$2,$3,$4,$5,2,$6,$7,$8)`
	_, e = s.DB.Exec(ctx, badSource, uuid.NewString(), tenant, workspace, base, document, text, strings.Repeat("f", 64), a.Me.User.ID)
	sqlCode(t, e, "23514")
	invalid := []byte{0xff}
	_, e = s.DB.Exec(ctx, badSource, uuid.NewString(), tenant, workspace, base, document, invalid, hashBytes(invalid), a.Me.User.ID)
	sqlCode(t, e, "22021")
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_chunks WHERE attempt_id=$1", attempt).Scan(&count); e != nil || count != 1 {
		t.Fatal("negative operations changed committed vectors", e)
	}
}

func TestM12ActualSchema11UpgradeFailureRollbackAndRecovery(t *testing.T) {
	// Keep the actual12 down/up test isolated from later additive migrations.
	s := fixture.NewAtVersion(t, 12)
	ctx := context.Background()
	down, e := migrations.Files.ReadFile("postgresql/000012_knowledge.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, string(down)); e != nil {
		t.Fatal("isolated empty knowledge rollback", e)
	}
	if _, e = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=12"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	password := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	var before string
	if e = s.DB.QueryRow(ctx, "SELECT string_agg(version::text||':'||checksum,',' ORDER BY version) FROM schema_migrations").Scan(&before); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "CREATE TABLE knowledge_bases(synthetic_marker integer NOT NULL); INSERT INTO knowledge_bases VALUES(7)"); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, s.DB); e == nil {
		t.Fatal("conflicting migration should roll back")
	}
	var last int
	if e = s.DB.QueryRow(ctx, "SELECT max(version) FROM schema_migrations").Scan(&last); e != nil || last != 11 {
		t.Fatal("failed migration advanced metadata", e)
	}
	var marker int
	if e = s.DB.QueryRow(ctx, "SELECT synthetic_marker FROM knowledge_bases").Scan(&marker); e != nil || marker != 7 {
		t.Fatal("failed migration changed preexisting fixture", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TABLE knowledge_bases"); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, s.DB); e != nil {
		t.Fatal("actual additive schema12 recovery", e)
	}
	if e = database.Check(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	var after string
	if e = s.DB.QueryRow(ctx, "SELECT string_agg(version::text||':'||checksum,',' ORDER BY version) FROM schema_migrations WHERE version<=11").Scan(&after); e != nil || before != after {
		t.Fatal("frozen prior migrations changed", e)
	}
	raw, e := migrations.Files.ReadFile("postgresql/000012_knowledge.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(raw)
	var stored string
	if e = s.DB.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE version=12").Scan(&stored); e != nil || stored != hex.EncodeToString(sum[:]) {
		t.Fatal("actual schema12 digest", e)
	}
	var active bool
	if e = s.DB.QueryRow(ctx, "SELECT status='active' FROM users WHERE id=$1", a.Me.User.ID).Scan(&active); e != nil || !active {
		t.Fatal("existing account lost", e)
	}
	var extension string
	if e = s.DB.QueryRow(ctx, "SELECT extversion FROM pg_extension WHERE extname='vector'").Scan(&extension); e != nil || extension != "0.8.7" {
		t.Fatal("rollback altered shared extension", e)
	}
	t.Log(fmt.Sprintf("Actual isolated schema11→failed12 rollback→repaired12; prior checksums/account and database-wide extension preserved."))
}
