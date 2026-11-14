package install

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	generatedCAValidity     = 10 * 365 * 24 * time.Hour
	generatedServerValidity = 2 * 365 * 24 * time.Hour
	serverReissueWindow     = 30 * 24 * time.Hour
	agentCAFile             = "agent-ca.pem"
	agentCAKeyFile          = "agent-ca-key.pem"
	gatewayCAFile           = "model-gateway-ca.pem"
	gatewayCAKeyFile        = "model-gateway-ca-key.pem"
)

var errTLSGeneration = errors.New("INSTALL_TLS_GENERATION_FAILED")

// materializeCA creates a CA of the installation's own once. An existing one is left alone,
// so applying a configuration again never replaces it.
func (c Config) materializeCA(l Layout, certificateFile, keyFile, commonName string) error {
	certificate, key := filepath.Join(l.Config, certificateFile), filepath.Join(l.Config, keyFile)
	if exists(certificate) && exists(key) {
		return nil
	}
	caCertificate, caKey, e := generateCA(commonName)
	if e != nil {
		return e
	}
	if e = writeFile(key, caKey, c, true); e != nil {
		return e
	}
	return writeFile(certificate, caCertificate, c, true)
}

func (c Config) materializeTLS(l Layout) error {
	// The agent CA signs node certificates (ADR 0050) and the model gateway CA signs the
	// serving certificates of the gateways agents run (ADR 0052). Both are separate from the
	// serving CA, which a site that brings its own certificate does not give us.
	if e := c.materializeCA(l, agentCAFile, agentCAKeyFile, "ownsolid agent CA"); e != nil {
		return e
	}
	if e := c.materializeCA(l, gatewayCAFile, gatewayCAKeyFile, "ownsolid model gateway CA"); e != nil {
		return e
	}
	certificate := filepath.Join(l.Config, "certificate.pem")
	privateKey := filepath.Join(l.Config, "private-key.pem")
	authority := filepath.Join(l.Config, "ca.pem")
	if c.TLS.Source == "files" {
		for source, target := range map[string]string{c.TLS.CertificateFile: certificate, c.TLS.PrivateKeyFile: privateKey, c.TLS.CAFile: authority} {
			raw, e := os.ReadFile(source)
			if e != nil {
				return fmt.Errorf("INSTALL_INPUT_UNREADABLE: %s", source)
			}
			if e = writeFile(target, raw, c, true); e != nil {
				return e
			}
		}
		return nil
	}
	hosts, e := c.originHosts()
	if e != nil {
		return e
	}
	caKeyPath := filepath.Join(l.Secrets, "ca-key.pem")
	if !exists(authority) || !exists(caKeyPath) {
		caCertificate, caKey, e := generateCA("ownsolid installation CA")
		if e != nil {
			return e
		}
		if e = writeFile(caKeyPath, caKey, c, false); e != nil {
			return e
		}
		if e = writeFile(authority, caCertificate, c, true); e != nil {
			return e
		}
	}
	caCertificate, e := os.ReadFile(authority)
	if e != nil {
		return errTLSGeneration
	}
	if current, e := os.ReadFile(certificate); e == nil && serverCertificateCurrent(current, caCertificate, hosts) {
		if key, e := os.ReadFile(privateKey); e == nil {
			if _, e = tls.X509KeyPair(current, key); e == nil {
				return nil
			}
		}
	}
	caKey, e := os.ReadFile(caKeyPath)
	if e != nil {
		return errTLSGeneration
	}
	serverCertificate, serverKey, e := issueServerCertificate(caCertificate, caKey, hosts)
	if e != nil {
		return e
	}
	if e = writeFile(privateKey, serverKey, c, true); e != nil {
		return e
	}
	return writeFile(certificate, serverCertificate, c, true)
}

func (c Config) originHosts() ([]string, error) {
	var hosts []string
	for _, origin := range []string{c.Origins.Portal, c.Origins.Admin} {
		u, e := originURL(origin)
		if e != nil {
			return nil, invalid("origins", "must be https origins")
		}
		hosts = append(hosts, u.Hostname())
	}
	return hosts, nil
}

func generateCA(commonName string) (certificate, key []byte, err error) {
	privateKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, errTLSGeneration
	}
	serial, e := serialNumber()
	if e != nil {
		return nil, nil, e
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: commonName},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(generatedCAValidity),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if e != nil {
		return nil, nil, errTLSGeneration
	}
	return encodeCertificate(der), encodeKey(privateKey), nil
}

func issueServerCertificate(caCertificate, caKey []byte, hosts []string) (certificate, key []byte, err error) {
	authority, authorityKey, e := parseAuthority(caCertificate, caKey)
	if e != nil {
		return nil, nil, e
	}
	privateKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, errTLSGeneration
	}
	serial, e := serialNumber()
	if e != nil {
		return nil, nil, e
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: hosts[0]},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(generatedServerValidity),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	der, e := x509.CreateCertificate(rand.Reader, template, authority, &privateKey.PublicKey, authorityKey)
	if e != nil {
		return nil, nil, errTLSGeneration
	}
	return encodeCertificate(der), encodeKey(privateKey), nil
}

// serverCertificateCurrent reports whether the certificate chains to the authority,
// covers every origin host and is not close to expiry.
func serverCertificateCurrent(certificate, authority []byte, hosts []string) bool {
	block, _ := pem.Decode(certificate)
	if block == nil {
		return false
	}
	parsed, e := x509.ParseCertificate(block.Bytes)
	if e != nil || time.Until(parsed.NotAfter) < serverReissueWindow {
		return false
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(authority) {
		return false
	}
	for _, host := range hosts {
		if _, e = parsed.Verify(x509.VerifyOptions{Roots: roots, DNSName: host}); e != nil {
			return false
		}
	}
	return true
}

func parseAuthority(certificate, key []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certificateBlock, _ := pem.Decode(certificate)
	keyBlock, _ := pem.Decode(key)
	if certificateBlock == nil || keyBlock == nil {
		return nil, nil, errTLSGeneration
	}
	authority, e := x509.ParseCertificate(certificateBlock.Bytes)
	if e != nil {
		return nil, nil, errTLSGeneration
	}
	privateKey, e := x509.ParseECPrivateKey(keyBlock.Bytes)
	if e != nil {
		return nil, nil, errTLSGeneration
	}
	return authority, privateKey, nil
}

func serialNumber() (*big.Int, error) {
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return nil, errTLSGeneration
	}
	return serial, nil
}

func encodeCertificate(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func encodeKey(key *ecdsa.PrivateKey) []byte {
	der, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}
