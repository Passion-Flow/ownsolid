package compose_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
)

func exerciseComposeKnowledge(t *testing.T, client *http.Client, origin, password string, restart func()) {
	t.Helper()
	var csrf string
	call := func(method, path string, value, out any, expected int) {
		t.Helper()
		var body []byte
		if value != nil {
			body, _ = json.Marshal(value)
		}
		request, _ := http.NewRequest(method, origin+path, bytes.NewReader(body))
		if method != "GET" {
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", origin)
			request.Header.Set("X-CSRF-Token", csrf)
			request.Header.Set("Idempotency-Key", uuid.NewString())
		}
		response, e := client.Do(request)
		if e != nil {
			t.Fatal("actual Compose knowledge transport", e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if e != nil || response.StatusCode != expected {
			var problem struct{ Code string }
			json.Unmarshal(raw, &problem)
			t.Fatalf("actual Compose knowledge %s %s status=%d code=%s expected=%d", method, path, response.StatusCode, problem.Code, expected)
		}
		if out != nil && json.Unmarshal(raw, out) != nil {
			t.Fatal("actual Compose knowledge envelope")
		}
	}
	refresh := func() {
		var response struct{ Data struct{ Token string } }
		call("GET", "/api/v1/auth/csrf", nil, &response, 200)
		csrf = response.Data.Token
		if csrf == "" {
			t.Fatal("actual Compose knowledge CSRF")
		}
	}
	refresh()
	call("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, nil, 200)
	refresh()
	var me struct {
		Data struct{ Workspace struct{ ID string } }
	}
	call("GET", "/api/v1/me", nil, &me, 200)
	collection := "/api/v1/workspaces/" + me.Data.Workspace.ID + "/knowledge-bases"
	var configuration struct{ Data knowledge.Configuration }
	call("GET", collection+"/configuration", nil, &configuration, 200)
	if !configuration.Data.EncoderAvailable || !configuration.Data.CanWrite || configuration.Data.Dimension != 384 {
		t.Fatal("actual Linux encoder unavailable")
	}
	var base struct{ Data knowledge.Base }
	call("POST", collection, map[string]string{"name": "Actual Linux CPU knowledge", "reason": "Isolated Compose acceptance"}, &base, 201)
	path := collection + "/" + base.Data.ID
	text := "\ufeffThe project launch city is Lisbon.\r\nCaf\u00e9 😀 \u6c49\u5b57 <script>literal</script>\r\n"
	digest := sha256.Sum256([]byte(text))
	hash := hex.EncodeToString(digest[:])
	var document struct{ Data knowledge.Document }
	call("POST", path+"/documents", knowledge.UploadDocument{Name: "synthetic.txt", Content: text, SHA256: hash, Reason: "Real offline Linux CPU ingestion"}, &document, 202)
	documentPath := path + "/documents/" + document.Data.ID
	deadline := time.Now().Add(60 * time.Second)
	for {
		call("GET", documentPath, nil, &document, 200)
		if document.Data.State == "ready" {
			break
		}
		if document.Data.State == "failed" || time.Now().After(deadline) {
			t.Fatal("actual Compose CPU ingestion failed or timed out", document.Data.State)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if document.Data.VisibleSource == nil || document.Data.VisibleSource.SHA256 != hash || document.Data.VisibleSource.SourceVersion != 1 {
		t.Fatal("actual Compose immutable source")
	}
	sourcePath := documentPath + "/source?source_id=" + document.Data.VisibleSource.ID
	verify := func() {
		var source struct{ Data knowledge.Source }
		call("GET", sourcePath, nil, &source, 200)
		if source.Data.Content != text || source.Data.SHA256 != hash || source.Data.SizeBytes != len([]byte(text)) {
			t.Fatal("Linux restart lost exact original")
		}
		var search struct{ Data knowledge.SearchResult }
		call("POST", path+"/searches", knowledge.SearchRequest{Query: "In which city does the project launch?", TopK: 5, MinimumScore: 0.2}, &search, 200)
		if len(search.Data.Matches) == 0 || search.Data.Matches[0].SourceID != source.Data.SourceID || search.Data.Matches[0].SourceSHA256 != hash {
			t.Fatal("actual Linux vector search")
		}
	}
	verify()
	restart()
	refresh()
	verify()
	call("GET", documentPath, nil, &document, 200)
	call("DELETE", documentPath, knowledge.RevisionReason{ExpectedRevision: document.Data.Revision, Reason: "Isolated deletion"}, nil, 200)
	call("GET", sourcePath, nil, nil, 404)
	var empty struct{ Data knowledge.SearchResult }
	call("POST", path+"/searches", knowledge.SearchRequest{Query: "In which city does the project launch?", TopK: 5, MinimumScore: 0.2}, &empty, 200)
	if empty.Data.Matches == nil || len(empty.Data.Matches) != 0 {
		t.Fatal("deleted Linux knowledge remains retrievable")
	}
	t.Log("actual restricted Linux control+CPU encoder+pgvector exact source ingestion/search/restart/deletion passed; synthetic data, no target hardware claim")
}
