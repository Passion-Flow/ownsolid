package config

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	"go.yaml.in/yaml/v3"
)

const MinimumDatabaseConnections int32 = 1
const MaximumDatabaseConnections int32 = 32

// MaximumRoleConnections bounds the serving role's connection limit, which covers control's
// pool and every agent gateway's (ADR 0048). It is the largest connection limit the installer
// gives PostgreSQL.
const MaximumRoleConnections int32 = 1000

type Config struct {
	Telemetry                 *TelemetryConfiguration       `yaml:"telemetry,omitempty"`
	LivenessReplica           *LivenessReplicaConfiguration `yaml:"liveness_replica,omitempty"`
	AgentGateway              *AgentGatewayConfiguration    `yaml:"agent_gateway,omitempty" json:"-"`
	AgentIdentity             *AgentIdentityConfiguration   `yaml:"agent_identity,omitempty" json:"-"`
	Topology                  TopologyConfiguration         `yaml:"topology,omitempty" json:"topology"`
	ReleaseViewConfigFile     string                        `yaml:"release_view_config_file,omitempty" json:"-"`
	MachineServer             *MachineServerConfiguration   `yaml:"machine_server,omitempty" json:"machine_server,omitempty"`
	PrivateClusterConfigFile  string                        `yaml:"private_cluster_config_file,omitempty" json:"-"`
	MonitoringConfigFile      string                        `yaml:"monitoring_config_file,omitempty" json:"-"`
	MiddlewareConfigFile      string                        `yaml:"middleware_config_file,omitempty" json:"-"`
	MiddlewareEnvironmentFile string                        `yaml:"middleware_environment_file,omitempty" json:"-"`
	TaskEvents                taskbus.Configuration         `yaml:"task_events,omitempty" json:"-"`
	ImageRegistryConfigFile   string                        `yaml:"image_registry_config_file,omitempty"`
	ImageRegistry             *registryconfig.Config        `yaml:"-" json:"-"`
	ObjectStorage             *ObjectStorageConfiguration   `yaml:"object_storage,omitempty" json:"ObjectStorage,omitempty"`
	ModelCache                ModelCacheConfiguration       `yaml:"model_cache,omitempty"`
	AWS                       AWSConfiguration              `yaml:"aws,omitempty"`
	MongoDBConfigFile         string                        `yaml:"mongodb_config_file,omitempty"`
	MongoDB                   MongoDBConfiguration          `yaml:"-"`
	Licensing                 *LicensingConfiguration       `yaml:"licensing,omitempty" json:"Licensing,omitempty"`
	Localization              LocalizationConfiguration     `yaml:"localization"`
	Plugins                   PluginConfiguration           `yaml:"plugins"`
	Workflows                 WorkflowConfiguration         `yaml:"workflows"`
	Knowledge                 KnowledgeConfiguration        `yaml:"knowledge"`
	SSO                       SSOConfiguration              `yaml:"sso"`
	ModelPreparation          ModelPreparationConfiguration `yaml:"model_preparation"`
	Training                  TrainingConfiguration         `yaml:"training"`
	Datasets                  DatasetConfiguration          `yaml:"datasets"`
	Runtime                   *RuntimePolicy                `yaml:"runtime"`
	Models                    struct {
		ArtifactRoot string `yaml:"artifact_root"`
	} `yaml:"models"`
	Components struct {
		NodeExporterArchiveFile      string `yaml:"node_exporter_archive_file"`
		NodeExporterCatalogDirectory string `yaml:"node_exporter_catalog_directory"`
	} `yaml:"components"`
	Server   ServerConfiguration `yaml:"server"`
	Database struct {
		Dameng                 *DamengServingConfiguration      `yaml:"dameng,omitempty" json:"-"`
		Engine                 string                           `yaml:"engine,omitempty"`
		MySQLConfigurationFile string                           `yaml:"mysql_configuration_file,omitempty"`
		MySQLMigrations        MySQLMigrationPolicy             `yaml:"mysql_migrations,omitempty"`
		MySQLBackup            *MySQLBackupConfiguration        `yaml:"mysql_backup,omitempty" json:"MySQLBackup,omitempty"`
		MySQLRuntimeAccess     *MySQLRuntimeAccessConfiguration `yaml:"mysql_runtime_access,omitempty" json:"MySQLRuntimeAccess,omitempty"`
		DSNFile                string                           `yaml:"dsn_file"`
		MaxOpenConnections     int32                            `yaml:"max_open_connections"`
		MaxIdleConnections     int32                            `yaml:"max_idle_connections"`
		// OperatorReservedConnections are the pool's connections agent calls never take, so an
		// operator can sign in while every agent seat is in use. The default, 3, is two for the
		// operator and one for the liveness writer, which waits on the same rows as agents do. The
		// installation lease holds one more for the life of the process.
		OperatorReservedConnections int32 `yaml:"operator_reserved_connections"`
		// RoleConnectionLimit is the connection limit of the serving role, shared by control
		// and the agent gateways beside it (ADR 0048). Zero means max_open_connections, which is
		// enough without gateways; with them it is control's pool plus each gateway's.
		RoleConnectionLimit     int32 `yaml:"role_connection_limit,omitempty"`
		StatementTimeoutSeconds int   `yaml:"statement_timeout_seconds"`
		LockTimeoutMilliseconds *int  `yaml:"lock_timeout_milliseconds,omitempty"`
	} `yaml:"database"`
	Security struct {
		AuthHMACKeyFile       string `yaml:"auth_hmac_key_file"`
		MailKeyringFile       string `yaml:"mail_keyring_file"`
		PasswordBlocklistFile string `yaml:"password_blocklist_file"`
	} `yaml:"security"`
	SMTP struct {
		Host                    string `yaml:"host"`
		Port                    int    `yaml:"port"`
		FromAddress             string `yaml:"from_address"`
		TLSMode                 string `yaml:"tls_mode"`
		CAFile                  string `yaml:"ca_file"`
		Username                string `yaml:"username"`
		PasswordFile            string `yaml:"password_file"`
		ConnectTimeoutSeconds   int    `yaml:"connect_timeout_seconds"`
		OperationTimeoutSeconds int    `yaml:"operation_timeout_seconds"`
	} `yaml:"smtp"`
}

func Secret(path string) ([]byte, error) {
	st, e := os.Stat(path)
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, errors.New("SECRET_FILE_INVALID")
	}
	return os.ReadFile(path)
}
func Load(path string) (Config, error) { return LoadWithEnvironment(path, "") }

// LoadWithEnvironment applies an explicit private serving policy file, never ambient variables.
func LoadWithEnvironment(path, environmentFile string) (Config, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return defaultConfiguration(), errors.New("CONFIG_UNREADABLE")
	}
	return decodeWithEnvironment(b, environmentFile)
}

func defaultConfiguration() Config {
	var c Config
	c.AWS = DefaultAWSConfiguration()
	c.ModelCache = DefaultModelCacheConfiguration()
	policy := DefaultRuntimePolicy()
	c.Runtime = &policy
	c.Database.Engine = PostgreSQLDatabaseEngine
	c.Database.MySQLMigrations = DefaultMySQLMigrationPolicy()
	c.Database.MaxOpenConnections = 10
	c.Database.OperatorReservedConnections = 3
	c.Database.MaxIdleConnections = 5
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	return c
}

// Decode validates a configuration snapshot supplied by its owning component,
// including bounded protected operator inputs read through os.Root.
func Decode(b []byte) (Config, error) { return decodeWithEnvironment(b, "") }

func decodeWithEnvironment(b []byte, environmentFile string) (Config, error) {
	c := defaultConfiguration()
	var e error
	if e = validateTopologyYAML(b); e != nil {
		return c, e
	}
	if e = validateServerTransportYAML(b); e != nil {
		return c, e
	}
	if e = validateObjectStorageYAML(b); e != nil {
		return c, e
	}
	if e = validateModelCacheYAML(b); e != nil {
		return c, e
	}
	if e = validateDatabaseYAML(b); e != nil {
		return c, e
	}
	if e = validateDatabaseSelectionYAML(b); e != nil {
		return c, e
	}
	if e = validateMySQLBackupYAML(b); e != nil {
		return c, e
	}
	if e = validateMySQLRuntimeAccessYAML(b); e != nil {
		return c, e
	}
	if e = validateWorkspaceYAML(b); e != nil {
		return c, e
	}
	if e = validateModelsYAML(b); e != nil {
		return c, e
	}
	if e = validateModelLifecycleYAML(b); e != nil {
		return c, e
	}
	if e = validateLocalizationYAML(b); e != nil {
		return c, e
	}
	if e = validatePortalYAML(b); e != nil {
		return c, e
	}
	if e = validatePluginYAML(b); e != nil {
		return c, e
	}
	if e = validateWorkflowYAML(b); e != nil {
		return c, e
	}
	if e = validateKnowledgeYAML(b); e != nil {
		return c, e
	}
	if e = validateSSOYAML(b); e != nil {
		return c, e
	}
	if e = validateLicensingYAML(b); e != nil {
		return c, e
	}
	if e = validateAWSYAML(b); e != nil {
		return c, e
	}
	if e = validateImageRegistryYAML(b); e != nil {
		return c, e
	}
	if e = validatePrivateClusterYAML(b); e != nil {
		return c, e
	}
	if e = validateReleaseViewYAML(b); e != nil {
		return c, e
	}
	if e = validateMonitoringYAML(b); e != nil {
		return c, e
	}
	if e = validateMiddlewareYAML(b); e != nil {
		return c, e
	}
	if e = validateTaskEventsYAML(b); e != nil {
		return c, e
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if e = d.Decode(&c); e != nil {
		return c, errors.New("CONFIG_INVALID")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("CONFIG_MULTIPLE_DOCUMENTS_REJECTED")
	}
	// A valid explicit override cannot repair a malformed original transport snapshot.
	if e = c.validateServerTransport(); e != nil {
		return c, e
	}
	if e = c.resolveWorkspaceEnvironment(environmentFile); e != nil {
		return c, e
	}
	if e = c.resolveImageRegistryConfiguration(); e != nil {
		return c, e
	}
	if e = c.resolveMongoDBConfiguration(); e != nil {
		return c, e
	}
	if e = c.resolveLicenseEnvironment(); e != nil {
		return c, e
	}
	if e = c.Validate(); e != nil {
		return c, e
	}
	return c, nil
}
func (c Config) Validate() error {
	if e := c.Telemetry.Validate(); e != nil {
		return e
	}
	if e := c.LivenessReplica.Validate(); e != nil {
		return e
	}
	if e := c.AgentIdentity.Validate(); e != nil {
		return e
	}
	if e := c.validateAgentGateway(); e != nil {
		return e
	}
	if e := c.ValidateReleaseViewReference(); e != nil {
		return e
	}
	if e := c.ValidatePrivateClusterReference(); e != nil {
		return e
	}
	if e := c.ValidateMonitoringReference(); e != nil {
		return e
	}
	if e := c.ValidateMiddlewareReferences(); e != nil {
		return e
	}
	if e := c.TaskEvents.Validate(); e != nil {
		return e
	}
	if e := c.validateImageRegistryConfiguration(); e != nil {
		return e
	}
	if c.ObjectStorage != nil && c.ObjectStorage.Enabled && (c.Models.ArtifactRoot == "" || c.Datasets.ArtifactRoot == "" || c.Datasets.PythonExecutable == "" || c.Datasets.ProcessorFile == "") {
		return errors.New("CONFIG_OBJECT_LOCAL_OWNERS_REQUIRED")
	}
	if e := c.ObjectStorage.Validate(c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot); e != nil {
		return e
	}
	if e := c.ValidateDatabaseSelection(); e != nil {
		return e
	}
	if !c.AWS.omitted() {
		if e := c.AWS.Validate(); e != nil {
			return e
		}
	}
	if c.AWS.Enabled && c.AWS.ImportSeconds > c.Policy().HTTP.RequestSeconds {
		return errors.New("CONFIG_AWS_HTTP_BUDGET_INVALID")
	}

	if c.ModelCache != (ModelCacheConfiguration{}) {
		if e := c.ModelCache.Validate(); e != nil {
			return e
		}
	}
	if c.Licensing != nil && c.Licensing.Validate() != nil {
		return errors.New("CONFIG_LICENSING_INVALID")
	}
	if e := c.Localization.Validate(); e != nil {
		return e
	}
	if c.Plugins.Enabled && c.Policy().HTTP.RequestSeconds < c.Policy().Plugins.CommitSeconds {
		return errors.New("CONFIG_PLUGIN_HTTP_BUDGET_INVALID")
	}
	if e := c.Plugins.Validate(); e != nil {
		return e
	}
	if c.Workflows.Enabled && ((!c.Knowledge.Enabled && !c.Plugins.Enabled) || c.Policy().HTTP.RequestSeconds < c.Policy().Workflows.CommitSeconds || c.Policy().HTTP.ShutdownSeconds < c.Policy().Workflows.CommitSeconds+c.Policy().Knowledge.KeyCleanupSeconds+c.Policy().Knowledge.ChildStopSeconds) {
		return errors.New("CONFIG_WORKFLOW_HTTP_BUDGET_INVALID")
	}
	if e := c.Knowledge.Validate(c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot); e != nil {
		return e
	}
	if c.Knowledge.Enabled && (c.Policy().HTTP.RequestSeconds < c.Policy().Knowledge.QuerySeconds+c.Policy().Knowledge.KeyCleanupSeconds+c.Policy().Knowledge.CommitSeconds || c.Policy().HTTP.StartupSeconds < c.Policy().Knowledge.QuerySeconds || c.Policy().HTTP.ShutdownSeconds < c.Policy().Knowledge.KeyCleanupSeconds+c.Policy().Knowledge.CommitSeconds || c.Policy().HTTP.ShutdownSeconds < c.Policy().Knowledge.ChildStopSeconds+c.Policy().Knowledge.CommitSeconds+(c.Policy().Knowledge.MemoryPollMilliseconds+999)/1000) {
		return errors.New("CONFIG_KNOWLEDGE_HTTP_BUDGET_INVALID")
	}
	if e := c.SSO.Validate(); e != nil {
		return e
	}
	if e := c.ModelPreparation.Validate(c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot); e != nil {
		return e
	}
	if e := c.Training.Validate(c.Models.ArtifactRoot, c.Datasets.ArtifactRoot); e != nil {
		return e
	}
	if e := c.Datasets.Validate(c.Models.ArtifactRoot); e != nil {
		return e
	}
	p := c.Policy()
	if c.MongoDB.Enabled {
		if e := c.MongoDB.Validate(); e != nil {
			return e
		}
		if (c.DatabaseEngine() != DamengDatabaseEngine && c.Database.MaxOpenConnections < int32(c.MongoDB.MaximumConcurrent+1)) || c.Datasets.ArtifactRoot == "" || p.HTTP.RequestSeconds < c.MongoDB.TimeoutSeconds+3*c.MongoDB.CloseSeconds+2*p.Datasets.UploadSeconds+4*p.Datasets.CommitSeconds {
			return errors.New("CONFIG_MONGODB_HTTP_BUDGET_INVALID")
		}
	}
	if e := p.Validate(); e != nil {
		return e
	}
	if p.Mail.AttemptSeconds <= c.SMTP.OperationTimeoutSeconds {
		return errors.New("CONFIG_MAIL_TIMEOUT_INVALID")
	}
	if e := c.validateServerTransport(); e != nil {
		return e
	}
	if c.DatabaseEngine() != DamengDatabaseEngine && (c.Database.MaxOpenConnections < MinimumDatabaseConnections || c.Database.MaxOpenConnections > MaximumDatabaseConnections || c.Database.MaxIdleConnections < 0 || c.Database.MaxIdleConnections > c.Database.MaxOpenConnections || c.Database.StatementTimeoutSeconds < 1 || c.Database.StatementTimeoutSeconds > 30) {
		return errors.New("DATABASE_BOUNDS_INVALID")
	}
	// Whether the pool leaves agents a seat matters only to a serving process, which refuses to start
	// with DATABASE_POOL_TOO_SMALL; operator commands run on small pools.
	if c.Database.OperatorReservedConnections < 0 || c.Database.OperatorReservedConnections > MaximumDatabaseConnections {
		return errors.New("DATABASE_BOUNDS_INVALID")
	}
	if c.Database.RoleConnectionLimit != 0 && (c.Database.RoleConnectionLimit < c.Database.MaxOpenConnections || c.Database.RoleConnectionLimit > MaximumRoleConnections) {
		return errors.New("DATABASE_BOUNDS_INVALID")
	}
	if e := c.ValidateDatabaseLockTimeout(); e != nil {
		return e
	}
	sender, senderError := mail.ParseAddress(c.SMTP.FromAddress)
	_, senderDomain, _ := strings.Cut(c.SMTP.FromAddress, "@")
	// The queue schema allows 254 bytes for UUID@domain message IDs.
	if senderError != nil || sender.Address != c.SMTP.FromAddress || len(senderDomain)+36+1 > 254 {
		return errors.New("SMTP_CONFIG_INVALID")
	}
	if c.SMTP.Host == "" || c.SMTP.Port < 1 || c.SMTP.Port > 65535 || strings.ContainsAny(c.SMTP.FromAddress, "\r\n") || !strings.Contains(c.SMTP.FromAddress, "@") {
		return errors.New("SMTP_CONFIG_INVALID")
	}
	if c.SMTP.TLSMode != "starttls_required" && c.SMTP.TLSMode != "implicit_tls" {
		return errors.New("SMTP_TLS_REQUIRED")
	}
	if (c.SMTP.Username == "") != (c.SMTP.PasswordFile == "") || c.SMTP.ConnectTimeoutSeconds < 1 || c.SMTP.ConnectTimeoutSeconds > 10 || c.SMTP.OperationTimeoutSeconds < 1 || c.SMTP.OperationTimeoutSeconds > 30 {
		return errors.New("SMTP_BOUNDS_INVALID")
	}
	return nil
}
func (c Config) TLSConfig() (*tls.Config, error) {
	if e := c.validateServerTransport(); e != nil {
		return nil, e
	}
	if c.Server.EffectiveTransportMode() != TransportDirectHTTPS {
		return nil, nil
	}
	t, e := tlsConfigForOrigins(c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile, []string{c.Server.PortalOrigin, c.Server.AdminOrigin})
	if e != nil || c.MachineServer != nil {
		return t, e
	}
	return c.requestAgentCertificates(t)
}

// Both listeners use the same pinned material reader and actual pair, validity,
// SAN and minimum-version validation. Origins were structurally admitted by the
// selected owner; this helper never selects a transport or rewrites an address.
func tlsConfigForOrigins(certificateFile, privateKeyFile string, origins []string) (*tls.Config, error) {
	certificatePEM, privateKeyPEM, e := readTLSMaterialPair(certificateFile, privateKeyFile)
	if e != nil {
		return nil, e
	}
	cert, e := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if e != nil {
		return nil, errors.New("TLS_KEYPAIR_INVALID")
	}
	leaf, e := x509.ParseCertificate(cert.Certificate[0])
	if e != nil || time.Now().Before(leaf.NotBefore) || time.Now().After(leaf.NotAfter) {
		return nil, errors.New("TLS_CERTIFICATE_INVALID")
	}
	for _, s := range origins {
		u, _ := url.Parse(s)
		if e = leaf.VerifyHostname(u.Hostname()); e != nil {
			return nil, errors.New("TLS_HOSTNAME_INVALID")
		}
	}
	// HTTP/2 is offered for the agent session stream (ADR 0007); HTTP/1.1 stays available.
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}}, nil
}

// DecodeWithEnvironment validates an immutable configuration snapshot and the
// same explicit protected policy file used by LoadProtectedWithEnvironment.
// The caller owns reading/protecting the snapshot; ambient values have no authority.
func DecodeWithEnvironment(raw []byte, environmentFile string) (Config, error) {
	return decodeWithEnvironment(raw, environmentFile)
}

// pinnedConnections is the pool connection a serving process holds for its installation lease,
// for as long as it runs.
const pinnedConnections = 1

// RoleConnections is the serving role's connection limit: role_connection_limit, or the pool
// when it is not set.
func (c Config) RoleConnections() int32 {
	if c.Database.RoleConnectionLimit != 0 {
		return c.Database.RoleConnectionLimit
	}
	return c.Database.MaxOpenConnections
}

// AgentDatabaseSeats is how many agent calls may use the database at once, or zero when no pool
// bounds them: the pool less the lease's pinned connection and the operators' reserve. A pool
// that leaves agents nothing cannot serve them.
func (c Config) AgentDatabaseSeats() (int, error) {
	if c.DatabaseEngine() == DamengDatabaseEngine {
		return 0, nil
	}
	seats := int(c.Database.MaxOpenConnections - c.Database.OperatorReservedConnections - pinnedConnections)
	if seats < 1 {
		return 0, errors.New("DATABASE_POOL_TOO_SMALL")
	}
	return seats, nil
}
