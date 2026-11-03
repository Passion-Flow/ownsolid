//go:build dataset_processor_integration && mongodb_integration && repository_integration

package datasets

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/mongodatasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	accountfixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/mongodbfixture"
)

// This root owns one actual local replica set and then traverses the production
// reader, selected PostgreSQL metadata, CPython processor and immutable M08 files.
// The old explicitly provisioned source test remains a separate unchanged entry.
func TestMongoDBActualNativeTLSImportAndRefusals(t *testing.T) {
	f := mongodbfixture.New(t)
	source := f.Source
	if !source.TLS || source.CAFile == "" {
		t.Fatal("actual native gate requires explicit TLS and CA")
	}
	python, processorFile := os.Getenv("PROJECT_ONE_DATA_PYTHON"), os.Getenv("PROJECT_ONE_DATA_PROCESSOR_FILE")
	if python == "" || processorFile == "" {
		t.Fatal("exact retained native data runtime/source inputs required")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	processor, err := NewProcessor(config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: python, ProcessorFile: processorFile}, config.DefaultDatasetPolicy())
	if err != nil {
		t.Fatal(err)
	}
	account := accountfixture.New(t)
	password := accountfixture.Bootstrap(t, account)
	a := accountfixture.Login(t, account, "admin@example.invalid", password, "admin")
	portal := accountfixture.Login(t, account, "admin@example.invalid", password, "portal")
	if err = InitializeStore(root, a.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(root, a.Me.Workspace.TenantID, 4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := &Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}, Store: store, Processor: processor}
	policy := config.DefaultMongoDBConfiguration()
	policy.Enabled = true
	policy.BatchSize = 2
	policy.TimeoutSeconds = 2
	for _, name := range []string{"wrong_ca", "wrong_password", "foreign_collection"} {
		bad := f.NegativeSource(t, name)
		result, e := mongodatasets.Read(context.Background(), bad, policy, mongodatasets.Mapping{Prompt: "question", Completion: "answer", Conversion: "string"}, s.policy().Limits, false)
		if e == nil || result.Rows != 0 || len(result.Data) != 0 {
			t.Fatalf("actual MongoDB %s did not fail closed without partial data", name)
		}
	}
	ca, err := config.MongoDBCA(source.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("fixture CA invalid")
	}
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Duration(f.Input.Policy.QuerySeconds) * time.Second}, "tcp", net.JoinHostPort(f.Input.Policy.ListenHost, strconv.Itoa(f.Input.Policy.Port)), &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: "not-the-mongodb-fixture.invalid"})
	if connection != nil {
		_ = connection.Close()
	}
	var hostname x509.HostnameError
	if err == nil || !errors.As(err, &hostname) {
		t.Fatal("actual trusted-chain TLS connection did not reject incorrect DNS SAN")
	}
	assertMongoDBSourceToImmutableVersion(t, source, s, a, portal)
}
