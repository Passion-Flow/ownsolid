package install

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"go.yaml.in/yaml/v3"
)

type historyTLSFiles struct {
	CAFile          string `yaml:"ca_file"`
	CertificateFile string `yaml:"certificate_file"`
	PrivateKeyFile  string `yaml:"private_key_file"`
}

type historyStoreConfig struct {
	Listen    string          `yaml:"listen"`
	Retention string          `yaml:"retention"`
	TLS       historyTLSFiles `yaml:"tls"`
}

type historyResources struct {
	Memory string `yaml:"memory"`
	CPUs   string `yaml:"cpus"`
	PIDs   int    `yaml:"pids"`
}

type fleetHistoryConfig struct {
	FormatVersion      int                `yaml:"format_version"`
	Root               string             `yaml:"root"`
	Project            string             `yaml:"project"`
	UID                int                `yaml:"uid"`
	GID                int                `yaml:"gid"`
	Subnet             string             `yaml:"subnet"`
	MetricsImage       string             `yaml:"metrics_image"`
	EvaluatorImage     string             `yaml:"evaluator_image"`
	EvaluatorListen    string             `yaml:"evaluator_listen"`
	Raw                historyStoreConfig `yaml:"raw"`
	Long               historyStoreConfig `yaml:"long"`
	StoreResources     historyResources   `yaml:"store_resources"`
	EvaluatorResources historyResources   `yaml:"evaluator_resources"`
	RulesFile          string             `yaml:"rules_file"`
	RulesSHA256        string             `yaml:"rules_sha256"`
	MonitoringFile     string             `yaml:"monitoring_file"`
	ControlCAFile      string             `yaml:"control_ca_file"`
	RequestSeconds     int                `yaml:"request_seconds"`
}

type historyPolicy struct {
	RawOrigin         string `json:"raw_origin"`
	LongOrigin        string `json:"long_origin"`
	RawHorizonSeconds int64  `json:"raw_horizon_seconds"`
	MaxRangeSeconds   int64  `json:"max_range_seconds"`
	SettlingSeconds   int64  `json:"settling_seconds"`
}

type historySite struct {
	config     fleetHistoryConfig
	files      map[string][]byte
	rawRoots   *x509.CertPool
	longRoots  *x509.CertPool
	generation string
}

func historyFailure(code, detail string) error {
	return errors.New("INSTALL_HISTORY_" + code + ": " + detail)
}

func historyAddress(value string, loopbackOnly bool) (netip.AddrPort, bool) {
	addr, err := netip.ParseAddrPort(value)
	return addr, err == nil && addr.String() == value && addr.Addr().Is4() &&
		(addr.Addr().IsLoopback() || !loopbackOnly && addr.Addr().IsPrivate()) && addr.Port() >= 1024
}

func historyPath(value string) bool {
	return canonicalPath(value) && !strings.ContainsAny(value, "$\x00\r\n")
}

func historyImage(value string) bool {
	if strings.HasPrefix(value, "sha256:") && wire.ValidSHA256(strings.TrimPrefix(value, "sha256:")) {
		return true
	}
	parts := strings.Split(value, "@sha256:")
	return len(parts) == 2 && pinnedImage(value) && wire.ValidSHA256(parts[1])
}

func (r historyResources) valid() bool {
	return memoryPattern.MatchString(r.Memory) && cpusPattern.MatchString(r.CPUs) && r.PIDs >= 32 && r.PIDs <= 4096
}

func historyRetention(value string) (int64, bool) {
	if !retentionPattern.MatchString(value) {
		return 0, false
	}
	var days int64
	for _, char := range strings.TrimSuffix(value, "d") {
		days = days*10 + int64(char-'0')
	}
	return days * 86400, true
}

func loadHistorySite(path string) (historySite, error) {
	site := historySite{files: map[string][]byte{}}
	raw, err := bundle.ReadProtectedConfiguration(path)
	if err != nil {
		return site, historyFailure("CONFIG_INVALID", "use a protected configuration file")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	c := &site.config
	if decoder.Decode(c) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return site, historyFailure("CONFIG_INVALID", "use one YAML document with known fields")
	}
	rawAddr, rawOK := historyAddress(c.Raw.Listen, false)
	longAddr, longOK := historyAddress(c.Long.Listen, false)
	evalAddr, evalOK := historyAddress(c.EvaluatorListen, true)
	subnet, subnetErr := netip.ParsePrefix(c.Subnet)
	rawRetention, rawRetentionOK := historyRetention(c.Raw.Retention)
	longRetention, longRetentionOK := historyRetention(c.Long.Retention)
	if c.FormatVersion != 1 || !historyPath(c.Root) || c.Root == "/" ||
		!projectPattern.MatchString(c.Project) || c.UID < 1 || c.UID >= 1<<31 || c.GID < 1 || c.GID >= 1<<31 ||
		subnetErr != nil || !subnet.Addr().Is4() || !subnet.Addr().IsPrivate() || subnet.Masked() != subnet ||
		subnet.Bits() < stateSubnetMinBits || subnet.Bits() > stateSubnetMaxBits ||
		!rawOK || !longOK || !evalOK || rawAddr == longAddr || rawAddr == evalAddr || longAddr == evalAddr ||
		!rawRetentionOK || !longRetentionOK || longRetention <= rawRetention ||
		!historyImage(c.MetricsImage) || !historyImage(c.EvaluatorImage) ||
		!c.StoreResources.valid() || !c.EvaluatorResources.valid() || c.RequestSeconds < 5 || c.RequestSeconds > 1800 ||
		!wire.ValidSHA256(c.RulesSHA256) {
		return site, historyFailure("CONFIG_INVALID", "set private distinct listeners, a private subnet, digest-pinned images, retention and bounded resources")
	}
	ancestor := c.Root
	for {
		_, err := os.Lstat(ancestor)
		if !errors.Is(err, os.ErrNotExist) {
			break
		}
		ancestor = filepath.Dir(ancestor)
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil || resolved != ancestor {
		return site, historyFailure("CONFIG_INVALID", "the deployment root must have a canonical parent")
	}
	inputs := map[string]string{
		"raw/ca.pem": c.Raw.TLS.CAFile, "raw/certificate.pem": c.Raw.TLS.CertificateFile, "raw/private-key.pem": c.Raw.TLS.PrivateKeyFile,
		"long/ca.pem": c.Long.TLS.CAFile, "long/certificate.pem": c.Long.TLS.CertificateFile, "long/private-key.pem": c.Long.TLS.PrivateKeyFile,
		"evaluator/rules.yaml": c.RulesFile, "control/monitoring.json": c.MonitoringFile, "control/ca.pem": c.ControlCAFile,
	}
	for name, source := range inputs {
		if !historyPath(source) || source == c.Root || strings.HasPrefix(source, c.Root+string(filepath.Separator)) {
			return site, historyFailure("INPUT_INVALID", "keep protected input files outside the dedicated deployment root")
		}
		site.files[name], err = bundle.ReadProtectedConfiguration(source)
		if err != nil || len(site.files[name]) == 0 {
			return site, historyFailure("INPUT_INVALID", "use readable protected regular input files")
		}
	}
	if wire.Digest(site.files["evaluator/rules.yaml"]) != c.RulesSHA256 {
		return site, historyFailure("RULES_INVALID", "the local recording rules must match their approved SHA-256")
	}
	addresses := NetworkConfig{StateSubnet: c.Subnet}.stateAddresses()
	controlRoots, err := historyRoots(site.files["control/ca.pem"])
	if err != nil {
		return site, err
	}
	for _, endpoint := range []struct {
		name     string
		host     netip.AddrPort
		internal netip.Addr
	}{{"raw", rawAddr, addresses.Postgres}, {"long", longAddr, addresses.Metrics}} {
		roots, err := historyRoots(site.files[endpoint.name+"/ca.pem"])
		if err != nil {
			return site, err
		}
		pair, err := tls.X509KeyPair(site.files[endpoint.name+"/certificate.pem"], site.files[endpoint.name+"/private-key.pem"])
		if err != nil {
			return site, historyFailure("TLS_INVALID", "the store certificate and private key must match")
		}
		chain := x509.NewCertPool()
		for _, raw := range pair.Certificate[1:] {
			cert, err := x509.ParseCertificate(raw)
			if err != nil {
				return site, historyFailure("TLS_INVALID", "provide a valid certificate chain")
			}
			chain.AddCert(cert)
		}
		for _, check := range []struct {
			roots *x509.CertPool
			host  string
		}{
			{roots, endpoint.host.Addr().String()}, {roots, endpoint.internal.String()}, {controlRoots, endpoint.host.Addr().String()},
		} {
			if _, err := pair.Leaf.Verify(x509.VerifyOptions{Roots: check.roots, Intermediates: chain, DNSName: check.host}); err != nil {
				return site, historyFailure("TLS_INVALID", "trusted current certificates must cover both the private listener and internal store IP")
			}
		}
		if endpoint.name == "raw" {
			site.rawRoots = roots
		} else {
			site.longRoots = roots
		}
	}
	site.files["evaluator/raw-ca.pem"] = site.files["raw/ca.pem"]
	site.files["evaluator/long-ca.pem"] = site.files["long/ca.pem"]
	var policy map[string]json.RawMessage
	var history historyPolicy
	var format int
	if wire.StrictJSON(site.files["control/monitoring.json"], &policy) != nil ||
		wire.StrictJSON(policy["format_version"], &format) != nil || format != 1 ||
		wire.StrictJSON(policy["fleet_history"], &history) != nil ||
		history.RawOrigin != "https://"+c.Raw.Listen || history.LongOrigin != "https://"+c.Long.Listen ||
		history.SettlingSeconds < 30 || history.SettlingSeconds > 300 ||
		history.RawHorizonSeconds <= 45+history.SettlingSeconds || history.RawHorizonSeconds > rawRetention ||
		history.MaxRangeSeconds < 60 || history.MaxRangeSeconds > longRetention || history.MaxRangeSeconds > 366*86400 {
		return site, historyFailure("POLICY_INVALID", "the protected monitoring policy must select these HTTPS origins and fit the store retention")
	}
	hashes := map[string]string{}
	for name, contents := range site.files {
		hashes[name] = wire.Digest(contents)
	}
	site.generation = wire.Digest(historyJSON(struct {
		Config fleetHistoryConfig
		Files  map[string]string
	}{*c, hashes}))
	return site, nil
}

func historyRoots(raw []byte) (*x509.CertPool, error) {
	roots := x509.NewCertPool()
	count := 0
	for len(bytes.TrimSpace(raw)) > 0 {
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, historyFailure("TLS_INVALID", "CA files must contain certificate-only PEM")
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, historyFailure("TLS_INVALID", "CA files must contain certificate-only PEM")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		now := time.Now()
		if err != nil || !cert.IsCA || !cert.BasicConstraintsValid || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return nil, historyFailure("TLS_INVALID", "CA certificates must be valid and current")
		}
		roots.AddCert(cert)
		count++
		raw = rest
	}
	if count == 0 {
		return nil, historyFailure("TLS_INVALID", "a nonempty trusted CA bundle is required")
	}
	return roots, nil
}

func historyJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return append(raw, '\n')
}
