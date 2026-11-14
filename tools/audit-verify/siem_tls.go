//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const maxSIEMMaterial = 1 << 20

var (
	tlsNameLabel       = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	errSIEMPinMismatch = errors.New("AUDIT_SIEM_TLS_PIN_MISMATCH")
)

type siemGrant struct {
	Version                 int    `json:"version"`
	TenantID                string `json:"tenant_id"`
	Endpoint                string `json:"endpoint"`
	ServerName              string `json:"server_name"`
	ServerCertificateSHA256 string `json:"server_certificate_sha256"`
	CAFile                  string `json:"ca_file"`
	ClientCertificateFile   string `json:"client_certificate_file"`
	ClientKeyFile           string `json:"client_key_file"`
	CursorDirectory         string `json:"cursor_directory"`
}

func (g siemGrant) binding() string {
	raw, _ := json.Marshal([5]string{"ownsolid.audit.siem.v1", g.TenantID, g.Endpoint, g.ServerName, g.ServerCertificateSHA256})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validTLSName(name string) bool {
	if addr, err := netip.ParseAddr(name); err == nil {
		return addr.Zone() == ""
	}
	if len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if !tlsNameLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func loadSIEMGrant(path string) (siemGrant, *tls.Config, error) {
	var grant siemGrant
	invalid := errors.New("AUDIT_SIEM_GRANT_INVALID")
	raw, err := readSIEMFile(path, true, 4096)
	if err != nil || !exactSIEMJSON(raw, &grant, []string{
		"version", "tenant_id", "endpoint", "server_name", "server_certificate_sha256",
		"ca_file", "client_certificate_file", "client_key_file", "cursor_directory",
	}) {
		return grant, nil, invalid
	}
	endpoint, err := netip.ParseAddrPort(grant.Endpoint)
	addr := endpoint.Addr().Unmap()
	_, pinOK := hashBytes(grant.ServerCertificateSHA256)
	if grant.Version != 1 || !validSIEMUUID(grant.TenantID) || err != nil || endpoint.Port() == 0 ||
		addr.Zone() != "" || (!addr.IsPrivate() && !addr.IsLoopback()) ||
		!validTLSName(grant.ServerName) || !pinOK || grant.ServerCertificateSHA256 == zeroHash {
		return grant, nil, invalid
	}
	for _, file := range []string{grant.CAFile, grant.ClientCertificateFile, grant.ClientKeyFile, grant.CursorDirectory} {
		if !filepath.IsAbs(file) || filepath.Clean(file) != file {
			return grant, nil, invalid
		}
	}
	grant.Endpoint = netip.AddrPortFrom(addr, endpoint.Port()).String()

	rootsPEM, rootError := readSIEMFile(grant.CAFile, false, maxSIEMMaterial)
	certPEM, certError := readSIEMFile(grant.ClientCertificateFile, false, maxSIEMMaterial)
	keyPEM, keyError := readSIEMFile(grant.ClientKeyFile, true, maxSIEMMaterial)
	if rootError != nil || certError != nil || keyError != nil {
		return grant, nil, errors.New("AUDIT_SIEM_TLS_MATERIAL_UNAVAILABLE")
	}
	defer clear(keyPEM)
	roots := x509.NewCertPool()
	client, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil || !roots.AppendCertsFromPEM(rootsPEM) {
		return grant, nil, errors.New("AUDIT_SIEM_TLS_MATERIAL_INVALID")
	}
	return grant, &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: grant.ServerName,
		Certificates: []tls.Certificate{client},
		VerifyConnection: func(state tls.ConnectionState) error {
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			if hex.EncodeToString(sum[:]) != grant.ServerCertificateSHA256 {
				return errSIEMPinMismatch
			}
			return nil
		},
	}, nil
}

func openSIEMFile(path string, private bool) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 ||
		(private && !protectedSIEMFile(info)) {
		file.Close()
		return nil, errors.New("AUDIT_SIEM_FILE_UNPROTECTED")
	}
	return file, nil
}

func protectedSIEMFile(info os.FileInfo) bool {
	owner, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(owner.Uid) == os.Geteuid() && info.Mode().Perm()&0077 == 0
}

func readSIEMFile(path string, private bool, limit int64) ([]byte, error) {
	file, err := openSIEMFile(path, private)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readSIEMMaterial(file, limit)
}

func readSIEMMaterial(input io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > limit {
		return nil, errors.New("AUDIT_SIEM_FILE_INVALID")
	}
	return raw, nil
}

func exactSIEMJSON(raw []byte, output any, keys []string) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	fields := make(map[string]json.RawMessage, len(keys))
	for decoder.More() {
		token, err := decoder.Token()
		field, ok := token.(string)
		if err != nil || !ok || fields[field] != nil || len(fields) == len(keys) {
			return false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return false
		}
		fields[field] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return false
	}
	for _, key := range keys {
		if fields[key] == nil {
			return false
		}
	}
	return json.Unmarshal(raw, output) == nil
}

type siemSender struct {
	address  string
	config   *tls.Config
	timeout  time.Duration
	attempts int
	conn     *tls.Conn
}

func (s *siemSender) close() {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
}

func (s *siemSender) send(ctx context.Context, frame []byte) error {
	for attempt := 0; attempt < s.attempts; attempt++ {
		call, cancel := context.WithTimeout(ctx, s.timeout)
		err := s.write(call, frame)
		cancel()
		if err == nil {
			return nil
		}
		s.close()
		if ctx.Err() != nil {
			return errors.New("AUDIT_SIEM_INTERRUPTED")
		}
		var certificateError *tls.CertificateVerificationError
		if errors.Is(err, errSIEMPinMismatch) || errors.As(err, &certificateError) {
			return errors.New("AUDIT_SIEM_TLS_PEER_REJECTED")
		}
		if attempt+1 < s.attempts {
			timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return errors.New("AUDIT_SIEM_INTERRUPTED")
			}
		}
	}
	return errors.New("AUDIT_SIEM_TLS_WRITE_FAILED")
}

func (s *siemSender) write(ctx context.Context, frame []byte) error {
	if s.conn == nil {
		dialer := tls.Dialer{Config: s.config}
		conn, err := dialer.DialContext(ctx, "tcp", s.address)
		if err != nil {
			return err
		}
		s.conn = conn.(*tls.Conn)
	}
	conn := s.conn
	deadline, _ := ctx.Deadline()
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetWriteDeadline(time.Now()) })
	defer stop()
	n, err := conn.Write(frame)
	if err == nil && n != len(frame) {
		return io.ErrShortWrite
	}
	return err
}
