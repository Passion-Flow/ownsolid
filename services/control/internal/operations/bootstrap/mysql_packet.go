package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
)

const PrepareMySQLPacketCommand = "prepare-mysql-bootstrap-packet"
const MySQLPacketCreated = "BOOTSTRAP_MYSQL_PRIVATE_PACKET_CREATED_NO_DATABASE_MUTATION"
const mysqlPacketFormat = "projectone.mysql.bootstrap-packet.v1"

// MySQLPacket is private secret material, separately versioned from the PG packet.
type MySQLPacket struct {
	Format               string `json:"format"`
	RuntimeBindingSHA256 string `json:"runtime_binding_sha256"`
	Packet               Packet `json:"packet"`
}

func (MySQLPacket) String() string   { return "MYSQL_BOOTSTRAP_PACKET_REDACTED" }
func (MySQLPacket) GoString() string { return "MYSQL_BOOTSTRAP_PACKET_REDACTED" }
func (p MySQLPacket) validate(binding string) error {
	decoded, e := hex.DecodeString(p.RuntimeBindingSHA256)
	if e != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != p.RuntimeBindingSHA256 || p.Format != mysqlPacketFormat || p.RuntimeBindingSHA256 != binding || p.Packet.validate() != nil {
		return errors.New("BOOTSTRAP_MYSQL_PACKET_INVALID")
	}
	return nil
}
func mysqlPacketBinding(ctx context.Context, s *identity.Service, o *mysqloperator.Context, runtime databaseaccess.MySQLRuntimeInput) (string, error) {
	if s == nil || s.Passwords == nil {
		return "", errors.New("BOOTSTRAP_CONTEXT_INVALID")
	}
	if e := o.VerifyIdentity(ctx, s); e != nil {
		return "", e
	}
	binding, e := databaseaccess.MySQLRuntimeBinding(ctx, o, runtime)
	if e != nil {
		return "", e
	}
	if e = o.VerifyIdentity(ctx, s); e != nil {
		return "", e
	}
	return binding, nil
}

func PrepareMySQLPacket(ctx context.Context, s *identity.Service, o *mysqloperator.Context, input, output string, runtime databaseaccess.MySQLRuntimeInput) error {
	binding, e := mysqlPacketBinding(ctx, s, o, runtime)
	if e != nil {
		return e
	}
	done, e := mysqlBootstrapState(ctx, s)
	if e != nil {
		return e
	}
	if done {
		return errors.New("BOOTSTRAP_ALREADY_COMPLETE")
	}
	var r Request
	raw, e := mysqloperator.ReadDocument(input, mysqloperator.MaximumIntentBytes, &r)
	if e != nil {
		return e
	}
	r, e = normalize(r)
	if e != nil {
		return e
	}
	canonical, e := json.Marshal(r)
	if e != nil {
		return errors.New("BOOTSTRAP_PACKET_INVALID")
	}
	secret := make([]byte, passwordBytes)
	if _, e = rand.Read(secret); e != nil {
		return errors.New("BOOTSTRAP_RANDOM_UNAVAILABLE")
	}
	packet := MySQLPacket{mysqlPacketFormat, binding, Packet{formatVersion, packetPurpose, r, digest(canonical), base64.RawURLEncoding.EncodeToString(secret)}}
	if _, e = s.Passwords.Policy(packet.Packet.Password, r.AdministratorEmail, r.InstallationName); e != nil {
		return errors.New("BOOTSTRAP_PASSWORD_POLICY_REJECTED")
	}
	if e = packet.validate(binding); e != nil {
		return e
	}
	var current Request
	actual, e := mysqloperator.ReadDocument(input, mysqloperator.MaximumIntentBytes, &current)
	if e != nil || !bytes.Equal(actual, raw) {
		return errors.New("BOOTSTRAP_REQUEST_CHANGED")
	}
	currentBinding, e := mysqlPacketBinding(ctx, s, o, runtime)
	if e != nil {
		return e
	}
	if currentBinding != binding {
		return errors.New("BOOTSTRAP_MYSQL_PACKET_BINDING_CHANGED")
	}
	encoded, e := json.Marshal(packet)
	if e != nil {
		return errors.New("BOOTSTRAP_PACKET_INVALID")
	}
	encoded = append(encoded, '\n')
	var retained MySQLPacket
	if e = mysqloperator.WriteDocument(output, encoded, &retained); e != nil {
		return e
	}
	actual, e = mysqloperator.ReadDocument(input, mysqloperator.MaximumIntentBytes, &current)
	if e != nil || !bytes.Equal(actual, raw) {
		return errors.New("BOOTSTRAP_REQUEST_CHANGED")
	}
	currentBinding, e = mysqlPacketBinding(ctx, s, o, runtime)
	if e != nil {
		return e
	}
	if currentBinding != binding {
		return errors.New("BOOTSTRAP_MYSQL_PACKET_BINDING_CHANGED")
	}
	actual, e = mysqloperator.ReadDocument(output, mysqloperator.MaximumIntentBytes, &retained)
	if e != nil || !bytes.Equal(actual, encoded) {
		return errors.New("BOOTSTRAP_PACKET_CHANGED")
	}
	return retained.validate(binding)
}

func RunMySQLPacket(ctx context.Context, s *identity.Service, o *mysqloperator.Context, command, path string, runtime databaseaccess.MySQLRuntimeInput) (string, error) {
	if !PacketCommand(command) {
		return "", errors.New("BOOTSTRAP_COMMAND_INVALID")
	}
	binding, e := mysqlPacketBinding(ctx, s, o, runtime)
	if e != nil {
		return "", e
	}
	var packet MySQLPacket
	raw, e := mysqloperator.ReadDocument(path, mysqloperator.MaximumIntentBytes, &packet)
	if e != nil {
		return "", e
	}
	if e = packet.validate(binding); e != nil {
		return "", e
	}
	r := packet.Packet.Request
	if _, e = s.Passwords.Policy(packet.Packet.Password, r.AdministratorEmail, r.InstallationName); e != nil {
		return "", errors.New("BOOTSTRAP_PASSWORD_POLICY_REJECTED")
	}
	unchanged := func() error {
		var retained MySQLPacket
		actual, e := mysqloperator.ReadDocument(path, mysqloperator.MaximumIntentBytes, &retained)
		if e != nil || !bytes.Equal(actual, raw) {
			return errors.New("BOOTSTRAP_PACKET_CHANGED")
		}
		current, e := mysqlPacketBinding(ctx, s, o, runtime)
		if e != nil {
			return e
		}
		if current != binding {
			return errors.New("BOOTSTRAP_MYSQL_PACKET_BINDING_CHANGED")
		}
		return nil
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	intent := MySQLIntent{Format: mysqlIntentFormat, Request: r, RequestSHA256: packet.Packet.RequestSHA256, TargetSHA256: binding, Password: packet.Packet.Password}
	status, e := observeMySQL(ctx, s, intent)
	if e != nil {
		return "", e
	}
	if command == CheckPacketCommand || status == Verified {
		if e = unchanged(); e != nil {
			return "", e
		}
		return status, nil
	}
	// observeMySQL validated the exact empty initial graph. No partial graph or
	// current credential is ever overwritten after an uncertain prior process.
	if ctx.Err() != nil {
		return "", errors.New("BOOTSTRAP_INTERRUPTED_RECONCILE_REQUIRED")
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	if e = s.Bootstrap(ctx, r.AdministratorEmail, r.DisplayName, packet.Packet.Password, r.InstallationName, r.WorkspaceName); e != nil {
		return "", errors.New("BOOTSTRAP_MUTATION_UNCERTAIN_RECONCILE_REQUIRED")
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	status, e = observeMySQL(ctx, s, intent)
	if e != nil {
		return "", e
	}
	if status != Verified {
		return "", errors.New("BOOTSTRAP_RESULT_UNCERTAIN")
	}
	return status, nil
}
