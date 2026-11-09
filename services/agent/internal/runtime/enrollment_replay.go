package runtime

import (
	"context"
	"crypto/x509"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
)

// EnrollIfNeeded enrolls a read-only node or recognizes its matching local identity.
// A completed enrollment never consumes another token or writes to identity state.
func EnrollIfNeeded(ctx context.Context, c Config) error {
	if profile(c) != wire.ReadOnlyProfile {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	if err := state.Directory(c.StateDirectory); err != nil {
		return err
	}
	if state.Exists(c.StateDirectory) {
		return verifyExistingEnrollment(c)
	}
	err := Enroll(ctx, c)
	if err != nil && (err.Error() == "IDENTITY_ALREADY_EXISTS" || err.Error() == "AGENT_ALREADY_RUNNING") && state.Exists(c.StateDirectory) {
		// An enrollment or run process acquired the lock after the first check.
		return verifyExistingEnrollment(c)
	}
	return err
}

func loadBoundIdentity(c Config) (state.Identity, error) {
	identity, err := state.Load(c.StateDirectory)
	if err != nil {
		return state.Identity{}, err
	}
	if identity.ControlOrigin != c.ControlOrigin || identity.ProtocolVersion != wire.ProfileVersion(profile(c)) ||
		(identity.ExecutionProfile != "" && identity.ExecutionProfile != profile(c)) || !idPattern.MatchString(identity.NodeID) || !token(identity.Credential) {
		return state.Identity{}, errors.New("IDENTITY_INVALID")
	}
	return identity, nil
}

func verifyExistingEnrollment(c Config) error {
	identity, err := loadBoundIdentity(c)
	if err != nil {
		return err
	}
	certificate, found, err := state.LoadCertificate(c.StateDirectory)
	if err != nil {
		return err
	}
	if found {
		if certificate.Certificate == "" {
			if _, err := wire.NewCertificateRequest(certificate.PrivateKey); err != nil {
				return errors.New("CERTIFICATE_STATE_INVALID")
			}
		} else {
			if _, err := checkEnrollmentCertificate(certificate.Certificate, certificate.PrivateKey, identity.NodeID, identity.Scope()); err != nil {
				return errors.New("CERTIFICATE_STATE_INVALID")
			}
		}
	}
	connection, err := client.New(c.ControlOrigin, c.CAFile, c.Policy())
	if err != nil {
		return err
	}
	connection.Close()
	return nil
}

func checkEnrollmentCertificate(certificate, key, node string, scope wire.Scope) (*x509.Certificate, error) {
	leaf, err := wire.CheckIssuedCertificate(certificate, key, node)
	if err != nil {
		return nil, err
	}
	actualScope, _, err := wire.CertificateNode(leaf)
	if err != nil || actualScope != scope {
		return nil, errors.New("CERTIFICATE_RESPONSE_INVALID")
	}
	return leaf, nil
}
