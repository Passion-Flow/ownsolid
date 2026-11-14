package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
)

const PreparePacketCommand = "prepare-bootstrap-packet"
const ApplyPacketCommand = "apply-bootstrap-packet"
const CheckPacketCommand = "check-bootstrap-packet"
const PacketCreated = "BOOTSTRAP_PRIVATE_PACKET_CREATED_NO_DATABASE_EFFECT"
const packetPurpose = "initial-administrator"

// Packet is a credential-bearing private document, never a diagnostic result.
type Packet struct {
	FormatVersion int     `json:"format_version"`
	Purpose       string  `json:"purpose"`
	Request       Request `json:"request"`
	RequestSHA256 string  `json:"request_sha256"`
	Password      string  `json:"password"`
}

func PacketCommand(command string) bool {
	return command == ApplyPacketCommand || command == CheckPacketCommand
}

func (p Packet) validate() error {
	r, e := normalize(p.Request)
	raw, je := json.Marshal(r)
	secret, se := base64.RawURLEncoding.DecodeString(p.Password)
	if e != nil || je != nil || se != nil || p.FormatVersion != formatVersion || p.Purpose != packetPurpose || r != p.Request || digest(raw) != p.RequestSHA256 || len(secret) != passwordBytes || base64.RawURLEncoding.EncodeToString(secret) != p.Password {
		return errors.New("BOOTSTRAP_PACKET_INVALID")
	}
	return nil
}

// PreparePacket has no database/configuration/network dependency or effect.
func PreparePacket(input, output string) error {
	var request Request
	if _, e := readPrivate(input, &request); e != nil {
		return e
	}
	request, e := normalize(request)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(request)
	if e != nil {
		return errors.New("BOOTSTRAP_PACKET_INVALID")
	}
	secret := make([]byte, passwordBytes)
	if _, e = rand.Read(secret); e != nil {
		return errors.New("BOOTSTRAP_RANDOM_UNAVAILABLE")
	}
	packet := Packet{formatVersion, packetPurpose, request, digest(raw), base64.RawURLEncoding.EncodeToString(secret)}
	raw, e = json.Marshal(packet)
	if e != nil {
		return errors.New("BOOTSTRAP_PACKET_INVALID")
	}
	var check Packet
	if e = writeDocument(output, append(raw, '\n'), &check); e != nil {
		return e
	}
	return check.validate()
}

// RunPacket holds the caller's actual installation lease throughout. A matching
// repeated apply only observes existing committed state and never resets a user.
func RunPacket(ctx context.Context, s *identity.Service, lease *pgxpool.Conn, command, path string, runtime databaseaccess.RuntimeInput) (string, error) {
	if !PacketCommand(command) {
		return "", errors.New("BOOTSTRAP_COMMAND_INVALID")
	}
	binding, e := lockedTarget(ctx, s, lease)
	if e != nil {
		return "", e
	}
	var packet Packet
	raw, e := readPrivate(path, &packet)
	if e != nil {
		return "", e
	}
	if e = packet.validate(); e != nil {
		return "", e
	}
	if _, e = s.Passwords.Policy(packet.Password, packet.Request.AdministratorEmail, packet.Request.InstallationName); e != nil {
		return "", errors.New("BOOTSTRAP_PASSWORD_POLICY_REJECTED")
	}
	status, e := databaseaccess.Check(ctx, s.DB, lease, runtime)
	if e != nil {
		return "", e
	}
	if status != databaseaccess.Verified {
		return "", errors.New("BOOTSTRAP_RUNTIME_ACCESS_NOT_VERIFIED")
	}
	unchanged := func() error {
		var current Packet
		actual, e := readPrivate(path, &current)
		if e != nil || !bytes.Equal(raw, actual) {
			return errors.New("BOOTSTRAP_PACKET_CHANGED")
		}
		return nil
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	intent := Intent{FormatVersion: formatVersion, Request: packet.Request, RequestSHA256: packet.RequestSHA256, TargetSHA256: binding, Password: packet.Password}
	status, e = observe(ctx, s, intent)
	if e != nil {
		return "", e
	}
	if command == CheckPacketCommand || status == Verified {
		if e = unchanged(); e != nil {
			return "", e
		}
		return status, nil
	}
	var empty bool
	if e = lease.QueryRow(ctx, `SELECT NOT bootstrap_complete AND (SELECT count(*) FROM users)=0 AND (SELECT count(*) FROM tenants)=0 AND (SELECT count(*) FROM workspaces)=0 FROM installation_state WHERE id=1`).Scan(&empty); e != nil || !empty {
		return "", errors.New("BOOTSTRAP_INITIAL_INSTALLATION_REQUIRED")
	}
	if e = ctx.Err(); e != nil {
		return "", errors.New("BOOTSTRAP_INTERRUPTED_RECONCILE_REQUIRED")
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	r := packet.Request
	if e = s.Bootstrap(ctx, r.AdministratorEmail, r.DisplayName, packet.Password, r.InstallationName, r.WorkspaceName); e != nil {
		return "", errors.New("BOOTSTRAP_MUTATION_UNCERTAIN_RECONCILE_REQUIRED")
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	status, e = observe(ctx, s, intent)
	if e != nil {
		return "", e
	}
	if status != Verified {
		return "", errors.New("BOOTSTRAP_RESULT_UNCERTAIN")
	}
	return status, nil
}
