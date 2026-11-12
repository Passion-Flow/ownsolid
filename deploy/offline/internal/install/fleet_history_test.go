package install

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"go.yaml.in/yaml/v3"
)

func historyFixture(t *testing.T) (string, fleetHistoryConfig) {
	t.Helper()
	input := shortTempDir(t)
	c := fleetHistoryConfig{
		FormatVersion: 1, Root: filepath.Join(input, "deployment"), Project: "history-test", UID: os.Getuid(), GID: os.Getgid(),
		Subnet: "172.29.248.0/28", MetricsImage: "sha256:" + strings.Repeat("a", 64),
		EvaluatorImage: "sha256:" + strings.Repeat("b", 64), EvaluatorListen: "127.0.0.1:18880",
		Raw:            historyStoreConfig{Listen: "127.0.0.1:18428", Retention: "2d"},
		Long:           historyStoreConfig{Listen: "127.0.0.1:28428", Retention: "90d"},
		StoreResources: historyResources{"256m", "1", 128}, EvaluatorResources: historyResources{"128m", "1", 128},
		RequestSeconds: 10,
	}
	if c.UID == 0 {
		c.UID = 10001
		c.GID = 10001
	}
	write := func(name string, raw []byte) string {
		path := filepath.Join(input, name)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	rawCA, rawKey, err := generateCA("raw-fixture")
	if err != nil {
		t.Fatal(err)
	}
	longCA, longKey, err := generateCA("long-fixture")
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []struct {
		name, internal string
		config         *historyStoreConfig
		ca, key        []byte
	}{
		{"raw", "172.29.248.2", &c.Raw, rawCA, rawKey}, {"long", "172.29.248.3", &c.Long, longCA, longKey},
	} {
		cert, key, err := issueServerCertificate(store.ca, store.key, []string{"127.0.0.1", store.internal})
		if err != nil {
			t.Fatal(err)
		}
		store.config.TLS = historyTLSFiles{write(store.name+"-ca.pem", store.ca), write(store.name+"-cert.pem", cert), write(store.name+"-key.pem", key)}
	}
	c.ControlCAFile = write("control-ca.pem", append(rawCA, longCA...))
	c.RulesFile = write("rules.yaml", []byte("groups: []\n"))
	c.RulesSHA256 = wire.Digest([]byte("groups: []\n"))
	policy := map[string]any{"format_version": 1, "fleet_history": historyPolicy{
		RawOrigin: "https://" + c.Raw.Listen, LongOrigin: "https://" + c.Long.Listen,
		RawHorizonSeconds: 3600, MaxRangeSeconds: 86400, SettlingSeconds: 60,
	}}
	c.MonitoringFile = write("monitoring.json", historyJSON(policy))
	path := filepath.Join(input, "site.yaml")
	writeHistoryFixture(t, path, c)
	return path, c
}

func writeHistoryFixture(t *testing.T, path string, c fleetHistoryConfig) {
	t.Helper()
	raw, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestFleetHistoryInputRefusals(t *testing.T) {
	for _, fault := range []string{"missing", "symlink", "writable", "unpinned", "public", "same-listener", "wrong-san", "rules", "policy", "untrusted", "bad-key", "expired-ca"} {
		t.Run(fault, func(t *testing.T) {
			path, c := historyFixture(t)
			switch fault {
			case "missing":
				c.Long.TLS.CAFile = filepath.Join(filepath.Dir(path), "missing")
			case "symlink":
				link := filepath.Join(filepath.Dir(path), "link")
				if err := os.Symlink(c.Raw.TLS.CAFile, link); err != nil {
					t.Fatal(err)
				}
				c.Raw.TLS.CAFile = link
			case "writable":
				os.Chmod(c.Raw.TLS.CAFile, 0660)
			case "unpinned":
				c.EvaluatorImage = "victoriametrics/vmalert:v1.155.0"
			case "public":
				c.Raw.Listen = "0.0.0.0:18428"
			case "same-listener":
				c.Long.Listen = c.Raw.Listen
			case "wrong-san":
				c.Raw.Listen = "127.0.0.2:18428"
			case "rules":
				c.RulesSHA256 = strings.Repeat("f", 64)
			case "policy":
				c.Long.Listen = "127.0.0.1:28429"
			case "untrusted":
				c.Raw.TLS.CAFile = c.Long.TLS.CAFile
			case "bad-key":
				c.Raw.TLS.PrivateKeyFile = c.Long.TLS.PrivateKeyFile
			case "expired-ca":
				ca, key, err := generateCA("expired-fixture")
				if err != nil {
					t.Fatal(err)
				}
				cert, signer, err := parseAuthority(ca, key)
				if err != nil {
					t.Fatal(err)
				}
				cert.NotBefore = time.Now().Add(-48 * time.Hour)
				cert.NotAfter = time.Now().Add(-24 * time.Hour)
				der, err := x509.CreateCertificate(rand.Reader, cert, cert, &signer.PublicKey, signer)
				if err != nil {
					t.Fatal(err)
				}
				os.WriteFile(c.Raw.TLS.CAFile, encodeCertificate(der), 0600)
			}
			writeHistoryFixture(t, path, c)
			if _, err := loadHistorySite(path); err == nil {
				t.Fatal("invalid input was admitted")
			}
			if _, err := os.Stat(c.Root); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("input validation created a deployment")
			}
		})
	}
}

func TestFleetHistoryImmutableGenerationAndIssuerOverlap(t *testing.T) {
	path, c := historyFixture(t)
	old, err := loadHistorySite(path)
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration, err := old.prepare()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := old.prepare(); err != nil || again != oldGeneration {
		t.Fatal("generation retry", err)
	}
	newCA, newKey, err := generateCA("next-issuer")
	if err != nil {
		t.Fatal(err)
	}
	oldCA, _ := os.ReadFile(c.Long.TLS.CAFile)
	newLeaf, key, err := issueServerCertificate(newCA, newKey, []string{"127.0.0.1", "172.29.248.3"})
	if err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string][]byte{c.Long.TLS.CAFile: append(oldCA, newCA...), c.Long.TLS.CertificateFile: newLeaf, c.Long.TLS.PrivateKeyFile: key} {
		if err := os.WriteFile(path, contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	rawCA, _ := os.ReadFile(c.Raw.TLS.CAFile)
	os.WriteFile(c.ControlCAFile, append(append(rawCA, oldCA...), newCA...), 0600)
	overlap, err := loadHistorySite(path)
	if err != nil || overlap.generation == old.generation {
		t.Fatal("trusted overlap", err)
	}
	os.WriteFile(c.Long.TLS.CAFile, newCA, 0600)
	os.WriteFile(c.ControlCAFile, append(rawCA, newCA...), 0600)
	next, err := loadHistorySite(path)
	if err != nil || next.generation == overlap.generation {
		t.Fatal("old issuer removal", err)
	}
	if _, err := next.prepare(); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(oldGeneration, "long/private-key.pem")) {
		t.Fatal("prior recovery generation removed")
	}
	target := filepath.Join(oldGeneration, "raw/ca.pem")
	if err := os.WriteFile(target, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := old.prepare(); err == nil {
		t.Fatal("changed immutable inputs accepted")
	}
}

func TestFleetHistoryRestartFailureRetryAndOwnerScope(t *testing.T) {
	path, _ := historyFixture(t)
	site, err := loadHistorySite(path)
	if err != nil {
		t.Fatal(err)
	}
	probe := func(context.Context, historySite) error { return nil }
	runner := &fakeRunner{}
	if err := applyHistory(t.Context(), site, runner, &bytes.Buffer{}, probe); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(runner.commands, "\n"), " pull ") {
		t.Fatal("deployment pulled images")
	}
	unlock, err := lockOperation(site.installerConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := applyHistory(t.Context(), site, &fakeRunner{}, &bytes.Buffer{}, probe); err == nil || !strings.Contains(err.Error(), "INSTALL_BUSY") {
		t.Fatal("concurrent apply", err)
	}
	unlock()
	runner = &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, " up ") {
			return "", errors.New("interrupted start")
		}
		return "", nil
	}}
	if err := applyHistory(t.Context(), site, runner, &bytes.Buffer{}, probe); err == nil {
		t.Fatal("interrupted start reported success")
	}
	commands := strings.Join(runner.commands, "\n")
	if !strings.Contains(commands, " stop evaluator raw long") {
		t.Fatal("trust clients were not drained")
	}
	if err := applyHistory(t.Context(), site, &fakeRunner{}, &bytes.Buffer{}, probe); err != nil {
		t.Fatal("retry", err)
	}
	site.config.Project = "another-project"
	another := &fakeRunner{}
	if err := applyHistory(t.Context(), site, another, &bytes.Buffer{}, probe); err == nil || len(another.commands) != 0 {
		t.Fatal("foreign deployment owner", err)
	}
}

func TestFleetHistoryRefusesForeignDockerProject(t *testing.T) {
	path, _ := historyFixture(t)
	site, err := loadHistorySite(path)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, "container ls") {
			return "foreign-container", nil
		}
		return "{}", nil
	}}
	err = applyHistory(t.Context(), site, runner, &bytes.Buffer{}, func(context.Context, historySite) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "INSTALL_HISTORY_OWNER_MISMATCH") {
		t.Fatal("foreign project was accepted", err)
	}
	if strings.Contains(strings.Join(runner.commands, "\n"), " stop ") {
		t.Fatal("foreign project was stopped")
	}
}
