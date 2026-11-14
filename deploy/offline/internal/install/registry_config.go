package install

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"go.yaml.in/yaml/v3"
)

var (
	registryNamePattern = regexp.MustCompile("^[a-z][a-z0-9-]{0,47}$")
	registryUserPattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$")
	registryRepoPattern = regexp.MustCompile("^[a-z0-9]+([.-][a-z0-9]+)*$")
)

type registryConfig struct {
	Root           string `yaml:"root"`
	Name           string `yaml:"name"`
	Listen         string `yaml:"listen"`
	UID            int    `yaml:"uid"`
	GID            int    `yaml:"gid"`
	TLSCAFile      string `yaml:"tls_ca_file"`
	TLSCertFile    string `yaml:"tls_certificate_file"`
	TLSKeyFile     string `yaml:"tls_key_file"`
	CredentialFile string `yaml:"credential_file"`
	RequestSeconds int    `yaml:"request_seconds"`
}

type registryCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type registrySite struct {
	config registryConfig
	addr   netip.AddrPort
	roots  *x509.CertPool
	cert   []byte
	key    []byte
	auth   registryCredential
	digest string
}

func loadRegistrySite(path string) (registrySite, error) {
	var site registrySite
	raw, err := bundle.ReadProtectedConfiguration(path)
	if err != nil {
		return site, errors.New("INSTALL_REGISTRY_CONFIG_INVALID: use a protected site configuration file")
	}
	c := &site.config
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	var extra any
	if d.Decode(c) != nil || d.Decode(&extra) != io.EOF {
		return site, errors.New("INSTALL_REGISTRY_CONFIG_INVALID: use a single document with known settings")
	}
	addr, ok := siteRegistryAddress(c.Listen)
	if !ok ||
		!canonicalPath(c.Root) || c.Root == "/" || strings.Contains(c.Root, ",") || strings.ContainsFunc(c.Root, unicode.IsControl) ||
		!registryNamePattern.MatchString(c.Name) || c.UID < 1 || c.UID >= 1<<31 || c.GID < 1 || c.GID >= 1<<31 ||
		c.RequestSeconds < 1 || c.RequestSeconds > 1800 {
		return site, errors.New("INSTALL_REGISTRY_CONFIG_INVALID: set a dedicated root, name, private IPv4 address with an unprivileged port, runtime UID/GID and request timeout in seconds")
	}
	site.addr = addr
	paths := []string{c.TLSCAFile, c.TLSCertFile, c.TLSKeyFile, c.CredentialFile}
	files := make([][]byte, len(paths))
	for i, p := range paths {
		if !canonicalPath(p) || p == c.Root || strings.HasPrefix(p, c.Root+"/") {
			return site, errors.New("INSTALL_REGISTRY_INPUT_INVALID: put the CA, certificate, key and credentials outside the Registry root")
		}
		files[i], err = bundle.ReadProtectedConfiguration(p)
		if err != nil {
			return site, errors.New("INSTALL_REGISTRY_INPUT_INVALID: use protected regular files for the CA, certificate, key and credentials")
		}
	}
	site.cert, site.key = files[1], files[2]
	site.roots = x509.NewCertPool()
	pair, err := tls.X509KeyPair(site.cert, site.key)
	if err != nil || !site.roots.AppendCertsFromPEM(files[0]) {
		return site, errors.New("INSTALL_REGISTRY_TLS_INVALID: provide a valid CA and matching certificate and private key")
	}
	chain := x509.NewCertPool()
	for _, raw := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return site, errors.New("INSTALL_REGISTRY_TLS_INVALID: provide a valid certificate chain")
		}
		chain.AddCert(cert)
	}
	if _, err := pair.Leaf.Verify(x509.VerifyOptions{Roots: site.roots, Intermediates: chain, DNSName: addr.Addr().String()}); err != nil {
		return site, errors.New("INSTALL_REGISTRY_TLS_INVALID: the CA must verify the certificate for the private listen address")
	}
	if wire.StrictJSON(files[3], &site.auth) != nil || !registryUserPattern.MatchString(site.auth.Username) ||
		len(site.auth.Password) < 20 || len(site.auth.Password) > 72 || strings.ContainsFunc(site.auth.Password, unicode.IsControl) {
		return site, errors.New("INSTALL_REGISTRY_CREDENTIAL_INVALID: provide username and a password of 20 to 72 bytes without control characters")
	}
	raw, _ = json.Marshal(struct {
		Config registryConfig
		CA     string
		Cert   string
		User   string
	}{*c, wire.Digest(files[0]), wire.Digest(site.cert), site.auth.Username})
	site.digest = wire.Digest(raw)
	return site, nil
}

func siteRegistryAddress(value string) (netip.AddrPort, bool) {
	address, err := netip.ParseAddrPort(value)
	return address, err == nil && address.String() == value && address.Addr().Is4() && address.Addr().IsPrivate() && address.Port() >= 1024
}

func (s registrySite) installConfig() Config {
	return Config{InstallRoot: s.config.Root, Runtime: RuntimeConfig{UID: s.config.UID, GID: s.config.GID}}
}

func (s registrySite) loopback() string {
	return netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), s.addr.Port()).String()
}

func (s registrySite) materialPaths() (string, string) {
	return filepath.Join(s.config.Root, "config"), filepath.Join(s.config.Root, "data")
}
