package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

type artifactServer struct {
	mu       sync.Mutex
	content  []byte
	offsets  []int64
	failAt   int64
	corrupt  bool
	lease    string
	approved wire.KubernetesArtifact
}

func (s *artifactServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var input wire.KubernetesArtifactRequest
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.LeaseToken != s.lease || input.Name != s.approved.Name || input.Offset%wire.KubernetesTransferChunkBytes != 0 {
		w.WriteHeader(400)
		return
	}
	s.offsets = append(s.offsets, input.Offset)
	if s.failAt >= 0 && input.Offset == s.failAt {
		s.failAt = -1
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(404)
		w.Write([]byte(`{"code":"RESOURCE_NOT_FOUND","status":404}`))
		return
	}
	chunk := s.content[input.Offset : input.Offset+input.Length]
	if s.corrupt {
		chunk = append([]byte("X"), chunk[1:]...)
	}
	sum := sha256.Sum256(chunk)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
	w.Header().Set("X-Artifact-SHA256", hex.EncodeToString(sum[:]))
	w.Write(chunk)
}

func TestKubernetesArtifactResumesAndVerifies(t *testing.T) {
	content := make([]byte, 2*wire.KubernetesTransferChunkBytes+12345)
	for i := range content {
		content[i] = byte(i * 7)
	}
	sum := sha256.Sum256(content)
	artifact := wire.KubernetesArtifact{Name: "rke2.linux-amd64.tar.gz", SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
	lease := strings.Repeat("A", 43)
	handler := &artifactServer{content: content, failAt: wire.KubernetesTransferChunkBytes, lease: lease, approved: artifact}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	root := t.TempDir()
	ca := filepath.Join(root, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600)
	c, e := New(server.URL, ca)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.Credential = strings.Repeat("B", 43)
	path := "/agent/v1/nodes/12345678-1234-1234-1234-123456789abc/tasks/22345678-1234-1234-1234-123456789abc/kubernetes-artifacts"
	destination := filepath.Join(root, artifact.Name)
	// The second range fails; the first stays in the partial file.
	if e = c.kubernetesRanges(context.Background(), path, lease, artifact, destination); e == nil {
		t.Fatal("a failed range succeeded")
	}
	if _, e = os.Stat(destination); !os.IsNotExist(e) {
		t.Fatal("an incomplete artifact was put in place")
	}
	// A torn write after the whole range is dropped on resume.
	partial, _ := os.OpenFile(destination+".partial", os.O_APPEND|os.O_WRONLY, 0o600)
	partial.Write([]byte("torn"))
	partial.Close()
	if e = c.kubernetesRanges(context.Background(), path, lease, artifact, destination); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(destination)
	if string(got) != string(content) {
		t.Fatal("artifact differs")
	}
	want := []int64{0, wire.KubernetesTransferChunkBytes, wire.KubernetesTransferChunkBytes, 2 * wire.KubernetesTransferChunkBytes}
	if len(handler.offsets) != len(want) {
		t.Fatalf("offsets %v", handler.offsets)
	}
	for i := range want {
		if handler.offsets[i] != want[i] {
			t.Fatalf("offsets %v", handler.offsets)
		}
	}
	if _, e = os.Stat(destination + ".partial"); !os.IsNotExist(e) {
		t.Fatal("partial file kept")
	}
	// Ranges whose bytes match their own digest but not the artifact's are refused whole.
	handler.corrupt, handler.offsets = true, nil
	other := filepath.Join(root, "other")
	if e = c.kubernetesRanges(context.Background(), path, lease, artifact, other); e == nil || e.Error() != "KUBERNETES_ASSET_INVALID" {
		t.Fatalf("corrupt artifact: %v", e)
	}
	for _, name := range []string{other, other + ".partial"} {
		if _, e = os.Stat(name); !os.IsNotExist(e) {
			t.Fatalf("%s kept", name)
		}
	}
}

func TestKubernetesArtifactRefusesUnapprovedFilesAndPaths(t *testing.T) {
	c := &Client{}
	approved := wire.ApprovedKubernetesRelease().Binary
	path := "/agent/v1/nodes/12345678-1234-1234-1234-123456789abc/tasks/22345678-1234-1234-1234-123456789abc/kubernetes-artifacts"
	altered := approved
	altered.SHA256 = strings.Repeat("0", 64)
	for name, call := range map[string]func() error{
		"altered digest": func() error {
			return c.KubernetesArtifact(context.Background(), path, "lease", altered, t.TempDir()+"/x")
		},
		"other path": func() error {
			return c.KubernetesArtifact(context.Background(), path+"/../artifact", "lease", approved, t.TempDir()+"/x")
		},
		"query": func() error {
			return c.KubernetesArtifact(context.Background(), path+"?host=outside", "lease", approved, t.TempDir()+"/x")
		},
	} {
		if e := call(); e == nil || e.Error() != "KUBERNETES_ASSET_INVALID" {
			t.Errorf("%s: %v", name, e)
		}
	}
}
