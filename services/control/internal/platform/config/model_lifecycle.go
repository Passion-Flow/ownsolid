package config

import (
	"errors"
	"go.yaml.in/yaml/v3"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"reflect"
	"strings"
)

// ModelLifecyclePolicy bounds authenticated source analysis; changes require control restart.
type ModelLifecyclePolicy struct {
	SnapshotSeconds             int            `yaml:"snapshot_seconds"`
	SnapshotManifestBytes       int64          `yaml:"snapshot_manifest_bytes"`
	SnapshotMaximumBytes        int64          `yaml:"snapshot_maximum_bytes"`
	SnapshotMaximumFiles        int            `yaml:"snapshot_maximum_files"`
	ScanSeconds                 int            `yaml:"scan_seconds"`
	ScanMaximumBytes            int64          `yaml:"scan_maximum_bytes"`
	ScanMaximumRows             int64          `yaml:"scan_maximum_rows"`
	ScanMaximumVersions         int            `yaml:"scan_maximum_versions"`
	ScanConcurrency             int            `yaml:"scan_concurrency"`
	TransactionSeconds          int            `yaml:"transaction_seconds"`
	MaxHoldoutsPerWorkspace     int            `yaml:"max_holdouts_per_workspace"`
	Resources                   wire.Resources `yaml:"resources"`
	MaxPreparationsPerWorkspace int            `yaml:"max_preparations_per_workspace"`
	MaxActivePerWorkspace       int            `yaml:"max_active_per_workspace"`
	MaxAttempts                 int            `yaml:"max_attempts"`
	PollMilliseconds            int            `yaml:"poll_milliseconds"`
	TransferSeconds             int            `yaml:"transfer_seconds"`
	OutputSeconds               int            `yaml:"output_seconds"`
	MinimumFreeBytes            int64          `yaml:"minimum_free_bytes"`
	MinimumHostMemory           int64          `yaml:"minimum_host_memory_bytes"`
	MinimumGPUMemoryMiB         int64          `yaml:"minimum_gpu_memory_mib"`
	MinimumDriverMajor          int            `yaml:"minimum_driver_major"`
	MinimumComputeMajor         int            `yaml:"minimum_compute_major"`
}

func DefaultModelLifecyclePolicy() ModelLifecyclePolicy {
	return ModelLifecyclePolicy{SnapshotSeconds: 300, SnapshotManifestBytes: 33554432, SnapshotMaximumBytes: 107374182400, SnapshotMaximumFiles: 100000, ScanSeconds: 5, ScanMaximumBytes: 67108864, ScanMaximumRows: 100000, ScanMaximumVersions: 1000, ScanConcurrency: 1, TransactionSeconds: 10, MaxHoldoutsPerWorkspace: 100, Resources: wire.DefaultResources(), MaxPreparationsPerWorkspace: 1000, MaxActivePerWorkspace: 10, MaxAttempts: 5, PollMilliseconds: 1000, TransferSeconds: 30, OutputSeconds: 300, MinimumFreeBytes: 3221225472, MinimumHostMemory: 12884901888, MinimumGPUMemoryMiB: 8192, MinimumDriverMajor: 580, MinimumComputeMajor: 8}
}
func (p ModelLifecyclePolicy) Validate() error {
	if p.SnapshotSeconds < 1 || p.SnapshotSeconds > 3600 || p.SnapshotManifestBytes < 4096 || p.SnapshotManifestBytes > 67108864 || p.SnapshotMaximumBytes < 2147483648 || p.SnapshotMaximumBytes > 10995116277760 || p.SnapshotMaximumFiles < 16 || p.SnapshotMaximumFiles > 1000000 {
		return errors.New("CONFIG_MODEL_LIFECYCLE_POLICY_INVALID")
	}
	if p.ScanSeconds < 1 || p.ScanSeconds > 300 || p.ScanMaximumBytes < 1024 || p.ScanMaximumBytes > 1073741824 || p.ScanMaximumRows < 3 || p.ScanMaximumRows > 1000000 || p.ScanMaximumVersions < 1 || p.ScanMaximumVersions > 10000 || p.ScanConcurrency < 1 || p.ScanConcurrency > 4 || p.TransactionSeconds < 1 || p.TransactionSeconds > 60 || p.MaxHoldoutsPerWorkspace < 1 || p.MaxHoldoutsPerWorkspace > 10000 {
		return errors.New("CONFIG_MODEL_LIFECYCLE_POLICY_INVALID")
	}
	if p.Resources.Validate() != nil || p.MaxPreparationsPerWorkspace < 1 || p.MaxPreparationsPerWorkspace > 100000 || p.MaxActivePerWorkspace < 1 || p.MaxActivePerWorkspace > 1000 || p.MaxActivePerWorkspace > p.MaxPreparationsPerWorkspace || p.MaxAttempts < 1 || p.MaxAttempts > wire.MaximumAttemptNumber || p.PollMilliseconds < 100 || p.PollMilliseconds > 60000 || p.TransferSeconds < 1 || p.TransferSeconds > 300 || p.OutputSeconds < 1 || p.OutputSeconds > 3600 || p.MinimumFreeBytes < p.Resources.MaximumOutputBytes || p.MinimumFreeBytes > 1099511627776 || p.MinimumHostMemory < p.Resources.MemoryBytes || p.MinimumHostMemory > 1099511627776 || p.MinimumGPUMemoryMiB < 8192 || p.MinimumGPUMemoryMiB > 1048576 || p.MinimumDriverMajor < 580 || p.MinimumDriverMajor > 999 || p.MinimumComputeMajor < 8 || p.MinimumComputeMajor > 99 {
		return errors.New("CONFIG_MODEL_LIFECYCLE_POLICY_INVALID")
	}
	return nil
}

// Validate new-policy YAML scalar types before default merging: the library otherwise accepts null and truncates floats.
// Other existing configuration namespaces retain their established decoding behavior.
func validateModelLifecycleYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil {
		return errors.New("CONFIG_INVALID")
	}
	invalid := func() error { return errors.New("CONFIG_MODEL_LIFECYCLE_POLICY_INVALID") }
	resolve := func(value *yaml.Node) *yaml.Node {
		visited := map[*yaml.Node]bool{}
		for value != nil && value.Kind == yaml.AliasNode {
			if visited[value] {
				return nil
			}
			visited[value] = true
			value = value.Alias
		}
		return value
	}
	var check func(*yaml.Node, reflect.Type, error) error
	check = func(node *yaml.Node, typ reflect.Type, bad error) error {
		body := resolve(node)
		if body == nil || body.Kind != yaml.MappingNode {
			return bad
		}
		fields := map[string]reflect.Type{}
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			fields[strings.Split(field.Tag.Get("yaml"), ",")[0]] = field.Type
		}
		seen := map[string]bool{}
		for index := 0; index+1 < len(body.Content); index += 2 {
			name, value := body.Content[index], resolve(body.Content[index+1])
			field, known := fields[name.Value]
			if name.Kind != yaml.ScalarNode || name.ShortTag() != "!!str" || !known || seen[name.Value] || value == nil {
				return bad
			}
			seen[name.Value] = true
			if field.Kind() == reflect.Struct {
				if e := check(value, field, bad); e != nil {
					return e
				}
			} else {
				tag := "!!int"
				if field.Kind() == reflect.String {
					tag = "!!str"
				}
				if value.Kind != yaml.ScalarNode || value.ShortTag() != tag {
					return bad
				}
			}
		}
		return nil
	}
	visited := map[*yaml.Node]bool{}
	var walk func(*yaml.Node) error
	walk = func(node *yaml.Node) error {
		if node == nil || visited[node] {
			return nil
		}
		visited[node] = true
		if node.Kind == yaml.AliasNode {
			return walk(node.Alias)
		}
		if node.Kind == yaml.MappingNode {
			for index := 0; index+1 < len(node.Content); index += 2 {
				key, value := node.Content[index], node.Content[index+1]
				if key.Value == "model_lifecycle" {
					if e := check(value, reflect.TypeOf(ModelLifecyclePolicy{}), invalid()); e != nil {
						return e
					}
				}
				if key.Value == "model_preparation" {
					if e := check(value, reflect.TypeOf(ModelPreparationConfiguration{}), errors.New("CONFIG_MODEL_PREPARATION_INVALID")); e != nil {
						return e
					}
				}
			}
		}
		for _, child := range node.Content {
			if e := walk(child); e != nil {
				return e
			}
		}
		return nil
	}
	return walk(&document)
}
