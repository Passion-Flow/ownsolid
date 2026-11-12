package install

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The overlay reuses the real certificate and protected-input test helpers.
func TestTemporaryCombinedHistoryPreparation(t *testing.T) {
	_, c := historyFixture(t)
	base := "/evidence/history-inputs"
	if err := os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) string {
		path := filepath.Join(base, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	c.Root = "/srv/fleet-history"
	c.Project = "lane-del-history"
	c.Subnet = "172.29.244.0/28"
	c.UID = 10001
	c.GID = 10001
	c.MetricsImage = os.Getenv("HISTORY_METRICS_IMAGE")
	c.EvaluatorImage = os.Getenv("HISTORY_EVALUATOR_IMAGE")
	c.RequestSeconds = 180
	c.Raw.Listen = "10.77.0.1:18428"
	c.Long.Listen = "10.77.0.1:28428"
	c.Long.Retention = "100d"
	c.EvaluatorListen = "127.0.0.1:18880"
	var roots []byte
	for _, s := range []struct {
		name, ip string
		store    *historyStoreConfig
	}{
		{"raw", "172.29.244.2", &c.Raw}, {"long", "172.29.244.3", &c.Long},
	} {
		ca, key, err := generateCA(s.name + "-combined-fixture")
		if err != nil {
			t.Fatal(err)
		}
		cert, leaf, err := issueServerCertificate(ca, key, []string{"10.77.0.1", s.ip})
		if err != nil {
			t.Fatal(err)
		}
		s.store.TLS = historyTLSFiles{write(s.name+"-ca.pem", ca), write(s.name+"-certificate.pem", cert), write(s.name+"-private-key.pem", leaf)}
		roots = append(roots, ca...)
	}
	c.ControlCAFile = write("control-ca.pem", roots)
	rules, err := os.ReadFile("/bundle/fleet-rollup-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c.RulesFile = write("rules.yaml", rules)
	c.RulesSHA256 = fmt.Sprintf("%x", sha256.Sum256(rules))
	policy, err := json.Marshal(map[string]any{"format_version": 1, "operation_seconds": 10, "fleet_history": historyPolicy{
		RawOrigin: "https://" + c.Raw.Listen, LongOrigin: "https://" + c.Long.Listen, RawHorizonSeconds: 3600, MaxRangeSeconds: 7776000, SettlingSeconds: 30,
	}})
	if err != nil {
		t.Fatal(err)
	}
	c.MonitoringFile = write("monitoring.json", policy)
	config := filepath.Join(base, "site.yaml")
	writeHistoryFixture(t, config, c)
	if err := ValidateFleetHistory(config); err != nil {
		t.Fatal(err)
	}
	fmt.Println("HISTORY_INPUTS_VALIDATED")
}
