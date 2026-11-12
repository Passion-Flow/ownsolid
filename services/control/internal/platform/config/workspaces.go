package config

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	"go.yaml.in/yaml/v3"
)

const MaximumPolicyEnvironmentBytes int64 = 16 << 10
const workspaceCountEnvironment = "PROJECT_ONE_WORKSPACES_MAXIMUM_COUNT"
const workspaceNotesEnvironment = "PROJECT_ONE_WORKSPACES_MAXIMUM_LIVE_NOTES"

type WorkspacePolicy struct {
	MaximumCount     int `yaml:"maximum_count" json:"maximum_count"`
	MaximumLiveNotes int `yaml:"maximum_live_notes" json:"maximum_live_notes"`
}

func DefaultWorkspacePolicy() WorkspacePolicy {
	return WorkspacePolicy{MaximumCount: 100, MaximumLiveNotes: 10000}
}

func (p WorkspacePolicy) Validate() error {
	if p.MaximumCount < 1 || p.MaximumCount > 10000 || p.MaximumLiveNotes < 1 || p.MaximumLiveNotes > 1000000 {
		return errors.New("CONFIG_WORKSPACE_POLICY_INVALID")
	}
	return nil
}

func (p *WorkspacePolicy) UnmarshalYAML(node *yaml.Node) error {
	if strictKnowledgeYAML(node, reflect.TypeOf(*p)) != nil {
		return errors.New("CONFIG_WORKSPACE_POLICY_INVALID")
	}
	type plain WorkspacePolicy
	value := plain(*p)
	if node.Decode(&value) != nil {
		return errors.New("CONFIG_WORKSPACE_POLICY_INVALID")
	}
	*p = WorkspacePolicy(value)
	return nil
}

// Explicit nulls and aliases must not silently restore defaults or override malformed YAML.
func validateWorkspaceYAML(raw []byte) error {
	var node yaml.Node
	bad := errors.New("CONFIG_WORKSPACE_POLICY_INVALID")
	if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return bad
	}
	root := node.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value != "runtime" {
			continue
		}
		runtime := root.Content[i+1]
		if runtime.Tag == "!!null" {
			continue
		} // Existing operator snapshots may omit the entire runtime policy.
		if runtime.Kind != yaml.MappingNode {
			return bad
		}
		for j := 0; j < len(runtime.Content); j += 2 {
			if runtime.Content[j].Value == "workspaces" && strictKnowledgeYAML(runtime.Content[j+1], reflect.TypeOf(WorkspacePolicy{})) != nil {
				return bad
			}
		}
	}
	return nil
}

func (c *Config) resolveWorkspaceEnvironment(path string) error {
	if e := c.validateMachineServer(); e != nil {
		return e
	}
	if e := c.TaskEvents.Validate(); e != nil {
		return e
	}
	if e := c.ValidateDatabaseSelection(); e != nil {
		return e
	}
	// Validate YAML independently even when the environment would replace an invalid value.
	if e := c.AWS.Validate(); e != nil {
		return e
	}
	if e := c.ModelCache.Validate(); e != nil {
		return e
	}
	if e := c.Policy().Workspaces.Validate(); e != nil {
		return e
	}
	if e := c.ValidateDatabaseLockTimeout(); e != nil {
		return e
	}
	if e := c.Policy().SSO.Validate(); e != nil {
		return e
	}
	if e := c.Policy().Validate(); e != nil {
		return e
	}
	if path == "" {
		return nil
	}
	raw, e := readPrivateFile(path, MaximumPolicyEnvironmentBytes, "CONFIG_POLICY_PRIVATE_FILE_INVALID")
	if e != nil {
		return e
	}
	bad := errors.New("CONFIG_POLICY_ENVIRONMENT_INVALID")
	if c.Runtime == nil {
		policy := DefaultRuntimePolicy()
		c.Runtime = &policy
	}
	seen := make(map[string]bool)
	taskEvents := make(map[string]string)
	transportChanged := false
	machineChanged := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || seen[key] || (key != releaseViewConfigurationEnvironment && key != privateClusterConfigurationEnvironment && key != monitoringConfigurationEnvironment && !middlewareEnvironment(key) && key != registryConfigEnvironment && key != registrySiteSourceEnvironment && key != mongoDBConfigEnvironment && key != workspaceCountEnvironment && key != workspaceNotesEnvironment && key != databaseLockEnvironment && c.Runtime.httpEnvironmentField(key) == nil && !serverTransportEnvironment(key) && key != "PROJECT_ONE_MODELS_ARTIFACT_ROOT" && key != "PROJECT_ONE_COMPONENT_NODE_EXPORTER_ARCHIVE_FILE" && key != "PROJECT_ONE_COMPONENT_NODE_EXPORTER_CATALOG_DIRECTORY" && !machineServerEnvironment(key) && !databaseSelectionEnvironment(key) && !ssoMaintenanceEnvironment(key) && !ssoConfigurationEnvironment(key) && !pluginConfigurationEnvironment(key) && !c.Runtime.Plugins.environmentField(key).IsValid() && key != "PROJECT_ONE_WORKFLOWS_ENABLED" && !c.Runtime.Workflows.environmentField(key).IsValid() && !c.Runtime.Portal.environmentField(key).IsValid() && !knowledgeConfigurationEnvironment(key) && !c.Runtime.Knowledge.environmentField(key).IsValid() && c.Runtime.nodeEnvironmentField(key) == nil && c.Runtime.Models.environmentField(key) == nil && c.Runtime.Distribution.environmentField(key) == nil && !awsEnvironment(key) && !modelCacheEnvironment(key) && !objectStorageEnvironment(key) && !taskEventEnvironment(key) && !datasetPathEnvironment(key) && !c.Runtime.Datasets.environmentKey(key) && !trainingArtifactEnvironment(key) && !c.Runtime.Training.environmentKey(key) && !preparationArtifactEnvironment(key) && !c.Runtime.ModelLifecycle.environmentKey(key)) {
			return bad
		}
		seen[key] = true
		if strings.HasPrefix(value, "\"") {
			var decoded string
			if json.Unmarshal([]byte(value), &decoded) != nil {
				return bad
			}
			value = decoded
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && len(value) >= 2 {
			value = value[1 : len(value)-1]
		}
		if databaseSelectionEnvironment(key) {
			if e := c.applyDatabaseSelectionEnvironment(key, value); e != nil {
				return bad
			}
			continue
		}
		if modelCacheEnvironment(key) {
			if e := c.ModelCache.applyEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		if value == "" {
			return bad
		}
		if pluginConfigurationEnvironment(key) {
			if e := c.Plugins.applyEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		if c.Runtime.Plugins.environmentField(key).IsValid() {
			if e := c.Runtime.Plugins.applyEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		if key == "PROJECT_ONE_WORKFLOWS_ENABLED" {
			if e := c.Workflows.applyEnvironment(value); e != nil {
				return e
			}
			continue
		}
		if c.Runtime.Portal.environmentField(key).IsValid() {
			if e := c.Runtime.Portal.applyEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		if c.Runtime.Workflows.environmentField(key).IsValid() {
			if e := c.Runtime.Workflows.applyEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		if knowledgeConfigurationEnvironment(key) {
			if e := c.Knowledge.applyEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		if c.Runtime.Knowledge.environmentField(key).IsValid() {
			if e := c.Runtime.Knowledge.applyEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		if ssoConfigurationEnvironment(key) {
			if e := c.SSO.applyEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		if machineServerEnvironment(key) {
			if c.MachineServer == nil {
				return bad
			}
			if !machineChanged {
				selected := *c.MachineServer
				c.MachineServer = &selected
			}
			if e := c.MachineServer.applyEnvironment(key, value); e != nil {
				return bad
			}
			machineChanged = true
			continue
		}
		if preparationArtifactEnvironment(key) {
			c.ModelPreparation.applyEnvironment(key, value)
			continue
		}
		if c.Runtime.ModelLifecycle.environmentKey(key) {
			if c.Runtime.ModelLifecycle.applyEnvironment(key, value) != nil {
				return bad
			}
			continue
		}
		if trainingArtifactEnvironment(key) {
			c.Training.applyEnvironment(key, value)
			continue
		}
		if c.Runtime.Training.environmentKey(key) {
			if c.Runtime.Training.applyEnvironment(key, value) != nil {
				return bad
			}
			continue
		}
		if datasetPathEnvironment(key) {
			if !filepath.IsAbs(value) || filepath.Clean(value) != value || value == string(filepath.Separator) {
				return bad
			}
			switch key {
			case "PROJECT_ONE_DATASETS_ARTIFACT_ROOT":
				c.Datasets.ArtifactRoot = value
			case "PROJECT_ONE_DATASETS_PYTHON_EXECUTABLE":
				c.Datasets.PythonExecutable = value
			case "PROJECT_ONE_DATASETS_PROCESSOR_FILE":
				c.Datasets.ProcessorFile = value
			}
			continue
		}
		if c.Runtime.Datasets.environmentKey(key) {
			if c.Runtime.Datasets.applyEnvironment(key, value) != nil {
				return bad
			}
			continue
		}
		if key == "PROJECT_ONE_MODELS_ARTIFACT_ROOT" {
			if !filepath.IsAbs(value) || filepath.Clean(value) != value || value == string(filepath.Separator) {
				return bad
			}
			c.Models.ArtifactRoot = value
			continue
		}
		if key == "PROJECT_ONE_COMPONENT_NODE_EXPORTER_CATALOG_DIRECTORY" {
			if !filepath.IsAbs(value) || filepath.Clean(value) != value {
				return bad
			}
			c.Components.NodeExporterCatalogDirectory = value
			continue
		}
		if key == "PROJECT_ONE_COMPONENT_NODE_EXPORTER_ARCHIVE_FILE" {
			if !filepath.IsAbs(value) || filepath.Clean(value) != value {
				return bad
			}
			c.Components.NodeExporterArchiveFile = value
			continue
		}
		if serverTransportEnvironment(key) {
			if e := c.Server.applyTransportEnvironment(key, value); e != nil {
				return bad
			}
			transportChanged = true
			continue
		}
		if taskEventEnvironment(key) {
			taskEvents[key] = value
			continue
		}
		if objectStorageEnvironment(key) {
			if e := c.applyObjectStorageEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		if key == privateClusterConfigurationEnvironment {
			if !registryConfigurationPath(value) {
				return bad
			}
			c.PrivateClusterConfigFile = value
			continue
		}
		if key == releaseViewConfigurationEnvironment {
			if !registryConfigurationPath(value) {
				return bad
			}
			c.ReleaseViewConfigFile = value
			continue
		}
		if key == monitoringConfigurationEnvironment {
			if !registryConfigurationPath(value) {
				return bad
			}
			c.MonitoringConfigFile = value
			continue
		}
		if middlewareEnvironment(key) {
			if !registryConfigurationPath(value) {
				return bad
			}
			if key == middlewareConfigurationEnvironment {
				c.MiddlewareConfigFile = value
			} else {
				c.MiddlewareEnvironmentFile = value
			}
			continue
		}
		if key == registryConfigEnvironment {
			if !registryConfigurationPath(value) {
				return bad
			}
			c.ImageRegistryConfigFile = value
			continue
		}
		if key == registrySiteSourceEnvironment {
			if !registryConfigurationPath(value) {
				return bad
			}
			c.ImageRegistrySiteSourceFile = value
			continue
		}
		if key == mongoDBConfigEnvironment {
			c.MongoDBConfigFile = value
			continue
		}
		if awsEnvironment(key) {
			if e := c.AWS.applyEnvironment(key, value); e != nil {
				return e
			}
			continue
		}
		for _, ch := range value {
			if ch < '0' || ch > '9' {
				return bad
			}
		}
		count, e := strconv.Atoi(value)
		if e != nil {
			return bad
		}
		switch key {
		case workspaceCountEnvironment:
			c.Runtime.Workspaces.MaximumCount = count
		case workspaceNotesEnvironment:
			c.Runtime.Workspaces.MaximumLiveNotes = count
		case databaseLockEnvironment:
			c.Database.LockTimeoutMilliseconds = &count
		default:
			if field := c.Runtime.httpEnvironmentField(key); field != nil {
				*field = count
			} else if field := c.Runtime.nodeEnvironmentField(key); field != nil {
				*field = count
			} else if field := c.Runtime.Models.environmentField(key); field != nil {
				*field = count
			} else if field := c.Runtime.Distribution.environmentField(key); field != nil {
				*field = count
			} else {
				c.Runtime.SSO.applyMaintenanceEnvironment(key, count)
			}
		}
	}
	if len(taskEvents) != 0 {
		value, e := taskbus.ApplyEnvironment(c.TaskEvents, taskEvents)
		if e != nil {
			return e
		}
		c.TaskEvents = value
	}
	if transportChanged && c.validateServerTransport() != nil {
		return bad
	}
	if machineChanged && c.validateMachineServer() != nil {
		return bad
	}
	if c.SSO.Validate() != nil || c.ModelCache.Validate() != nil || c.ValidateDatabaseSelection() != nil || c.Runtime.Workspaces.Validate() != nil || c.ValidateDatabaseLockTimeout() != nil || c.Runtime.SSO.Validate() != nil || c.Runtime.Validate() != nil {
		return bad
	}
	return nil
}
