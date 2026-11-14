package objectstore

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func protocolS3(t *testing.T, handler http.HandlerFunc) (*S3, *httptest.Server) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(root, 0700) != nil {
		t.Fatal("private test root", e)
	}
	stage := filepath.Join(root, "stage")
	if os.Mkdir(stage, 0700) != nil {
		t.Fatal("stage")
	}
	refusalAccess, refusalSecret := filepath.Join(root, "refusal-access"), filepath.Join(root, "refusal-secret")
	access, secret, ca := filepath.Join(root, "access"), filepath.Join(root, "secret"), filepath.Join(root, "ca.pem")
	for p, b := range map[string][]byte{refusalAccess: []byte("SYNTHETIC_REFUSAL_ACCESS_ONLY"), refusalSecret: []byte("SYNTHETIC_REFUSAL_SECRET_ONLY"), access: []byte("SYNTHETIC_ACCESS_ONLY"), secret: []byte("SYNTHETIC_SECRET_ONLY"), ca: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})} {
		if os.WriteFile(p, b, 0600) != nil {
			t.Fatal("protected test input")
		}
	}
	c := config.ObjectStorageConfiguration{Enabled: true, Backend: "seaweedfs_s3", Endpoint: server.URL, Transport: "https", Region: "private", Bucket: "synthetic-artifact-fixture", RefusalBucket: "synthetic-refusal-fixture", RefusalAccessKeyFile: refusalAccess, RefusalSecretKeyFile: refusalSecret, AccessKeyFile: access, SecretKeyFile: secret, CAFile: ca, DialAddresses: []string{"127.0.0.1"}, StagingRoot: stage, PartBytes: 5 << 20, MaximumObjectBytes: 10 << 20, MinimumFreeBytes: 4096, OperationSeconds: 5, TransferSeconds: 10, UploadSeconds: 60, ObjectLifetimeSeconds: 3600, MaximumGrantSeconds: 300, MaximumConcurrentTransfers: 2, MaximumListPages: 2, WorkerPollSeconds: 1, Management: protocolManagementFixtures()}
	client, e := NewS3(c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { client.Close() })
	return client, server
}
func protocolRef() Ref { return Ref{uuid.NewString(), uuid.NewString(), uuid.NewString()} }

// This is explicitly an SDK/protocol fault fixture. It is not SeaweedFS, cloud,
// persistence, deployment/security-profile or target qualification evidence.
func TestS3SDKEmbeddedCompleteErrorNeverRetriesOrReturnsSuccess(t *testing.T) {
	var calls atomic.Int32
	client, _ := protocolS3(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.Header.Get("If-None-Match") != "*" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("actual signed conditional complete missing")
		}
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, `<Error><Code>InternalError</Code><Message>sensitive-value</Message></Error>`)
	})
	out, e := client.Complete(context.Background(), protocolRef(), "opaque-owned-upload", []Part{{Number: 1, Bytes: 100, SHA256: strings.Repeat("a", 64), ETag: `"opaque"`}})
	if e == nil || out.ETag != "" || calls.Load() != 1 || strings.Contains(e.Error(), "sensitive-value") {
		t.Fatal("embedded error treated as success, retried or leaked", e, calls.Load())
	}
}
func TestS3SDKRedirectCannotChangeOriginOrExposeCredentials(t *testing.T) {
	var foreign atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreign.Add(1) }))
	defer destination.Close()
	client, _ := protocolS3(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	})
	out, e := client.Head(context.Background(), protocolRef())
	if e == nil || out.ETag != "" || foreign.Load() != 0 {
		t.Fatal("redirect followed", e)
	}
}
func TestS3SDKExactMultipartScopeAndTruncatedListFailClosed(t *testing.T) {
	ref := protocolRef()
	key, _ := ref.Key()
	var calls atomic.Int32
	client, _ := protocolS3(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !strings.Contains(r.URL.Path, "synthetic-artifact-fixture") {
			t.Error("bucket path missing")
		}
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, `<ListMultipartUploadsResult><Bucket>synthetic-artifact-fixture</Bucket><IsTruncated>false</IsTruncated><Upload><Key>`+key+`/foreign</Key><UploadId>not-owned</UploadId></Upload><Upload><Key>`+key+`</Key><UploadId>exact-owned</UploadId></Upload></ListMultipartUploadsResult>`)
	})
	out, e := client.FindUploads(context.Background(), ref)
	if e != nil || len(out) != 1 || out[0].ID != "exact-owned" || calls.Load() != 1 {
		t.Fatal("prefix mistaken for exact scope", e)
	}
	if _, e = (Ref{Tenant: "..", Workspace: ref.Workspace, Object: ref.Object}).Key(); e == nil {
		t.Fatal("path accepted instead of UUID")
	}
}

func protocolManagementFixtures() []config.ObjectStorageManagementProbe {
	out := []config.ObjectStorageManagementProbe{}
	for index, kind := range []string{"s3_grpc", "master_grpc", "filer_grpc", "volume_grpc"} {
		out = append(out, config.ObjectStorageManagementProbe{Kind: kind, Authority: "management.example.invalid:" + strconv.Itoa(10443+index), DialAddresses: []string{"127.0.0.1"}, CAFile: "/protected/management-ca", ClientCertificateFile: "/protected/management-certificate", ClientKeyFile: "/protected/management-key"})
	}
	return out
}
