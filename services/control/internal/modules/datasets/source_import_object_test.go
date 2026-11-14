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
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	"github.com/google/uuid"
)

func TestSourceImportS3(t *testing.T) {
	const body = "{\"id\":1,\"text\":\" one \"}\n{\"id\":2,\"text\":null}\n"
	const etag = `"import-fixture"`
	const bucket = "dataset-import-fixture"
	ref := objectstore.Ref{Tenant: uuid.NewString(), Workspace: uuid.NewString(), Object: uuid.NewString()}
	key, err := ref.Key()
	if err != nil {
		t.Fatal(err)
	}
	var mode atomic.Int32
	var calls atomic.Int32
	client := newSourceFixtureS3(t, bucket, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/"+bucket+"/"+key ||
			r.Header.Get("If-Match") != etag || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("missing exact signed conditional GET")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if mode.Load() == 1 {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = io.WriteString(w, "<Error><Code>PreconditionFailed</Code></Error>")
			return
		}
		length := len(body)
		if mode.Load() == 5 {
			length++
		}
		w.Header().Set("Content-Length", strconv.Itoa(length))
		w.Header().Set("ETag", etag)
		if mode.Load() == 6 {
			w.Header().Set("ETag", `"unexpected-etag"`)
		}
		if mode.Load() == 2 {
			_, _ = io.WriteString(w, body[:len(body)/2])
			return
		}
		_, _ = io.WriteString(w, body)
	})
	importer := testImporter(t, testImportLimits(), nil)
	receipt := SourceObject{Ref: ref, ETag: etag, Bytes: int64(len(body)), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(body)))}
	for index, name := range []string{"valid", "changed-etag", "truncated", "wrong-digest", "cleaning"} {
		t.Run(name, func(t *testing.T) {
			mode.Store(int32(index))
			dir := privateImportDir(t)
			path := filepath.Join(dir, "rows.jsonl")
			selected := receipt
			opts := SourceImportOptions{Format: SourceJSONL, SkipInvalid: true}
			if index >= 3 {
				opts.Cleaning = &SourceCleaningPolicy{TrimFields: []string{"text"}, KeepNonBlankFields: []string{"text"}, PreviewRows: 2, PreviewBytes: 1024}
			}
			if index == 3 {
				selected.SHA256 = strings.Repeat("a", 64)
			}
			report, err := importer.ImportObject(t.Context(), client, selected, path, opts)
			switch index {
			case 0:
				if err != nil || report.Rows != 2 || report.FieldCount != 2 ||
					report.SourceSHA256 != receipt.SHA256 || readImported(t, path) != body {
					t.Fatalf("report %+v, error %v", report, err)
				}
			case 4:
				if err != nil || report.Rows != 1 || report.Cleaning == nil || report.Cleaning.ChangedRows != 1 ||
					report.Cleaning.FilteredRows != 1 || len(report.Cleaning.Samples) != 2 ||
					readImported(t, path) != "{\"id\":1,\"text\":\"one\"}\n" {
					t.Fatalf("report %+v, error %v", report, err)
				}
			default:
				if err == nil || report.Cleaning != nil && len(report.Cleaning.Samples) != 0 {
					t.Fatalf("accepted or disclosed an unbound object: %+v, %v", report, err)
				}
				assertNoImports(t, dir)
			}
		})
	}
	const cleanMode = 4
	for _, tc := range []struct {
		name string
		mode int32
		code string
		all  bool
	}{
		{name: "preview-valid", mode: cleanMode},
		{name: "preview-all-filtered", mode: cleanMode, all: true},
		{name: "preview-precondition", mode: 1, code: "OBJECT_CONFLICT"},
		{name: "preview-truncated", mode: 2, code: sourceReadFailedCode},
		{name: "preview-wrong-digest", mode: 3, code: "DATASET_SOURCE_CHANGED"},
		{name: "preview-metadata-length", mode: 5, code: "DATASET_SOURCE_CHANGED"},
		{name: "preview-metadata-etag", mode: 6, code: "OBJECT_CHANGED"},
		{name: "preview-after-metadata-refusal", mode: cleanMode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode.Store(tc.mode)
			dir := privateImportDir(t)
			selected := receipt
			policy := SourceCleaningPolicy{
				TrimFields: []string{"text"}, KeepNonBlankFields: []string{"text"},
				PreviewRows: 2, PreviewBytes: 1024,
			}
			if tc.mode == 3 {
				selected.SHA256 = strings.Repeat("a", 64)
			}
			if tc.all {
				policy.KeepNonBlankFields = []string{"missing"}
			}
			preview, err := importer.PreviewObject(t.Context(), client, selected, dir, SourceImportOptions{Format: SourceJSONL, Cleaning: &policy})
			if tc.code != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.code) || !reflect.DeepEqual(preview, SourceImportReport{}) {
					t.Fatalf("object preview did not fail closed: %+v, %v", preview, err)
				}
			} else {
				if err != nil || preview.Cleaning == nil || len(preview.Cleaning.Samples) != 2 ||
					preview.SourceSHA256 != receipt.SHA256 || preview.SourceBytes != receipt.Bytes || preview.OutputSHA256 != "" {
					t.Fatalf("object preview did not verify its receipt: %+v, %v", preview, err)
				}
				if tc.all {
					if preview.Rows != 0 || preview.OutputBytes != 0 || preview.Cleaning.FilteredRows != 2 ||
						preview.Cleaning.Samples[0].After != nil || preview.Cleaning.Samples[1].After != nil {
						t.Fatal("all-filtered preview did not keep only the original samples")
					}
				} else if preview.Rows != 1 || preview.Cleaning.ChangedRows != 1 || preview.Cleaning.FilteredRows != 1 {
					t.Fatal("object preview returned inconsistent cleaning counts")
				}
			}
			assertNoImports(t, dir)
		})
	}

	dir := privateImportDir(t)
	if _, err := importer.PreviewObject(t.Context(), client, receipt, dir, SourceImportOptions{Format: SourceJSONL}); err == nil ||
		err.Error() != "DATASET_CLEANING_POLICY_INVALID" {
		t.Fatal("object preview accepted a missing policy", err)
	}
	invalid := receipt
	invalid.Ref.Workspace = "../outside"
	opts := SourceImportOptions{Format: SourceJSONL, Cleaning: &SourceCleaningPolicy{TrimFields: []string{"text"}}}
	if _, err := importer.PreviewObject(t.Context(), client, invalid, dir, opts); err == nil ||
		err.Error() != "DATASET_IMPORT_OBJECT_INVALID" {
		t.Fatal("object preview accepted an invalid workspace reference", err)
	}

	if got := calls.Load(); got != 13 {
		t.Fatalf("unexpected request count %d", got)
	}
}

// newSourceFixtureS3 uses the real signed client with an isolated TLS endpoint.
func newSourceFixtureS3(t *testing.T, bucket string, handler http.HandlerFunc) *objectstore.S3 {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
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
	t.Cleanup(func() { _ = client.Close() })
	return client
}
