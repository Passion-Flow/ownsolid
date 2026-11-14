package mongodatasets

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestCapturedSourcePinsPrivateBytesAndRejectsChangedGrant(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "uri")
	first := "mongodb://fixture:synthetic-first@example.invalid/training?authSource=admin"
	if e := os.WriteFile(path, []byte(first), 0600); e != nil {
		t.Fatal(e)
	}
	s := config.MongoDBSource{ID: "source", TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), URIFile: path, TLS: true, Database: "training", Collection: "m21_examples", Fields: []string{"question", "answer"}}
	p := config.DefaultMongoDBConfiguration()
	p.Enabled = true
	p.Sources = []config.MongoDBSource{s}
	captured, e := CaptureSource(s, p)
	if e != nil {
		t.Fatal(e)
	}
	if captured.options.Auth == nil || captured.options.Auth.Password != "synthetic-first" {
		t.Fatal("captured driver lost protected credential")
	}
	if e = os.WriteFile(path, []byte(strings.Replace(first, "synthetic-first", "synthetic-second", 1)), 0600); e != nil {
		t.Fatal(e)
	}
	replacement, e := CaptureSource(s, p)
	if e != nil {
		t.Fatal(e)
	}
	if captured.Fingerprint() == replacement.Fingerprint() || captured.options.Auth.Password != "synthetic-first" {
		t.Fatal("credential change did not fence the original source")
	}
	s.Fields[0] = "changed"
	if captured.source.Fields[0] != "question" {
		t.Fatal("captured fields changed through caller slice")
	}
	if _, e = CaptureSource(captured.source, p); e == nil {
		t.Fatal("current grant mutation admitted")
	}
	raw, e := json.Marshal(captured)
	if e != nil || string(raw) != "{}" {
		t.Fatal("private source serialized", e)
	}
	for _, rendered := range []string{fmt.Sprint(captured), fmt.Sprintf("%#v", captured), string(raw)} {
		if strings.Contains(rendered, "synthetic") || strings.Contains(rendered, path) || strings.Contains(rendered, captured.Fingerprint()) {
			t.Fatal("private source leaked")
		}
	}
}
func TestCapturedSourceRejectsUnprotectedOrUnapprovedInput(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "uri")
	if e := os.WriteFile(path, []byte("mongodb://fixture:synthetic@example.invalid/training"), 0644); e != nil {
		t.Fatal(e)
	}
	s := config.MongoDBSource{ID: "source", TenantID: uuid.NewString(), WorkspaceID: uuid.NewString(), URIFile: path, TLS: true, Database: "training", Collection: "m21_examples", Fields: []string{"question", "answer"}}
	p := config.DefaultMongoDBConfiguration()
	p.Enabled = true
	p.Sources = []config.MongoDBSource{s}
	if _, e := CaptureSource(s, p); e == nil {
		t.Fatal("unprotected source admitted")
	}
	if e := os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	s.Collection = "unapproved"
	if _, e := CaptureSource(s, p); e == nil {
		t.Fatal("source outside current allowlist admitted")
	}
}
