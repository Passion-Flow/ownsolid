package imageregistry

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
)

func TestRegistryFinalAuthorityChecksRenewedGrantAndImmutableIntent(t *testing.T) {
	for _, name := range []string{"grant_rotation", "expiry_rotation", "archive_changed", "proof_changed", "revision_changed", "pin_changed", "revoked"} {
		t.Run(name, func(t *testing.T) {
			original := registrySyntheticManifest()
			current := original
			switch name {
			case "grant_rotation":
				current.GrantID = "42345678-1234-1234-1234-123456789abc"
			case "expiry_rotation":
				current.ExpiresAt = current.ExpiresAt.Add(time.Hour)
			case "archive_changed":
				current.ArchiveBytes++
			case "proof_changed":
				current.ReleaseProof = json.RawMessage(`{"explicit_test":false}`)
			case "revision_changed":
				current.Revision++
			case "pin_changed":
				current.PinID = current.GrantID
			}
			path := "/agent/v1/nodes/52345678-1234-1234-1234-123456789abc/tasks/62345678-1234-1234-1234-123456789abc/image-registry"
			calls := 0
			// Real TLS transport to an explicit authority contract double. Its metadata
			// is synthetic; this does not prove a real server grant or signed image.
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != strings.Replace(path, "/agent/v1/nodes/", "/agent/v1/sites/default/cells/default/nodes/", 1)+"/manifest" || r.Header.Get("Authorization") != "Bearer explicit_contract_double_credential" {
					t.Error("authority request changed")
					w.WriteHeader(400)
					return
				}
				if name == "revoked" {
					w.WriteHeader(403)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(struct {
					Data wire.RegistryManifest `json:"data"`
				}{current})
			}))
			defer server.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
				t.Fatal(e)
			}
			connection, e := client.New(server.URL, ca)
			if e != nil {
				t.Fatal(e)
			}
			defer connection.Close()
			connection.Credential = "explicit_contract_double_credential"
			loader := &Loader{control: connection}
			command := wire.InferenceCommand{Action: "deploy", DeploymentID: original.DeploymentID, ModelID: "72345678-1234-1234-1234-123456789abc", CatalogDigest: wire.ModelCatalogDigest(), GPUUUID: "GPU-82345678-1234-1234-1234-123456789abc", Revision: original.Revision, RegistryPinID: original.PinID}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			actual, e := loader.finalTaskAuthority(ctx, path, "explicit_contract_double_lease", command, original)
			want := name == "grant_rotation" || name == "expiry_rotation"
			if (e == nil) != want || calls != 1 {
				t.Fatal("final authority changed", name, e, calls)
			}
			if want && (actual.GrantID != current.GrantID || !actual.ExpiresAt.Equal(current.ExpiresAt)) {
				t.Fatal("stale grant returned")
			}
		})
	}
}
