package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// certificateFile holds the node's private keys and certificate (ADR 0050).
const certificateFile = "certificate.json"

// Certificate is the node's certificate and what a rotation has reached.
type Certificate struct {
	// PrivateKey is the key of Certificate, in PEM. Until the first certificate arrives it
	// is the key that was asked about.
	PrivateKey string `json:"private_key"`
	// Certificate is the signed certificate in PEM, empty until the first one arrives.
	Certificate string `json:"certificate,omitempty"`
	// PendingKey is a new key whose certificate has not arrived. It is saved before it is
	// asked for, so a lost reply is retried with the same key.
	PendingKey string `json:"pending_key,omitempty"`
	// Confirmed says control knows the old key is finished with.
	Confirmed bool `json:"confirmed"`
	// RenewAt is when this certificate is due for rotation.
	RenewAt time.Time `json:"renew_at,omitzero"`
}

// LoadCertificate returns the node's certificate state. A node enrolled before certificates
// has none, which is reported as false.
func LoadCertificate(dir string) (Certificate, bool, error) {
	if e := Directory(dir); e != nil {
		return Certificate{}, false, e
	}
	path := filepath.Join(dir, certificateFile)
	if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
		return Certificate{}, false, nil
	}
	b, e := ProtectedFile(path)
	if e != nil {
		return Certificate{}, false, errors.New("CERTIFICATE_STATE_INVALID")
	}
	var value Certificate
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF || value.PrivateKey == "" {
		return Certificate{}, false, errors.New("CERTIFICATE_STATE_INVALID")
	}
	return value, true, nil
}

// SaveCertificate durably replaces the certificate state.
func SaveCertificate(dir string, value Certificate) error {
	if e := Directory(dir); e != nil {
		return e
	}
	bad := errors.New("CERTIFICATE_STATE_WRITE_FAILED")
	if value.PrivateKey == "" {
		return errors.New("CERTIFICATE_STATE_INVALID")
	}
	b, e := json.Marshal(value)
	if e != nil {
		return bad
	}
	temp, e := os.CreateTemp(dir, ".certificate-*")
	if e != nil {
		return bad
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, e = temp.Write(b); e == nil {
		e = temp.Sync()
	}
	closeError := temp.Close()
	if e != nil || closeError != nil {
		return bad
	}
	if e = os.Rename(name, filepath.Join(dir, certificateFile)); e != nil {
		return bad
	}
	d, e := os.Open(dir)
	if e != nil {
		return bad
	}
	defer d.Close()
	if d.Sync() != nil {
		return bad
	}
	return nil
}
