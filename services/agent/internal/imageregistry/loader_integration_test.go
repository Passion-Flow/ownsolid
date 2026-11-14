//go:build registry_integration

package imageregistry

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func registryNodeInputs(t *testing.T) (Config, wire.RegistryOfflineManifest, string, string) {
	t.Helper()
	c, e := Load(os.Getenv("PROJECT_ONE_REGISTRY_AGENT_TEST_CONFIGURATION"))
	if e != nil {
		t.Fatal("actual node filesystem/configuration required", e)
	}
	c.Root = t.TempDir()
	if e = os.Chmod(c.Root, 0700); e != nil {
		t.Fatal(e)
	}

	t.Cleanup(func() {
		directory := os.Getenv("PROJECT_ONE_REGISTRY_NODE_EVIDENCE_DIRECTORY")
		if directory == "" {
			return
		}
		if !filepath.IsAbs(directory) {
			t.Error("exact retained node evidence directory required")
			return
		}
		name := strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
		e := filepath.WalkDir(c.Root, func(path string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
				return nil
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			if info.Size() > 5<<20 {
				return os.ErrInvalid
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			relative, e := filepath.Rel(c.Root, path)
			if e != nil {
				return e
			}
			leaf := strings.ReplaceAll(relative, string(filepath.Separator), "-")
			return os.WriteFile(filepath.Join(directory, "node-journal-"+name+"-"+leaf), raw, 0600)
		})
		if e != nil {
			t.Error("actual node journal retention failed", e)
		}
	})
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_REGISTRY_OFFLINE_MANIFEST"))
	if e != nil {
		t.Fatal(e)
	}
	var m wire.RegistryOfflineManifest
	if release.StrictJSON(raw, &m) != nil || m.Validate() != nil {
		t.Fatal("actual exported held snapshot manifest required")
	}
	archive := os.Getenv("PROJECT_ONE_REGISTRY_OFFLINE_ARCHIVE")
	socket := os.Getenv("PROJECT_ONE_REGISTRY_DOCKER_SOCKET")
	if !filepath.IsAbs(archive) || !filepath.IsAbs(socket) {
		t.Fatal("actual archive/socket references required")
	}
	return c, m, archive, socket
}
func TestActualRegistryOfflineDocker(t *testing.T) {
	t.Run("offline_actual_load_and_independent_inspect", func(t *testing.T) {
		c, m, archive, socket := registryNodeInputs(t)
		docker, e := inference.NewDocker(socket)
		if e != nil {
			t.Fatal(e)
		}
		defer docker.Close()
		loader, e := New(c, nil, docker)
		if e != nil {
			t.Fatal(e)
		}
		if e = loader.ImportOffline(t.Context(), m, archive); e != nil {
			t.Fatal("actual Docker archive load/inspect unavailable; no qualification", e)
		}
		if e = loader.ReconcileOffline(t.Context(), m); e != nil {
			t.Fatal(e)
		}
		if e = loader.ImportOffline(t.Context(), m, archive); e == nil || e.Error() != "REGISTRY_RECOVERY_REQUIRED" {
			t.Fatal("ordinary replay repeated a load", e)
		}
	})
	t.Run("actual_load_ack_unknown_and_explicit_inspect", func(t *testing.T) {
		c, m, archive, socket := registryNodeInputs(t)
		owned := filepath.Join(t.TempDir(), "docker-fault.sock")
		listener, e := net.Listen("unix", owned)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(owned, 0600); e != nil {
			t.Fatal(e)
		}
		transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}
		defer transport.CloseIdleConnections()
		var loads atomic.Int64
		server := &http.Server{ReadHeaderTimeout: time.Duration(c.OperationSeconds) * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := r.Clone(r.Context())
			req.URL.Scheme = "http"
			req.URL.Host = "docker"
			req.RequestURI = ""
			response, e := transport.RoundTrip(req)
			if e != nil {
				t.Error("actual Docker forwarding failed")
				w.WriteHeader(502)
				return
			}
			defer response.Body.Close()
			for key, values := range response.Header {
				for _, v := range values {
					w.Header().Add(key, v)
				}
			}
			if strings.HasSuffix(r.URL.Path, "/images/load") && loads.Add(1) == 1 {
				if response.StatusCode != 200 {
					t.Error("real Docker load did not acknowledge200")
					w.WriteHeader(502)
					return
				}
				if _, e = io.Copy(io.Discard, response.Body); e != nil {
					t.Error("real Docker load response failed", e)
				}
				w.Header().Set("Content-Length", "99")
				w.WriteHeader(200)
				w.Write([]byte(`{"stream":"truncated`))
				return
			}
			w.WriteHeader(response.StatusCode)
			io.Copy(w, response.Body)
		})}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if e := server.Serve(listener); e != nil && e != http.ErrServerClosed {
				t.Error(e)
			}
		}()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			server.Shutdown(ctx)
			<-done
		}()
		docker, e := inference.NewDocker(owned)
		if e != nil {
			t.Fatal(e)
		}
		defer docker.Close()
		loader, e := New(c, nil, docker)
		if e != nil {
			t.Fatal(e)
		}
		if e = loader.ImportOffline(t.Context(), m, archive); e == nil || e.Error() != "REGISTRY_LOAD_OUTCOME_UNKNOWN" {
			t.Fatal("actual committed load ambiguity was accepted", e)
		}
		if e = loader.ImportOffline(t.Context(), m, archive); e == nil {
			t.Fatal("unacknowledged load was replayed")
		}
		if e = loader.ReconcileOffline(t.Context(), m); e != nil {
			t.Fatal("actual independent image inspect did not reconcile", e)
		}
		if loads.Load() != 1 {
			t.Fatal("actual Docker mutation was repeated")
		}
	})
}

// These refusals use an actual exported manufacturer's signed image manifest.
// No Docker request, synthetic success signature or runtime signing key is used.
func TestActualRegistryProofRefusals(t *testing.T) {
	c, m, _, _ := registryNodeInputs(t)
	loader := &Loader{config: c}
	if e := loader.verifyProof(t.Context(), m.Image); e != nil {
		t.Fatal("actual source proof required", e)
	}
	for _, fault := range []string{"missing", "wrong-asset", "signature", "manifest", "source-manifest", "image-manifest", "image-config", "architecture"} {
		t.Run(fault, func(t *testing.T) {
			image := m.Image
			var proof bundle.ImageProof
			if release.StrictJSON(image.ReleaseProof, &proof) != nil {
				t.Fatal("invalid actual fixture proof")
			}
			switch fault {
			case "missing":
				image.ReleaseProof = nil
			case "wrong-asset":
				proof.AssetID = "not-in-manufacturer-release"
			case "signature":
				proof.Signature = append([]byte(nil), proof.Signature...)
				proof.Signature[len(proof.Signature)/2] ^= 1
			case "manifest":
				proof.Manifest = append(append([]byte(nil), proof.Manifest...), ' ')
			case "source-manifest":
				image.SourceManifestSHA256 = strings.Repeat("0", 64)
			case "image-manifest":
				image.ImageManifestDigest = "sha256:" + strings.Repeat("0", 64)
			case "image-config":
				image.ImageConfigDigest = "sha256:" + strings.Repeat("0", 64)
			case "architecture":
				image.Architecture = "not-an-approved-architecture"
			}
			if fault == "wrong-asset" || fault == "signature" || fault == "manifest" {
				var e error
				image.ReleaseProof, e = json.Marshal(proof)
				if e != nil {
					t.Fatal(e)
				}
			}
			if e := loader.verifyProof(t.Context(), image); e == nil {
				t.Fatal("invalid signed binding admitted before node load")
			}
		})
	}
}
