package config

import (
	"errors"
	"path/filepath"
)

func (s ServerConfiguration) Profile() string {
	if s.ApplicationProfile == nil {
		return "platform"
	}
	return *s.ApplicationProfile
}
func (c Config) AccountOnly() bool      { return c.Server.Profile() == "account" }
func (c Config) WorkspacesOnly() bool   { return c.Server.Profile() == "workspaces" }
func (c Config) NodesOnly() bool        { return c.Server.Profile() == "nodes" }
func (c Config) ComponentsOnly() bool   { return c.Server.Profile() == "components" }
func (c Config) MaintenanceOnly() bool  { return c.Server.Profile() == "maintenance" }
func (c Config) ModelsOnly() bool       { return c.Server.Profile() == "models" }
func (c Config) GatewayOnly() bool      { return c.Server.Profile() == "gateway" }
func (c Config) DatasetsOnly() bool     { return c.Server.Profile() == "datasets" }
func (c Config) TrainingOnly() bool     { return c.Server.Profile() == "training" }
func (c Config) LifecycleOnly() bool    { return c.Server.Profile() == "lifecycle" }
func (c Config) SSOOnly() bool          { return c.Server.Profile() == "sso" }
func (c Config) KnowledgeOnly() bool    { return c.Server.Profile() == "knowledge" }
func (c Config) WorkflowsOnly() bool    { return c.Server.Profile() == "workflows" }
func (c Config) PluginsOnly() bool      { return c.Server.Profile() == "plugins" }
func (c Config) PortalOnly() bool       { return c.Server.Profile() == "portal" }
func (c Config) LocalizationOnly() bool { return c.Server.Profile() == "localization" }
func (c Config) ReleasesOnly() bool     { return c.Server.Profile() == "releases" }

const ReleasesCacheV1Profile = "releases-cache-v1"

func (c Config) ReleasesCacheV1Only() bool { return c.Server.Profile() == ReleasesCacheV1Profile }
func (c Config) ReleasesRuntime() bool     { return c.ReleasesOnly() || c.ReleasesCacheV1Only() }
func (c Config) LocalizationRuntime() bool { return c.LocalizationOnly() || c.ReleasesRuntime() }
func (c Config) PortalRuntime() bool       { return c.PortalOnly() || c.LocalizationRuntime() }
func (c Config) PluginsRuntime() bool      { return c.PluginsOnly() || c.PortalRuntime() }
func (c Config) WorkflowsRuntime() bool    { return c.WorkflowsOnly() || c.PluginsRuntime() }
func (c Config) KnowledgeRuntime() bool    { return c.KnowledgeOnly() || c.WorkflowsRuntime() }
func (c Config) SSORuntime() bool          { return c.SSOOnly() || c.KnowledgeRuntime() }
func (c Config) LifecycleRuntime() bool    { return c.LifecycleOnly() || c.SSORuntime() }
func (c Config) TrainingRuntime() bool     { return c.TrainingOnly() || c.LifecycleRuntime() }
func (c Config) DatasetRuntime() bool      { return c.DatasetsOnly() || c.TrainingRuntime() }
func (c Config) GatewayRuntime() bool      { return c.GatewayOnly() || c.DatasetRuntime() }
func (c Config) ModelsRuntime() bool       { return c.ModelsOnly() || c.GatewayRuntime() }
func (c Config) MaintenanceRuntime() bool  { return c.MaintenanceOnly() || c.ModelsRuntime() }
func (c Config) ComponentRuntime() bool    { return c.ComponentsOnly() || c.MaintenanceRuntime() }
func (c Config) NodeRuntime() bool         { return c.NodesOnly() || c.ComponentRuntime() }
func (c Config) WorkspaceRuntime() bool    { return c.WorkspacesOnly() || c.NodeRuntime() }

// AccountRuntime selects the bounded M01 services with optional M02 Workspaces and M03 discovery and M04 components.
func (c Config) AccountRuntime() bool { return c.AccountOnly() || c.WorkspaceRuntime() }
func (c Config) ValidateApplicationProfile() error {
	if c.Server.Profile() != "platform" && !c.AccountRuntime() && !c.MVPPostgreSQLCPUV1Only() {
		return errors.New("CONFIG_APPLICATION_PROFILE_INVALID")
	}
	if (c.ReleasesRuntime() || c.MVPPostgreSQLCPUV1Only()) && c.ReleaseViewConfigFile == "" {
		return errors.New("CONFIG_RELEASE_VIEW_REQUIRED")
	}
	if (c.NodeRuntime() || c.MVPPostgreSQLCPUV1Only()) && c.Server.EffectiveTransportMode() == TransportHTTP && c.MachineServer == nil {
		return errors.New("CONFIG_NODES_MACHINE_HTTPS_REQUIRED")
	}
	if (c.ComponentRuntime() || c.MVPPostgreSQLCPUV1Only()) && c.Components.NodeExporterArchiveFile != "" && (!filepath.IsAbs(c.Components.NodeExporterArchiveFile) || filepath.Clean(c.Components.NodeExporterArchiveFile) != c.Components.NodeExporterArchiveFile) {
		return errors.New("CONFIG_COMPONENT_ARCHIVE_REFERENCE_INVALID")
	}
	if c.ComponentsOnly() && c.Components.NodeExporterCatalogDirectory != "" {
		return errors.New("CONFIG_COMPONENTS_PROFILE_CONFLICT")
	}
	if (c.MaintenanceRuntime() || c.MVPPostgreSQLCPUV1Only()) && c.Components.NodeExporterCatalogDirectory != "" && (!filepath.IsAbs(c.Components.NodeExporterCatalogDirectory) || filepath.Clean(c.Components.NodeExporterCatalogDirectory) != c.Components.NodeExporterCatalogDirectory) {
		return errors.New("CONFIG_COMPONENT_CATALOG_REFERENCE_INVALID")
	}
	if (c.ModelsRuntime() || c.MVPPostgreSQLCPUV1Only()) && (!filepath.IsAbs(c.Models.ArtifactRoot) || filepath.Clean(c.Models.ArtifactRoot) != c.Models.ArtifactRoot || c.Models.ArtifactRoot == string(filepath.Separator)) {
		return errors.New("CONFIG_MODEL_STORE_REFERENCE_REQUIRED")
	}
	if (c.DatasetRuntime() || c.MVPPostgreSQLCPUV1Only()) && (c.Datasets.ArtifactRoot == "" || c.Datasets.PythonExecutable == "" || c.Datasets.ProcessorFile == "") {
		return errors.New("CONFIG_DATASETS_RUNTIME_REQUIRED")
	}
	if (c.TrainingRuntime() || c.MVPPostgreSQLCPUV1Only()) && !c.Training.Enabled() {
		return errors.New("CONFIG_TRAINING_RUNTIME_REQUIRED")
	}
	if (c.LifecycleRuntime() || c.MVPPostgreSQLCPUV1Only()) && !c.ModelPreparation.Enabled() {
		return errors.New("CONFIG_LIFECYCLE_RUNTIME_REQUIRED")
	}
	if (c.PortalRuntime() || c.MVPPostgreSQLCPUV1Only()) && (c.Policy().HTTP.RequestSeconds <= c.Policy().Portal.CommitSeconds || c.Policy().HTTP.StartupSeconds <= c.Policy().Portal.CommitSeconds || c.Policy().HTTP.ShutdownSeconds <= c.Policy().Portal.CommitSeconds) {
		return errors.New("CONFIG_PORTAL_HTTP_BUDGET_INVALID")
	}
	if c.MVPPostgreSQLCPUV1Only() && c.DatabaseEngine() != PostgreSQLDatabaseEngine {
		return errors.New("CONFIG_MVP_POSTGRESQL_REQUIRED")
	}
	if !c.AccountRuntime() {
		return nil
	}
	if c.DatabaseEngine() != PostgreSQLDatabaseEngine || (c.SSO.Enabled && !c.SSORuntime()) || (c.Knowledge.Enabled && !c.KnowledgeRuntime()) || (c.Workflows.Enabled && !c.WorkflowsRuntime()) || (c.Plugins.Enabled && !c.PluginsRuntime()) || c.TaskEvents.Enabled || c.MongoDB.Enabled || c.AWS.Enabled || (c.ModelCache.Enabled && !c.ReleasesCacheV1Only()) || (c.MachineServer != nil && !c.NodeRuntime()) || (c.Models.ArtifactRoot != "" && !c.ModelsRuntime()) || (c.Datasets.ArtifactRoot != "" && !c.DatasetRuntime()) || (c.Training.ArtifactRoot != "" && !c.TrainingRuntime()) || (c.ModelPreparation.ArtifactRoot != "" && !c.LifecycleRuntime()) || c.ObjectStorage != nil || c.ImageRegistryConfigFile != "" || c.MiddlewareConfigFile != "" || c.PrivateClusterConfigFile != "" || c.MonitoringConfigFile != "" || (c.ReleaseViewConfigFile != "" && !c.ReleasesRuntime()) {
		return errors.New("CONFIG_ACCOUNT_PROFILE_CONFLICT")
	}
	return nil
}
