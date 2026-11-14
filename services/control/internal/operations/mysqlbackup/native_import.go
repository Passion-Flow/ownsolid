package mysqlbackup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func importArguments(options, database string) []string {
	return []string{"--defaults-file=" + options, "--no-login-paths", "--binary-mode", "--local-infile=0", "--batch", "--skip-column-names", "--no-auto-rehash", "--skip-reconnect", "--database=" + database}
}
func importArgumentsValid(args []string, database string) bool {
	if len(args) != 9 || !identifier.MatchString(database) || !strings.HasPrefix(args[0], "--defaults-file=") {
		return false
	}
	path := strings.TrimPrefix(args[0], "--defaults-file=")
	if !canonicalPath(path) || filepath.Base(path) != "client.cnf" || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	expected := importArguments(path, database)
	for n := range expected {
		if expected[n] != args[n] {
			return false
		}
	}
	return true
}

// ImportMeasured never accepts arbitrary client statements or a partial target.
// The original SQL descriptor is hashed on both sides of actual joined execution.
func (n *Native) ImportMeasured(ctx context.Context, fresh *mysqloperator.Fresh, c config.Config, archive, expectedSHA string, diagnostic io.Writer) (NativeExecution, error) {
	if ctx == nil || fresh == nil || n == nil || c.Database.MySQLBackup == nil || n.policy != *c.Database.MySQLBackup || diagnostic == nil {
		return NativeExecution{}, errors.New("MYSQL_NATIVE_CONFIGURATION_REQUIRED")
	}
	if e := fresh.VerifyConfiguration(ctx, c); e != nil {
		return NativeExecution{}, e
	}
	settings, e := fresh.Settings(ctx)
	if e != nil {
		return NativeExecution{}, e
	}
	if settings != n.settings {
		return NativeExecution{}, errors.New("MYSQL_NATIVE_CONFIGURATION_REQUIRED")
	}
	if e = fresh.RequireEmpty(ctx); e != nil {
		return NativeExecution{}, e
	}
	lease, e := fresh.Lease(ctx)
	if e != nil {
		return NativeExecution{}, e
	}
	target, connection, e := connectionTarget(ctx, lease)
	if e != nil {
		return NativeExecution{}, e
	}
	var execution NativeExecution
	e = privateArchive(ctx, archive, n.policy.MaximumSQLBytes, expectedSHA, func(input *os.File) error {
		return n.withOptions(ctx, target, connection, func(password, ca string) error { return fresh.VerifyConnectorHashes(ctx, n.settings, password, ca) }, func(options string) error {
			if e := fresh.RequireEmpty(ctx); e != nil {
				return e
			}
			var runError error
			execution, runError = runNativeMeasured(ctx, n.policy.ClientFile, importArguments(options, target.Database), n.policy.ScratchRoot, time.Duration(n.policy.TerminationGraceSeconds)*time.Second, input, io.Discard, diagnostic)
			return runError
		})
	})
	if e != nil {
		return NativeExecution{}, e
	}
	return execution, nil
}
