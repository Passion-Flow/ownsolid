package config

import (
	"errors"
	"net/netip"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const MaximumKnowledgeTopK = 100
const MaximumKnowledgeChunkBytes = 65536
const MaximumKnowledgeSourceBytes = 2097152

// KnowledgeConfiguration selects installed code/assets and trusted gateway destinations.
// Original TXT bytes and vectors are persisted atomically in the business database.
type KnowledgeConfiguration struct {
	Enabled          bool               `yaml:"enabled"`
	PythonExecutable string             `yaml:"python_executable"`
	WorkerFile       string             `yaml:"worker_file"`
	ModelDirectory   string             `yaml:"model_directory"`
	ScratchRoot      string             `yaml:"scratch_root"`
	Gateways         []KnowledgeGateway `yaml:"gateways"`
}
type KnowledgeGateway struct {
	Origin        string   `yaml:"origin"`
	DialAddresses []string `yaml:"dial_addresses"`
	CAFile        string   `yaml:"ca_file"`
}

func knowledgePath(value string) bool {
	return value != "/" && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
func (c KnowledgeConfiguration) Validate(otherRoots ...string) error {
	bad := errors.New("CONFIG_KNOWLEDGE_INVALID")
	paths := []string{c.PythonExecutable, c.WorkerFile, c.ModelDirectory, c.ScratchRoot}
	for _, value := range paths {
		if value != "" && !knowledgePath(value) {
			return bad
		}
		if c.Enabled && value == "" {
			return bad
		}
	}
	inside := func(parent, child string) bool {
		return parent != "" && child != "" && (parent == child || strings.HasPrefix(child, parent+string(filepath.Separator)))
	}
	for _, path := range []string{c.PythonExecutable, c.WorkerFile, c.ModelDirectory} {
		if inside(c.ScratchRoot, path) || inside(path, c.ScratchRoot) {
			return bad
		}
	}
	for _, root := range otherRoots {
		if inside(root, c.ScratchRoot) || inside(c.ScratchRoot, root) {
			return bad
		}
	}
	if c.PythonExecutable != "" && c.PythonExecutable == c.WorkerFile {
		return bad
	}
	if len(c.Gateways) > 64 {
		return bad
	}
	seen := map[string]bool{}
	for _, g := range c.Gateways {
		// Gateway destinations use the same canonical HTTPS-origin grammar as SSO.
		if !SSOOrigin(g.Origin) || seen[g.Origin] || len(g.DialAddresses) < 1 || len(g.DialAddresses) > 8 || g.CAFile != "" && !knowledgePath(g.CAFile) {
			return bad
		}
		seen[g.Origin] = true
		addresses := map[string]bool{}
		for _, value := range g.DialAddresses {
			ip, e := netip.ParseAddr(value)
			if e != nil || ip.Zone() != "" || ip.Is4In6() || ip.String() != value || !(ip.IsGlobalUnicast() || ip.IsLoopback()) || addresses[value] {
				return bad
			}
			addresses[value] = true
		}
	}
	return nil
}

type KnowledgePolicy struct {
	QueryCount             int     `yaml:"query_count"`
	QueryWindowSeconds     int     `yaml:"query_window_seconds"`
	ChunkBytes             int     `yaml:"chunk_bytes"`
	SourceBytes            int     `yaml:"source_bytes"`
	SourceTokens           int     `yaml:"source_tokens"`
	MaximumChunks          int     `yaml:"maximum_chunks"`
	ChunkTokens            int     `yaml:"chunk_tokens"`
	OverlapTokens          int     `yaml:"overlap_tokens"`
	QueryBytes             int     `yaml:"query_bytes"`
	QueryTokens            int     `yaml:"query_tokens"`
	TopK                   int     `yaml:"top_k"`
	MaximumTopK            int     `yaml:"maximum_top_k"`
	MinimumScore           float64 `yaml:"minimum_score"`
	MaximumBases           int     `yaml:"maximum_bases"`
	MaximumDocuments       int     `yaml:"maximum_documents"`
	MaximumGenerations     int     `yaml:"maximum_generations"`
	MaximumAttempts        int     `yaml:"maximum_attempts"`
	WorkerThreads          int     `yaml:"worker_threads"`
	WorkerBatchSize        int     `yaml:"worker_batch_size"`
	WorkerMemoryBytes      int64   `yaml:"worker_memory_bytes"`
	Concurrency            int     `yaml:"concurrency"`
	QuerySeconds           int     `yaml:"query_seconds"`
	IngestSeconds          int     `yaml:"ingest_seconds"`
	LeaseSeconds           int     `yaml:"lease_seconds"`
	PollMilliseconds       int     `yaml:"poll_milliseconds"`
	MemoryPollMilliseconds int     `yaml:"memory_poll_milliseconds"`
	ChildStopSeconds       int     `yaml:"child_stop_seconds"`
	CommitSeconds          int     `yaml:"commit_seconds"`
	KeyCleanupSeconds      int     `yaml:"key_cleanup_seconds"`
	KeyTTLSeconds          int     `yaml:"key_ttl_seconds"`
	GatewayConnectSeconds  int     `yaml:"gateway_connect_seconds"`
	GatewayResponseBytes   int     `yaml:"gateway_response_bytes"`
	ContextBytes           int     `yaml:"context_bytes"`
	OutputTokens           int     `yaml:"output_tokens"`
	CleanupSeconds         int     `yaml:"cleanup_seconds"`
	CleanupBatch           int     `yaml:"cleanup_batch"`
	RetentionDays          int     `yaml:"retention_days"`
}

func DefaultKnowledgePolicy() KnowledgePolicy {
	return KnowledgePolicy{QueryCount: 30, QueryWindowSeconds: 60, ChunkBytes: 16384, SourceBytes: 524288, SourceTokens: 131072, MaximumChunks: 2048, ChunkTokens: 192, OverlapTokens: 32, QueryBytes: 4096, QueryTokens: 128, TopK: 5, MaximumTopK: 20, MinimumScore: 0.2, MaximumBases: 32, MaximumDocuments: 100, MaximumGenerations: 32, MaximumAttempts: 3, WorkerThreads: 2, WorkerBatchSize: 8, WorkerMemoryBytes: 1610612736, Concurrency: 1, QuerySeconds: 60, IngestSeconds: 240, LeaseSeconds: 300, PollMilliseconds: 1000, MemoryPollMilliseconds: 500, ChildStopSeconds: 3, CommitSeconds: 10, KeyCleanupSeconds: 5, KeyTTLSeconds: 300, GatewayConnectSeconds: 3, GatewayResponseBytes: 65536, ContextBytes: 4096, OutputTokens: 256, CleanupSeconds: 60, CleanupBatch: 100, RetentionDays: 7}
}
func (p KnowledgePolicy) Validate() error {
	bad := errors.New("CONFIG_KNOWLEDGE_POLICY_INVALID")
	if p.QueryCount < 1 || p.QueryCount > 1000 || p.QueryWindowSeconds < 1 || p.QueryWindowSeconds > 3600 {
		return bad
	}
	if p.ChunkBytes < 1024 || p.ChunkBytes > MaximumKnowledgeChunkBytes || p.SourceBytes < 1024 || p.SourceBytes > MaximumKnowledgeSourceBytes || p.SourceTokens < 32 || p.SourceTokens > 262144 || p.MaximumChunks < 1 || p.MaximumChunks > 4096 || p.ChunkTokens < 8 || p.ChunkTokens > 254 || p.OverlapTokens < 0 || p.OverlapTokens >= p.ChunkTokens || p.QueryBytes < 32 || p.QueryBytes > 32768 || p.QueryTokens < 1 || p.QueryTokens > 254 {
		return bad
	}
	if p.TopK < 1 || p.TopK > p.MaximumTopK || p.MaximumTopK > MaximumKnowledgeTopK || p.MinimumScore < -1 || p.MinimumScore > 1 || p.MinimumScore != p.MinimumScore || p.MaximumBases < 1 || p.MaximumBases > 1000 || p.MaximumDocuments < 1 || p.MaximumDocuments > 1000 || p.MaximumGenerations < 2 || p.MaximumGenerations > 1000 || p.MaximumAttempts < 1 || p.MaximumAttempts > 10 {
		return bad
	}
	if p.WorkerThreads < 1 || p.WorkerThreads > 4 || p.WorkerBatchSize < 1 || p.WorkerBatchSize > 32 || p.WorkerMemoryBytes < 536870912 || p.WorkerMemoryBytes > 4294967296 || p.Concurrency < 1 || p.Concurrency > 2 {
		return bad
	}
	if p.QuerySeconds < 5 || p.QuerySeconds > 120 || p.IngestSeconds < 10 || p.IngestSeconds > 600 || p.LeaseSeconds < p.IngestSeconds+p.ChildStopSeconds+p.CommitSeconds || p.LeaseSeconds > 900 || p.PollMilliseconds < 100 || p.PollMilliseconds > 10000 || p.MemoryPollMilliseconds < 100 || p.MemoryPollMilliseconds > 2000 || p.ChildStopSeconds < 1 || p.ChildStopSeconds > 10 || p.CommitSeconds < 1 || p.CommitSeconds > 60 {
		return bad
	}
	if p.KeyCleanupSeconds < 1 || p.KeyCleanupSeconds > 30 || p.KeyTTLSeconds < 300 || p.KeyTTLSeconds > 900 || p.GatewayConnectSeconds < 1 || p.GatewayConnectSeconds > 30 || p.GatewayResponseBytes < 4096 || p.GatewayResponseBytes > 1048576 || p.ContextBytes < 256 || p.ContextBytes > 32768 || p.OutputTokens < 1 || p.OutputTokens > 512 || p.CleanupSeconds < 1 || p.CleanupSeconds > 3600 || p.CleanupBatch < 1 || p.CleanupBatch > 1000 || p.RetentionDays < 1 || p.RetentionDays > 365 {
		return bad
	}
	return nil
}

// Owned knowledge groups reject implicit coercion, aliases, nulls, duplicate and unknown fields.
func strictKnowledgeYAML(node *yaml.Node, typ reflect.Type) error {
	bad := errors.New("CONFIG_KNOWLEDGE_INVALID")
	if node.Kind == yaml.AliasNode || node.Tag == "!!null" {
		return bad
	}
	// Optional fields retain the same concrete shape and scalar requirements.
	// Explicit null and aliases above never select omission/default semantics.
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Struct {
		if node.Kind != yaml.MappingNode {
			return bad
		}
		fields := map[string]reflect.Type{}
		seen := map[string]bool{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			fields[strings.Split(f.Tag.Get("yaml"), ",")[0]] = f.Type
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			field, ok := fields[key.Value]
			if key.Tag != "!!str" || !ok || seen[key.Value] {
				return bad
			}
			seen[key.Value] = true
			if e := strictKnowledgeYAML(value, field); e != nil {
				return e
			}
		}
		return nil
	}
	if typ.Kind() == reflect.Slice {
		if node.Kind != yaml.SequenceNode {
			return bad
		}
		for _, item := range node.Content {
			if e := strictKnowledgeYAML(item, typ.Elem()); e != nil {
				return e
			}
		}
		return nil
	}
	expected := "!!int"
	switch typ.Kind() {
	case reflect.String:
		expected = "!!str"
	case reflect.Bool:
		expected = "!!bool"
	case reflect.Float64:
		if node.Tag == "!!int" {
			return nil
		}
		expected = "!!float"
	}
	if node.Kind != yaml.ScalarNode || node.Tag != expected {
		return bad
	}
	return nil
}
func (p *KnowledgePolicy) UnmarshalYAML(node *yaml.Node) error {
	if e := strictKnowledgeYAML(node, reflect.TypeOf(*p)); e != nil {
		return e
	}
	type plain KnowledgePolicy
	value := plain(*p)
	if e := node.Decode(&value); e != nil {
		return e
	}
	*p = KnowledgePolicy(value)
	return nil
}
func (p *KnowledgeConfiguration) UnmarshalYAML(node *yaml.Node) error {
	if e := strictKnowledgeYAML(node, reflect.TypeOf(*p)); e != nil {
		return e
	}
	type plain KnowledgeConfiguration
	var value plain
	if e := node.Decode(&value); e != nil {
		return e
	}
	*p = KnowledgeConfiguration(value)
	return nil
}
func validateKnowledgeYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil || len(document.Content) != 1 {
		return errors.New("CONFIG_INVALID")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return errors.New("CONFIG_INVALID")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		name, value := root.Content[i].Value, root.Content[i+1]
		if name == "knowledge" {
			if e := strictKnowledgeYAML(value, reflect.TypeOf(KnowledgeConfiguration{})); e != nil {
				return e
			}
		}
		if name == "runtime" && value.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(value.Content); j += 2 {
				if value.Content[j].Value == "knowledge" {
					if e := strictKnowledgeYAML(value.Content[j+1], reflect.TypeOf(KnowledgePolicy{})); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}
