package config

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
)

// ManagementProbe identifies one private gRPC TLS listener. These are verified
// transport probes, never administrator RPC mutation targets or a bypass switch.
type ObjectStorageManagementProbe struct {
	Kind                  string   `yaml:"kind" json:"kind"`
	Authority             string   `yaml:"authority" json:"authority"`
	DialAddresses         []string `yaml:"dial_addresses" json:"dial_addresses"`
	CAFile                string   `yaml:"ca_file" json:"ca_file"`
	ClientCertificateFile string   `yaml:"client_certificate_file" json:"client_certificate_file"`
	ClientKeyFile         string   `yaml:"client_key_file" json:"client_key_file"`
}

func (p ObjectStorageManagementProbe) Validate() error {
	bad := errors.New("CONFIG_OBJECT_STORAGE_MANAGEMENT_INVALID")
	switch p.Kind {
	case "s3_grpc", "master_grpc", "filer_grpc", "volume_grpc":
	default:
		return bad
	}
	host, port, e := net.SplitHostPort(p.Authority)
	if e != nil || host == "" || strings.ContainsAny(host, "/%?#@") || strings.ContainsFunc(host, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return bad
	}
	number, e := strconv.Atoi(port)
	if e != nil || number < 1 || number > 65535 {
		return bad
	}
	if len(p.DialAddresses) < 1 || len(p.DialAddresses) > 8 {
		return bad
	}
	seen := map[string]bool{}
	for _, v := range p.DialAddresses {
		ip, e := netip.ParseAddr(v)
		if e != nil || ip.String() != v || !ip.IsPrivate() && !ip.IsLoopback() || seen[v] {
			return bad
		}
		seen[v] = true
	}
	if !objectStoragePath(p.CAFile) || !objectStoragePath(p.ClientCertificateFile) || !objectStoragePath(p.ClientKeyFile) || p.ClientKeyFile == p.ClientCertificateFile {
		return bad
	}
	return nil
}
func (p ObjectStorageManagementProbe) Material() (ca, certificate, key []byte, err error) {
	if e := p.Validate(); e != nil {
		return nil, nil, nil, e
	}
	code := "CONFIG_OBJECT_STORAGE_MANAGEMENT_SECRET_INVALID"
	ca, err = readPrivateFile(p.CAFile, 1<<20, code)
	if err != nil {
		return nil, nil, nil, err
	}
	certificate, err = readPrivateFile(p.ClientCertificateFile, 1<<20, code)
	if err != nil {
		return nil, nil, nil, err
	}
	key, err = readPrivateFile(p.ClientKeyFile, 1<<20, code)
	if err != nil {
		return nil, nil, nil, err
	}
	return ca, certificate, key, nil
}
