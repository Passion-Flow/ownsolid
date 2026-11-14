package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

func knowledgeFixture() KnowledgeConfiguration {
	return KnowledgeConfiguration{Enabled: true, PythonExecutable: "/synthetic/runtime/bin/python", WorkerFile: "/synthetic/code/knowledge/embed.py", ModelDirectory: "/synthetic/models/encoder", ScratchRoot: "/synthetic/scratch/knowledge", Gateways: []KnowledgeGateway{{Origin: "https://gateway.example.invalid:9443", DialAddresses: []string{"127.0.0.1", "::1"}, CAFile: "/synthetic/private/gateway-ca.pem"}}}
}
func TestM12KnowledgeTypedPathsDestinationsAndDisabledMode(t *testing.T) {
	c := knowledgeFixture()
	if e := c.Validate("/synthetic/datasets"); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*KnowledgeConfiguration){func(c *KnowledgeConfiguration) { c.PythonExecutable = "" }, func(c *KnowledgeConfiguration) { c.ModelDirectory = "relative" }, func(c *KnowledgeConfiguration) { c.ScratchRoot = "/synthetic/models" }, func(c *KnowledgeConfiguration) { c.Gateways[0].Origin = "http://gateway.example.invalid" }, func(c *KnowledgeConfiguration) { c.Gateways[0].DialAddresses = []string{"169.254.169.254"} }, func(c *KnowledgeConfiguration) { c.Gateways[0].DialAddresses = []string{"0.0.0.0"} }, func(c *KnowledgeConfiguration) { c.Gateways[0].DialAddresses = []string{"::ffff:127.0.0.1"} }, func(c *KnowledgeConfiguration) { c.Gateways[0].CAFile = "../key" }} {
		bad := c
		bad.Gateways = append([]KnowledgeGateway(nil), c.Gateways...)
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe knowledge configuration accepted")
		}
	}
	if c.Validate("/synthetic/scratch") == nil {
		t.Fatal("overlapping mutable stores")
	}
	if e := (KnowledgeConfiguration{}).Validate(); e != nil {
		t.Fatal("disabled default", e)
	}
	c.Enabled = false
	c.ModelDirectory = "/synthetic/missing/encoder"
	if e := c.Validate(); e != nil {
		t.Fatal("disabled parsing must not access missing files", e)
	}
}
func TestM12KnowledgePolicyDefaultsOverridesAndStrictYAML(t *testing.T) {
	p := DefaultKnowledgePolicy()
	if e := p.Validate(); e != nil {
		t.Fatal(e)
	}
	if e := yaml.Unmarshal([]byte("query_count: 7\nquery_window_seconds: 120\nchunk_tokens: 64\noverlap_tokens: 8\nminimum_score: -0.1\nworker_threads: 1\n"), &p); e != nil || p.ChunkTokens != 64 || p.OverlapTokens != 8 || p.WorkerThreads != 1 || p.QuerySeconds != 60 || p.MinimumScore != -0.1 || p.QueryCount != 7 || p.QueryWindowSeconds != 120 {
		t.Fatal("override/default preservation", e)
	}
	for _, raw := range []string{"knowledge: null", "knowledge: {enabled: 1}", "knowledge: {gateways: null}", "knowledge: {unknown: true}", "knowledge: {gateways: [null]}", "knowledge: {gateways: [{dial_addresses: [1]}]}", "knowledge: {enabled: false, enabled: true}", "runtime: {knowledge: null}", "runtime: {knowledge: {chunk_tokens: 1.5}}", "runtime: {knowledge: {worker_threads: true}}", "runtime: {knowledge: {minimum_score: '0.1'}}", "runtime: {knowledge: {minimum_score: null}}", "knowledge: &k {enabled: false}\nruntime: {knowledge: *k}"} {
		if validateKnowledgeYAML([]byte(raw)) == nil {
			t.Fatalf("invalid owned YAML accepted: %s", raw)
		}
	}
	for _, mutate := range []func(*KnowledgePolicy){func(p *KnowledgePolicy) { p.QueryCount = 0 }, func(p *KnowledgePolicy) { p.QueryWindowSeconds = 3601 }, func(p *KnowledgePolicy) { p.ChunkTokens = 255 }, func(p *KnowledgePolicy) { p.OverlapTokens = p.ChunkTokens }, func(p *KnowledgePolicy) { p.SourceBytes = 2097153 }, func(p *KnowledgePolicy) { p.MinimumScore = math.NaN() }, func(p *KnowledgePolicy) { p.WorkerMemoryBytes = 1 }, func(p *KnowledgePolicy) { p.LeaseSeconds = p.IngestSeconds }, func(p *KnowledgePolicy) { p.KeyTTLSeconds = 299 }, func(p *KnowledgePolicy) { p.Concurrency = 3 }, func(p *KnowledgePolicy) { p.MaximumTopK = 0 }} {
		bad := p
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe policy accepted")
		}
	}
	c := validConfig()
	c.Knowledge = knowledgeFixture()
	policy := DefaultRuntimePolicy()
	c.Runtime = &policy
	if c.Validate() == nil {
		t.Fatal("interactive and startup budgets must fit actual worker")
	}
	policy.HTTP.RequestSeconds = 90
	policy.HTTP.StartupSeconds = 60
	policy.HTTP.ShutdownSeconds = 20
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(t.TempDir(), "control.yaml")
	if e = os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(file)
	if e != nil || loaded.Policy().Knowledge.ChunkTokens != 192 || !loaded.Knowledge.Enabled {
		t.Fatal("actual configuration loader", e)
	}
	var values map[string]any
	if yaml.Unmarshal(raw, &values) != nil {
		t.Fatal("fixture decode")
	}
	values["knowledge"] = nil
	raw, e = yaml.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Load(file); e == nil {
		t.Fatal("actual loader accepted null owned knowledge group")
	}
}

func TestM12KnowledgeShutdownBudgetIncludesIndependentKeyCleanup(t *testing.T) {
	c := validConfig()
	c.Knowledge = knowledgeFixture()
	policy := DefaultRuntimePolicy()
	policy.HTTP.RequestSeconds = 120
	policy.HTTP.StartupSeconds = 60
	c.Runtime = &policy
	policy.HTTP.ShutdownSeconds = 14
	if c.Validate() == nil {
		t.Fatal("shutdown omitted independent key cleanup and commit budget")
	}
	policy.HTTP.ShutdownSeconds = 15
	if e := c.Validate(); e != nil {
		t.Fatal("default cleanup boundary", e)
	}
	policy.Knowledge.KeyCleanupSeconds = 30
	policy.HTTP.ShutdownSeconds = 39
	if c.Validate() == nil {
		t.Fatal("nondefault independent cleanup can outlive HTTP shutdown")
	}
	policy.HTTP.ShutdownSeconds = 40
	if e := c.Validate(); e != nil {
		t.Fatal("explicit nondefault bounded cleanup", e)
	}
}
