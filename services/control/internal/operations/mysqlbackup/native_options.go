package mysqlbackup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

type ConnectionTarget struct {
	ServerUUID string `json:"server_uuid"`
	Database   string `json:"database"`
	Principal  string `json:"principal"`
	Version    string `json:"version"`
}

var principalPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}@[A-Za-z0-9_.:%-]{1,255}$`)
var versionPattern = regexp.MustCompile(`^8\.4\.(0|[1-9][0-9]*)$`)

func (t ConnectionTarget) valid() bool {
	id, e := uuid.Parse(t.ServerUUID)
	return e == nil && id != uuid.Nil && id.String() == t.ServerUUID && identifier.MatchString(t.Database) && principalPattern.MatchString(t.Principal) && versionPattern.MatchString(t.Version)
}
func connectionTarget(ctx context.Context, c *sql.Conn) (ConnectionTarget, uint64, error) {
	if c == nil {
		return ConnectionTarget{}, 0, errors.New("MYSQL_NATIVE_LOCK_REQUIRED")
	}
	var t ConnectionTarget
	var id uint64
	if c.QueryRowContext(ctx, "SELECT @@server_uuid,DATABASE(),CURRENT_USER(),VERSION(),CONNECTION_ID()").Scan(&t.ServerUUID, &t.Database, &t.Principal, &t.Version, &id) != nil || !t.valid() || id == 0 {
		return ConnectionTarget{}, 0, errors.New("MYSQL_NATIVE_TARGET_INVALID")
	}
	return t, id, nil
}
func initCommand(t ConnectionTarget, connection uint64) (string, error) {
	if !t.valid() || connection == 0 {
		return "", errors.New("MYSQL_NATIVE_TARGET_INVALID")
	}
	lock, e := mysqlmigrate.LockName(t.Database)
	if e != nil {
		return "", e
	}
	// Every interpolated target has a strict protocol alphabet. An invalid JSON
	// branch raises a real server error before this native connection can dump or
	// import on a foreign endpoint/instance/principal/installation lock.
	return fmt.Sprintf("DO JSON_EXTRACT(IF(@@server_uuid='%s' AND DATABASE()='%s' AND CURRENT_USER()='%s' AND VERSION()='%s' AND IS_USED_LOCK('%s')=%d,'{}','MYSQL_NATIVE_TARGET_MISMATCH'),'$')", t.ServerUUID, t.Database, t.Principal, t.Version, lock, connection), nil
}
func optionQuote(value string) (string, error) {
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("MYSQL_NATIVE_OPTION_INVALID")
	}
	value = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\t", "\\t", "\b", "\\b").Replace(value)
	return "\"" + value + "\"", nil
}
func optionDocument(settings mysqlstore.Configuration, password, ca, initial string) ([]byte, error) {
	if settings.Validate() != nil || settings.Host != settings.ServerName || password == "" || initial == "" {
		return nil, errors.New("MYSQL_NATIVE_OPTION_INVALID")
	}
	// mysqldump initially connects with no database. Its supported additional
	// initialization command runs separately after selecting this exact database;
	// do not assume DATABASE() is set or enable arbitrary multi-statements.
	values := []struct{ key, value string }{{"host", settings.Host}, {"user", settings.User}, {"password", password}, {"ssl-ca", ca}, {"init-command", "USE `" + settings.Database + "`"}, {"init-command-add", initial}}
	var out strings.Builder
	out.WriteString("[client]\nprotocol=TCP\nssl-mode=VERIFY_IDENTITY\ntls-version=TLSv1.2,TLSv1.3\ndefault-character-set=utf8mb4\n")
	for _, item := range values {
		quoted, e := optionQuote(item.value)
		if e != nil {
			return nil, e
		}
		out.WriteString(item.key + "=" + quoted + "\n")
	}
	out.WriteString("port=" + strconv.Itoa(settings.Port) + "\nmax-allowed-packet=" + strconv.Itoa(settings.Policy.MaximumPacketBytes) + "\n")
	return []byte(out.String()), nil
}

func (n *Native) withOptions(ctx context.Context, target ConnectionTarget, connection uint64, verify func(string, string) error, operation func(string) error) (result error) {
	if verify == nil || operation == nil {
		return errors.New("MYSQL_NATIVE_CONTEXT_REQUIRED")
	}
	if e := n.verify(ctx); e != nil {
		return e
	}
	password, e := mysqloperator.ReadPrivateBytes(n.settings.PasswordFile, mysqlstore.MaximumPasswordBytes)
	if e != nil {
		return e
	}
	ca, e := mysqloperator.ReadPrivateBytes(n.settings.CAFile, mysqlstore.MaximumCABytes)
	if e != nil {
		return e
	}
	check := func() error {
		if e := n.verify(ctx); e != nil {
			return e
		}
		return verify(mysqloperator.SHA256(password), mysqloperator.SHA256(ca))
	}
	if e = check(); e != nil {
		return e
	}
	if e = requireSpace(n.policy.ScratchRoot, int64(len(password)+len(ca))+n.policy.MaximumManifestBytes, n.policy.MinimumFreeBytes); e != nil {
		return e
	}
	parent, e := openPrivateRoot(n.policy.ScratchRoot)
	if e != nil {
		return e
	}
	defer parent.Close()
	name := "mysql-native-" + uuid.NewString()
	if parent.Mkdir(name, 0700) != nil {
		return errors.New("MYSQL_NATIVE_SCRATCH_CREATE_FAILED")
	}
	if e = syncRoot(parent); e != nil {
		return e
	}
	path := filepath.Join(n.policy.ScratchRoot, name)
	root, e := openPrivateRoot(path)
	if e != nil {
		return e
	}
	// runNative always joins before this cleanup can run. Delete only the two
	// exact files inventoried here, then the empty owned directory; never prune.
	defer func() {
		for _, file := range []string{"client.cnf", "ca.pem"} {
			if e := root.Remove(file); e != nil && !os.IsNotExist(e) {
				result = errors.Join(result, errors.New("MYSQL_NATIVE_SCRATCH_CLEANUP_FAILED"))
			}
		}
		if syncRoot(root) != nil {
			result = errors.Join(result, errors.New("MYSQL_NATIVE_SCRATCH_CLEANUP_FAILED"))
		}
		if root.Close() != nil {
			result = errors.Join(result, errors.New("MYSQL_NATIVE_SCRATCH_CLEANUP_FAILED"))
		}
		if parent.Remove(name) != nil {
			result = errors.Join(result, errors.New("MYSQL_NATIVE_SCRATCH_CLEANUP_FAILED"))
		}
		if syncRoot(parent) != nil {
			result = errors.Join(result, errors.New("MYSQL_NATIVE_SCRATCH_CLEANUP_FAILED"))
		}
	}()
	if e = writeNew(root, "ca.pem", ca); e != nil {
		return e
	}
	initial, e := initCommand(target, connection)
	if e != nil {
		return e
	}
	options, e := optionDocument(n.settings, string(password), filepath.Join(path, "ca.pem"), initial)
	if e != nil {
		return e
	}
	if e = writeNew(root, "client.cnf", options); e != nil {
		return e
	}
	if e = check(); e != nil {
		return e
	}
	result = operation(filepath.Join(path, "client.cnf"))
	return errors.Join(result, check())
}

func (n *Native) Dump(ctx context.Context, operator *mysqloperator.Context, c config.Config, output, diagnostic io.Writer) error {
	_, e := n.DumpMeasured(ctx, operator, c, output, diagnostic)
	return e
}

func (n *Native) DumpMeasured(ctx context.Context, operator *mysqloperator.Context, c config.Config, output, diagnostic io.Writer) (NativeExecution, error) {
	if c.Database.MySQLBackup == nil || n == nil || n.policy != *c.Database.MySQLBackup || output == nil || diagnostic == nil {
		return NativeExecution{}, errors.New("MYSQL_NATIVE_CONFIGURATION_REQUIRED")
	}
	if e := operator.VerifyConfiguration(ctx, c); e != nil {
		return NativeExecution{}, e
	}
	if e := operator.VerifyInstallation(ctx); e != nil {
		return NativeExecution{}, e
	}
	lease, e := operator.Lease(ctx)
	if e != nil {
		return NativeExecution{}, e
	}
	target, connection, e := connectionTarget(ctx, lease)
	if e != nil {
		return NativeExecution{}, e
	}
	var triggers, foreign int
	if lease.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(DEFINER<>?),0) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE()", target.Principal).Scan(&triggers, &foreign) != nil || triggers < 1 || foreign != 0 {
		return NativeExecution{}, errors.New("MYSQL_NATIVE_TRIGGER_DEFINER_CONFLICT")
	}
	var observed NativeExecution
	e = n.withOptions(ctx, target, connection, func(password, ca string) error { return operator.VerifyConnectorHashes(ctx, n.settings, password, ca) }, func(options string) error {
		args := []string{"--defaults-file=" + options, "--no-login-paths", "--single-transaction", "--quick", "--triggers", "--hex-blob", "--default-character-set=utf8mb4", "--no-tablespaces", "--set-gtid-purged=OFF", "--skip-add-locks", "--skip-disable-keys", "--skip-network-timeout", "--max-allowed-packet=" + strconv.Itoa(n.settings.Policy.MaximumPacketBytes), "--skip-lock-tables", n.settings.Database}
		var runError error
		observed, runError = runNativeMeasured(ctx, n.policy.DumpFile, args, n.policy.ScratchRoot, time.Duration(n.policy.TerminationGraceSeconds)*time.Second, nil, output, diagnostic)
		return runError
	})
	if e != nil {
		return NativeExecution{}, e
	}
	return observed, nil
}
