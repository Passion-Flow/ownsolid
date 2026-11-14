package main

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestActualStoreInitializationCLIWithoutUnrelatedAuthOrWorkerAssets(t *testing.T) {
	s := identityfixture.New(t)
	identityfixture.Bootstrap(t, s)
	ctx := context.Background()
	root := t.TempDir()
	os.Chmod(root, 0700)
	write := func(name string, b []byte) string {
		t.Helper()
		p := filepath.Join(root, name)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		return p
	}
	cc := s.DB.Config().ConnConfig
	q := url.Values{"sslmode": {"disable"}}
	for k, v := range cc.RuntimeParams {
		q.Set(k, v)
	}
	uri := url.URL{Scheme: "postgres", User: url.UserPassword(cc.User, cc.Password), Host: net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))), Path: "/" + cc.Database, RawQuery: q.Encode()}
	c := s.Config
	c.Server.ListenAddress = "127.0.0.1:18443"
	c.Database.DSNFile = write("operator-dsn", []byte(uri.String()))
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.Host = "smtp.example.invalid"
	c.SMTP.Port = 587
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	c.Models.ArtifactRoot = filepath.Join(root, "models")
	// Explicit synthetic artifact identities exercise configuration validation only.
	// No worker/model/frontend/auth/mail/locale artifact is executed or loaded.
	c.Datasets = config.DatasetConfiguration{ArtifactRoot: filepath.Join(root, "datasets"), PythonExecutable: filepath.Join(root, "absent-python"), ProcessorFile: filepath.Join(root, "absent-processor.py")}
	c.Training = config.TrainingConfiguration{ArtifactRoot: filepath.Join(root, "training"), ImageReference: "projectone/training@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
	c.ModelPreparation = config.ModelPreparationConfiguration{ArtifactRoot: filepath.Join(root, "preparations"), ImageReference: "projectone/model-lifecycle@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
	for _, p := range []string{c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	configuration := write("operator.yaml", raw)
	args := os.Args
	defer func() { os.Args = args }()
	for _, store := range []struct{ kind, root, action string }{{"dataset", c.Datasets.ArtifactRoot, "dataset.store_initialize_verified"}, {"training", c.Training.ArtifactRoot, "training.store_initialize_verified"}, {"preparation", c.ModelPreparation.ArtifactRoot, "model.preparation_store_initialize_verified"}} {
		call := func(command string) error {
			os.Args = []string{"control", command, "--config", configuration, "--" + store.kind + "-store-root", store.root, "--owner-uid", strconv.Itoa(os.Geteuid()), "--owner-gid", strconv.Itoa(os.Getegid()), "--reason", "Synthetic isolated CLI initialization", "--confirm-empty-" + store.kind + "-store"}
			return run()
		}
		if e = call("initialize-" + store.kind + "-store"); e != nil {
			t.Fatal(store.kind, e)
		}
		if e = call("initialize-" + store.kind + "-store"); e == nil {
			t.Fatal("CLI replaced namespace")
		}
		for _, mode := range []string{"check", "record", "check"} {
			if e = call(mode + "-" + store.kind + "-store-initialization"); e != nil {
				t.Fatal(mode, store.kind, e)
			}
		}
		var count int
		if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action=$1", store.action).Scan(&count); e != nil || count != 1 {
			t.Fatal("CLI audit", count, e)
		}
	}
}
