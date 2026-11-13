// Package install owns the one install path: a typed configuration, the files it
// produces under install_root and the ordered steps that bring the platform up.
package install

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/pkg/capabilities"
	"go.yaml.in/yaml/v3"
)

const FormatVersion = 1

// maximumSocketPath is the longest Unix socket address Linux accepts, less its terminator. Going
// over it would only show the day an operator needs the socket.
const maximumSocketPath = 107

// SingleNode is the size an omitted topology selects. Topology sizes share one configuration
// format; which of them this release can run comes from the capability declaration.
const SingleNode = "single-node"

var (
	projectPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	memoryPattern  = regexp.MustCompile(`^[1-9][0-9]{0,5}[mg]$`)
	cpusPattern    = regexp.MustCompile(`^[1-9][0-9]{0,2}(\.[0-9]{1,2})?$`)
	// Both stores read a retention period with this syntax.
	retentionPattern = regexp.MustCompile(`^[1-9][0-9]{0,3}d$`)
)

// controlOwnedKeys are written by the installer and cannot be set through control_settings.
var controlOwnedKeys = []string{"topology", "server", "database", "security", "licensing", "smtp", "telemetry", "models", "agent_identity", "model_gateway_identity"}

type Config struct {
	FormatVersion int             `yaml:"format_version"`
	Topology      TopologyConfig  `yaml:"topology"`
	InstallRoot   string          `yaml:"install_root"`
	ComposeFile   string          `yaml:"compose_file"`
	Project       string          `yaml:"project"`
	Runtime       RuntimeConfig   `yaml:"runtime"`
	Images        ImagesConfig    `yaml:"images"`
	Network       NetworkConfig   `yaml:"network"`
	Origins       OriginsConfig   `yaml:"origins"`
	TLS           TLSConfig       `yaml:"tls"`
	License       LicenseConfig   `yaml:"license"`
	SMTP          SMTPConfig      `yaml:"smtp"`
	Administrator AdminConfig     `yaml:"administrator"`
	Resources     ResourceConfig  `yaml:"resources"`
	Telemetry     TelemetryConfig `yaml:"telemetry"`
	StepSeconds   int             `yaml:"step_seconds"`
	BackupSeconds int             `yaml:"backup_seconds"`
	// ControlSettings are further control plane settings, which the control process
	// validates when it first reads them. Keys the installer owns are refused here.
	ControlSettings map[string]any `yaml:"control_settings"`

	Preflight *PreflightPolicy `yaml:"preflight"`

	BackupSchedule *BackupScheduleConfig `yaml:"backup_schedule"`
	PITR           *PITRConfig           `yaml:"pitr" json:",omitempty"`
}
type TopologyConfig struct {
	Size string `yaml:"size"`
}
type RuntimeConfig struct {
	UID int `yaml:"uid"`
	GID int `yaml:"gid"`
	// PostgresUID is the user the PostgreSQL image runs as. It reads the password file.
	PostgresUID int `yaml:"postgres_uid"`
}
type ImagesConfig struct {
	ArchiveFile string `yaml:"archive_file"`
	Control     string `yaml:"control"`
	Postgres    string `yaml:"postgres"`
	// Metrics and Logs are the telemetry stores of ADR 0009.
	Metrics string `yaml:"metrics"`
	Logs    string `yaml:"logs"`
}
type NetworkConfig struct {
	BindAddress string `yaml:"bind_address"`
	HTTPSPort   int    `yaml:"https_port"`
	// StateSubnet is the private IPv4 subnet of the internal network that carries
	// database and telemetry traffic. Each store takes a fixed address in it, so no
	// service ever asks DNS for one: a lookup while a store is stopped would otherwise
	// be forwarded to the site resolvers, with the host's search domains appended.
	StateSubnet string `yaml:"state_subnet"`
}

// State subnet sizes: large enough for every service of a node, small enough to fit
// beside the site's own networks.
const (
	stateSubnetMinBits = 16
	stateSubnetMaxBits = 28
)

// StateAddresses is the fixed layout of the state subnet.
type StateAddresses struct {
	Postgres, Metrics, Logs netip.Addr
	// Dynamic is the range for containers without a fixed address.
	Dynamic netip.Prefix
}

// stateAddresses splits the state subnet: Docker's bridge takes the first host address,
// the stores the next ones, and containers without a fixed address draw from the upper
// half so they can never take a store's address.
func (n NetworkConfig) stateAddresses() StateAddresses {
	subnet := netip.MustParsePrefix(n.StateSubnet)
	postgres := subnet.Addr().Next().Next()
	metrics := postgres.Next()
	base := subnet.Addr().As4()
	var upper [4]byte
	binary.BigEndian.PutUint32(upper[:], binary.BigEndian.Uint32(base[:])+1<<(31-subnet.Bits()))
	return StateAddresses{Postgres: postgres, Metrics: metrics, Logs: metrics.Next(), Dynamic: netip.PrefixFrom(netip.AddrFrom4(upper), subnet.Bits()+1)}
}

type OriginsConfig struct {
	Portal string `yaml:"portal"`
	Admin  string `yaml:"admin"`
}
type TLSConfig struct {
	Source string `yaml:"source"`
	// AgentCertificateLifetimeHours is how long a node's certificate lasts; zero means control's default.
	AgentCertificateLifetimeHours int    `yaml:"agent_certificate_lifetime_hours"`
	CertificateFile               string `yaml:"certificate_file"`
	PrivateKeyFile                string `yaml:"private_key_file"`
	CAFile                        string `yaml:"ca_file"`
}
type LicenseConfig struct {
	TrustFile string `yaml:"trust_file"`
}
type SMTPConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	FromAddress  string `yaml:"from_address"`
	TLSMode      string `yaml:"tls_mode"`
	CAFile       string `yaml:"ca_file"`
	Username     string `yaml:"username"`
	PasswordFile string `yaml:"password_file"`
}
type AdminConfig struct {
	Email            string `yaml:"email"`
	DisplayName      string `yaml:"display_name"`
	InstallationName string `yaml:"installation_name"`
	WorkspaceName    string `yaml:"workspace_name"`
}

// controlPoolConnections is control's database pool, which the installer leaves at control's
// default, and operatorConnections what PostgreSQL keeps beyond the serving role: operator
// commands and its own reserved connections.
const (
	controlPoolConnections = 10
	operatorConnections    = 10
)

type ResourceConfig struct {
	ControlMemory          string `yaml:"control_memory"`
	ControlCPUs            string `yaml:"control_cpus"`
	PostgresMemory         string `yaml:"postgres_memory"`
	PostgresCPUs           string `yaml:"postgres_cpus"`
	PostgresMaxConnections int    `yaml:"postgres_max_connections"`
	// DatabaseRoleConnections is the serving role's connection limit, for control and the agent
	// gateways beside it (ADR 0048). Zero leaves it at control's pool.
	DatabaseRoleConnections int    `yaml:"database_role_connections"`
	MetricsMemory           string `yaml:"metrics_memory"`
	MetricsCPUs             string `yaml:"metrics_cpus"`
	LogsMemory              string `yaml:"logs_memory"`
	LogsCPUs                string `yaml:"logs_cpus"`
}

// TelemetryConfig selects store retention and optional private fleet-history inputs.
type TelemetryConfig struct {
	MetricsRetention string `yaml:"metrics_retention"`
	LogsRetention    string `yaml:"logs_retention"`
	// FleetHistoryControlDirectory selects the protected Control outputs of fleet-history apply.
	FleetHistoryControlDirectory string `yaml:"fleet_history_control_directory,omitempty"`
}

// Docker's CPU weight: 1024 is one default container, and the daemon refuses more than the
// maximum.
const (
	cpuSharesPerCPU = 1024
	maxCPUShares    = 262144
)

// cpuShares turns a CPU count into a Docker CPU weight. PostgreSQL gets a weight, not a quota:
// a quota stalls every backend for the rest of its period once a burst has spent it (ADR 0038).
func cpuShares(cpus string) (int, error) {
	count, err := strconv.ParseFloat(cpus, 64)
	if err != nil {
		return 0, errors.New("must be a number of CPUs like 2 or 1.5")
	}
	shares := int(math.Round(count * cpuSharesPerCPU))
	if shares > maxCPUShares {
		return 0, fmt.Errorf("must be at most %d CPUs", maxCPUShares/cpuSharesPerCPU)
	}
	return shares, nil
}

func defaults() Config {
	var c Config
	c.Topology.Size = SingleNode
	c.Project = "ownsolid"
	c.Runtime = RuntimeConfig{UID: 10001, GID: 10001, PostgresUID: 999}
	c.Network.BindAddress = "127.0.0.1"
	c.Network.HTTPSPort = 8443
	c.Network.StateSubnet = "172.31.254.0/26"
	c.TLS.Source = "generate"
	c.SMTP.Port = 587
	c.SMTP.TLSMode = "starttls_required"
	c.Resources = ResourceConfig{
		ControlMemory: "1g", ControlCPUs: "2", PostgresMemory: "2g", PostgresCPUs: "2", PostgresMaxConnections: 50,
		MetricsMemory: "2g", MetricsCPUs: "2", LogsMemory: "1g", LogsCPUs: "1",
	}
	c.Telemetry = TelemetryConfig{MetricsRetention: "30d", LogsRetention: "30d"}
	c.StepSeconds = 600
	c.BackupSeconds = 3600
	return c
}

// Load reads and validates the configuration. Unknown keys and invalid values fail.
func Load(path string) (Config, error) {
	c, err := readConfiguration(path)
	if err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func readConfiguration(path string) (Config, error) {
	raw, e := os.ReadFile(path)
	if e != nil {
		return Config{}, errors.New("INSTALL_CONFIG_UNREADABLE")
	}
	return decodeConfiguration(raw)
}

func Decode(raw []byte) (Config, error) {
	c, err := decodeConfiguration(raw)
	if err != nil {
		return Config{}, err
	}
	if err = c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func decodeConfiguration(raw []byte) (Config, error) {
	c := defaults()
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if e := decoder.Decode(&c); e != nil {
		return Config{}, fmt.Errorf("INSTALL_CONFIG_INVALID: %s", firstLine(e.Error()))
	}
	if e := decoder.Decode(&struct{}{}); !errors.Is(e, io.EOF) {
		return Config{}, errors.New("INSTALL_CONFIG_INVALID: more than one document")
	}
	return c, nil
}

func invalid(field, rule string) error {
	return fmt.Errorf("INSTALL_CONFIG_INVALID: %s %s", field, rule)
}

// validateTopologySize accepts a declared size this release can run, and refuses the others
// before anything changes on the host, with the declared reason.
func validateTopologySize(size string) error {
	sizes, found := capabilities.Lookup(capabilities.InstallSizes)
	if !found {
		return errors.New("CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED: this release declares no installation sizes")
	}
	option, found := sizes.Lookup(size)
	if !found {
		names := make([]string, len(sizes.Options))
		for i, known := range sizes.Options {
			names[i] = known.ID
		}
		return invalid("topology.size", "must be one of "+strings.Join(names, ", "))
	}
	if option.State != capabilities.Ready {
		return fmt.Errorf("CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED: this release cannot run %s: %s (roadmap item %s). "+
			"`ownsolid-install capabilities` lists what it can", size, option.Reason, option.RoadmapItem)
	}
	return nil
}

func (c Config) Validate() error { return c.validate(false) }

func (c Config) validate(fromBundle bool) error {
	if c.FormatVersion != FormatVersion {
		return invalid("format_version", fmt.Sprintf("must be %d", FormatVersion))
	}
	if e := validateTopologySize(c.Topology.Size); e != nil {
		return e
	}
	if len(c.SocketPath()) > maximumSocketPath {
		return errors.New("INSTALL_ROOT_TOO_LONG: install_root must leave room for the operator socket, at most " + strconv.Itoa(maximumSocketPath) + " bytes")
	}
	if !canonicalPath(c.InstallRoot) || c.InstallRoot == "/" {
		return invalid("install_root", "must be an absolute clean path below /")
	}
	if fromBundle && (c.ComposeFile != "" || c.Images != (ImagesConfig{})) {
		return invalid("compose_file and images", "must be omitted when installing a signed bundle")
	}
	if !fromBundle && !canonicalPath(c.ComposeFile) {
		return invalid("compose_file", "must be an absolute clean path")
	}
	if !projectPattern.MatchString(c.Project) {
		return invalid("project", "must be lowercase letters, digits, hyphen or underscore")
	}
	if c.Runtime.UID < 1 || c.Runtime.GID < 1 || c.Runtime.PostgresUID < 1 {
		return invalid("runtime", "uid, gid and postgres_uid must be positive: the platform never runs as root")
	}
	if c.Images.ArchiveFile != "" && !canonicalPath(c.Images.ArchiveFile) {
		return invalid("images.archive_file", "must be an absolute clean path")
	}
	if !fromBundle {
		for field, image := range map[string]string{"images.control": c.Images.Control, "images.postgres": c.Images.Postgres, "images.metrics": c.Images.Metrics, "images.logs": c.Images.Logs} {
			if !pinnedImage(image) {
				return invalid(field, "must name an image by tag or digest, and never latest")
			}
		}
	}
	if net.ParseIP(c.Network.BindAddress) == nil {
		return invalid("network.bind_address", "must be an IP address")
	}
	if c.Network.HTTPSPort < 1 || c.Network.HTTPSPort > 65535 {
		return invalid("network.https_port", "must be 1 to 65535")
	}
	if subnet, e := netip.ParsePrefix(c.Network.StateSubnet); e != nil || !subnet.Addr().Is4() || subnet.Masked() != subnet ||
		!subnet.Addr().IsPrivate() || subnet.Bits() < stateSubnetMinBits || subnet.Bits() > stateSubnetMaxBits {
		return invalid("network.state_subnet", fmt.Sprintf("must be a private IPv4 network address with a prefix of /%d to /%d, for example 172.31.254.0/26", stateSubnetMinBits, stateSubnetMaxBits))
	}
	if e := c.validateOrigins(); e != nil {
		return e
	}
	if e := c.validateTLS(); e != nil {
		return e
	}
	if !canonicalPath(c.License.TrustFile) {
		return invalid("license.trust_file", "must name the issuer trust file by absolute clean path")
	}
	if e := c.validateSMTP(); e != nil {
		return e
	}
	if e := c.validateAdministrator(); e != nil {
		return e
	}
	r := c.Resources
	for _, memory := range []string{r.ControlMemory, r.PostgresMemory, r.MetricsMemory, r.LogsMemory} {
		if !memoryPattern.MatchString(memory) {
			return invalid("resources", "memory must look like 512m or 2g")
		}
	}
	for _, cpus := range []string{r.ControlCPUs, r.PostgresCPUs, r.MetricsCPUs, r.LogsCPUs} {
		if !cpusPattern.MatchString(cpus) {
			return invalid("resources", "cpus must look like 2 or 1.5")
		}
	}
	if _, err := cpuShares(r.PostgresCPUs); err != nil {
		return invalid("resources.postgres_cpus", err.Error())
	}
	if !retentionPattern.MatchString(c.Telemetry.MetricsRetention) || !retentionPattern.MatchString(c.Telemetry.LogsRetention) {
		return invalid("telemetry", "metrics_retention and logs_retention must be whole days like 30d")
	}
	if c.Telemetry.FleetHistoryControlDirectory != "" {
		if !historyPath(c.Telemetry.FleetHistoryControlDirectory) {
			return invalid("telemetry.fleet_history_control_directory", "must name the protected fleet-history Control directory")
		}
		if _, present := c.ControlSettings["monitoring_config_file"]; present {
			return invalid("control_settings.monitoring_config_file", "use telemetry.fleet_history_control_directory as the single owner")
		}
	}
	if r.PostgresMaxConnections < 20 || r.PostgresMaxConnections > 1000 {
		return invalid("resources.postgres_max_connections", "must be 20 to 1000")
	}
	if r.DatabaseRoleConnections != 0 && (r.DatabaseRoleConnections < controlPoolConnections || r.DatabaseRoleConnections > r.PostgresMaxConnections-operatorConnections) {
		return invalid("resources.database_role_connections", fmt.Sprintf("must be 0, or %d to postgres_max_connections less %d", controlPoolConnections, operatorConnections))
	}
	if c.StepSeconds < 30 || c.StepSeconds > 7200 {
		return invalid("step_seconds", "must be 30 to 7200")
	}
	if c.BackupSeconds < 60 || c.BackupSeconds > 86400 {
		return invalid("backup_seconds", "must be 60 to 86400")
	}
	if c.BackupSchedule != nil {
		if err := c.BackupSchedule.validate(); err != nil {
			return err
		}
	}
	if c.PITR != nil {
		if err := c.PITR.validate(); err != nil {
			return err
		}
	}
	if c.Preflight != nil {
		if e := c.Preflight.validate(); e != nil {
			return e
		}
	}
	for _, key := range controlOwnedKeys {
		if _, present := c.ControlSettings[key]; present {
			return invalid("control_settings."+key, "is set by the installer from the typed fields")
		}
	}
	if _, err := c.machineServingIntent(); err != nil {
		return err
	}
	return nil
}

func (c Config) validateOrigins() error {
	for field, origin := range map[string]string{"origins.portal": c.Origins.Portal, "origins.admin": c.Origins.Admin} {
		if _, e := originURL(origin); e != nil {
			return invalid(field, "must be an https origin without a path, for example https://portal.example.invalid:8443")
		}
	}
	if c.Origins.Portal == c.Origins.Admin {
		return invalid("origins", "portal and admin must differ: the host selects the audience")
	}
	return nil
}

func (c Config) validateTLS() error {
	if hours := c.TLS.AgentCertificateLifetimeHours; hours != 0 && (hours < 24 || hours > 8760) {
		return invalid("tls.agent_certificate_lifetime_hours", "must be from 24 to 8760")
	}
	t := c.TLS
	switch t.Source {
	case "generate":
		if t.CertificateFile != "" || t.PrivateKeyFile != "" || t.CAFile != "" {
			return invalid("tls", "file references apply only when source is files")
		}
	case "files":
		if !canonicalPath(t.CertificateFile) || !canonicalPath(t.PrivateKeyFile) || !canonicalPath(t.CAFile) {
			return invalid("tls", "certificate_file, private_key_file and ca_file must be absolute clean paths")
		}
	default:
		return invalid("tls.source", "must be generate or files")
	}
	return nil
}

func (c Config) validateSMTP() error {
	s := c.SMTP
	if s.Host == "" || strings.ContainsAny(s.Host, " /\r\n") {
		return invalid("smtp.host", "must name the internal relay")
	}
	if s.Port < 1 || s.Port > 65535 {
		return invalid("smtp.port", "must be 1 to 65535")
	}
	if a, e := mail.ParseAddress(s.FromAddress); e != nil || a.Address != s.FromAddress {
		return invalid("smtp.from_address", "must be a bare email address")
	}
	if s.TLSMode != "starttls_required" && s.TLSMode != "implicit_tls" {
		return invalid("smtp.tls_mode", "must be starttls_required or implicit_tls")
	}
	if s.CAFile != "" && !canonicalPath(s.CAFile) {
		return invalid("smtp.ca_file", "must be an absolute clean path")
	}
	if (s.Username == "") != (s.PasswordFile == "") || s.PasswordFile != "" && !canonicalPath(s.PasswordFile) {
		return invalid("smtp", "username and password_file go together, and password_file must be an absolute clean path")
	}
	return nil
}

func (c Config) validateAdministrator() error {
	a := c.Administrator
	if parsed, e := mail.ParseAddress(a.Email); e != nil || parsed.Address != a.Email {
		return invalid("administrator.email", "must be a bare email address")
	}
	if a.DisplayName == "" || a.InstallationName == "" || a.WorkspaceName == "" {
		return invalid("administrator", "display_name, installation_name and workspace_name are required")
	}
	return nil
}

func canonicalPath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func pinnedImage(image string) bool {
	if image == "" || strings.ContainsAny(image, " \t\r\n") {
		return false
	}
	if name, digest, found := strings.Cut(image, "@"); found {
		return name != "" && regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(digest)
	}
	slash := strings.LastIndex(image, "/")
	colon := strings.LastIndex(image, ":")
	return colon > slash && colon < len(image)-1 && image[colon+1:] != "latest"
}

func originURL(origin string) (*url.URL, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("origin invalid")
	}
	return u, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
