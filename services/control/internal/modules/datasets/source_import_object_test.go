package datasets

import (
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	"github.com/google/uuid"
)

func TestSourceImportS3(t *testing.T) {
	const body = "{\"id\":1}\n{\"id\":2}\n"
	const etag = `"import-fixture"`
	const bucket = "dataset-import-fixture"
	ref := objectstore.Ref{Tenant: uuid.NewString(), Workspace: uuid.NewString(), Object: uuid.NewString()}
	key, err := ref.Key()
	if err != nil {
		t.Fatal(err)
	}
	var mode atomic.Int32
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/"+bucket+"/"+key ||
			r.Header.Get("If-Match") != etag || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("missing exact signed conditional GET")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if mode.Load() == 1 {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("ETag", etag)
		if mode.Load() == 2 {
			_, _ = io.WriteString(w, body[:len(body)/2])
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	root := privateImportDir(t)
	writeFixture := func(name string, value []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	c := config.ObjectStorageConfiguration{
		Enabled: true, Backend: "seaweedfs_s3", Endpoint: server.URL, Transport: "https",
		Region: "private", Bucket: bucket, RefusalBucket: "dataset-refusal-fixture",
		AccessKeyFile: writeFixture("access", []byte(uuid.NewString())), SecretKeyFile: writeFixture("secret", []byte(uuid.NewString())),
		RefusalAccessKeyFile: writeFixture("refusal-access", []byte(uuid.NewString())), RefusalSecretKeyFile: writeFixture("refusal-secret", []byte(uuid.NewString())),
		CAFile:        writeFixture("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})),
		DialAddresses: []string{"127.0.0.1"}, StagingRoot: filepath.Join(root, "stage"),
		PartBytes: 5 << 20, MaximumObjectBytes: 10 << 20, MinimumFreeBytes: 4096, OperationSeconds: 5, TransferSeconds: 10,
		UploadSeconds: 60, ObjectLifetimeSeconds: 3600, MaximumGrantSeconds: 300, MaximumConcurrentTransfers: 1,
		MaximumListPages: 1, WorkerPollSeconds: 1,
	}
	for index, kind := range []string{"s3_grpc", "master_grpc", "filer_grpc", "volume_grpc"} {
		c.Management = append(c.Management, config.ObjectStorageManagementProbe{
			Kind: kind, Authority: "management.example.invalid:" + strconv.Itoa(10443+index), DialAddresses: []string{"127.0.0.1"},
			CAFile: c.CAFile, ClientCertificateFile: filepath.Join(root, "client.pem"), ClientKeyFile: filepath.Join(root, "client.key"),
		})
	}
	client, err := objectstore.NewS3(c)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	importer := testImporter(t, testImportLimits(), nil)
	receipt := SourceObject{Ref: ref, ETag: etag, Bytes: int64(len(body)), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(body)))}
	opts := SourceImportOptions{Format: SourceJSONL, SkipInvalid: true}
	for index, name := range []string{"valid", "changed-etag", "truncated", "wrong-digest"} {
		t.Run(name, func(t *testing.T) {
			mode.Store(int32(index))
			dir := privateImportDir(t)
			path := filepath.Join(dir, "rows.jsonl")
			selected := receipt
			if index == 3 {
				selected.SHA256 = strings.Repeat("a", 64)
			}
			report, err := importer.ImportObject(t.Context(), client, selected, path, opts)
			if index == 0 {
				if err != nil || report.Rows != 2 || report.FieldCount != 1 ||
					report.SourceSHA256 != receipt.SHA256 || readImported(t, path) != body {
					t.Fatalf("report %+v, error %v", report, err)
				}
			} else {
				if err == nil {
					t.Fatalf("accepted an unbound object: %+v", report)
				}
				assertNoImports(t, dir)
			}
		})
	}
	if got := calls.Load(); got != 4 {
		t.Fatalf("unexpected request count %d", got)
	}
}
