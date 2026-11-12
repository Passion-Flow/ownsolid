package cli

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func authenticatedCLIServer(t *testing.T, handler http.HandlerFunc) *atomic.Int64 {
	t.Helper()
	session := token(t)
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		cookie, err := r.Cookie("__Host-admin_session")
		if err != nil || cookie.Value != session {
			t.Error("request lost its Admin session")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	dir := privateDir(t)
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", "")
	ca := filepath.Join(dir, "ca.pem")
	writeFile(t, ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	writeConfig(t, dir, fileConfig{
		Server: server.URL, CAFile: ca, SiteID: "other-site", CellID: "other-cell", WorkspaceID: secondNodeID,
	})
	cfg, err := loadSettings("", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.root.Close() })
	if err := store.save(session); err != nil {
		t.Fatal(err)
	}
	return &requests
}
