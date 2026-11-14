package databaseaccess

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqlbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const CheckRestoredMySQLCommand = "check-restored-runtime-access"
const ReconcileRestoredMySQLCommand = "reconcile-restored-runtime-access"
const ResumeRestoredMySQLCommand = "resume-restored-runtime-access"

type mysqlRestoreProof struct {
	sha    string
	verify func(bool) error
}

func MySQLRestoredCommand(command string) bool {
	return command == "prepare-restored-runtime-access" || command == CheckRestoredMySQLCommand || command == ReconcileRestoredMySQLCommand || command == ResumeRestoredMySQLCommand
}
func restoreRelated(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func newMySQLRestoreProof(ctx context.Context, o *mysqloperator.Context, fresh *mysqloperator.Fresh, c config.Config, accounts *identity.Service, i MySQLRuntimeInput, path string) (*mysqlRestoreProof, error) {
	if ctx == nil || o == nil || fresh == nil || accounts == nil || c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil {
		return nil, errors.New("MYSQL_RUNTIME_ACCESS_ACTUAL_RESTORE_REQUIRED")
	}
	if e := o.VerifyConfiguration(ctx, c); e != nil {
		return nil, e
	}
	if e := o.VerifyIdentity(ctx, accounts); e != nil {
		return nil, e
	}
	base, e := mysqlbackup.VerifyRestoredContext(ctx, fresh, c, accounts, path, false)
	if e != nil {
		return nil, e
	}
	original, e := mysqloperator.ReadPrivateBytes(path, c.Database.MySQLBackup.MaximumManifestBytes)
	if e != nil {
		return nil, e
	}
	var decoded mysqlbackup.RestoredContext
	if strict.DecodeStrict(original, &decoded) != nil || decoded != base {
		return nil, errors.New("MYSQL_RUNTIME_ACCESS_RESTORE_CONTEXT_CHANGED")
	}
	var intent mysqlbackup.RestoreIntent
	intentBytes, e := mysqloperator.ReadDocument(path+".intent", c.Database.MySQLBackup.MaximumManifestBytes, &intent)
	if e != nil || mysqloperator.SHA256(intentBytes) != base.IntentSHA256 {
		return nil, errors.New("MYSQL_RUNTIME_ACCESS_RESTORE_INTENT_CHANGED")
	}
	if i.configuration.Database.MySQLRuntimeAccess == nil {
		return nil, errors.New("MYSQL_RUNTIME_ACCESS_CONFIGURATION_REQUIRED")
	}
	destination := i.configuration.Database.MySQLRuntimeAccess.IntentFile
	for _, protected := range []string{intent.BackupRoot, c.Models.ArtifactRoot, c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot, path, path + ".intent", path + ".files", path + ".native.stderr"} {
		if restoreRelated(destination, protected) || restoreRelated(destination+".created", protected) {
			return nil, errors.New("MYSQL_RUNTIME_ACCESS_RESTORE_PATH_OVERLAP")
		}
	}
	proof := &mysqlRestoreProof{sha: mysqloperator.SHA256(original)}
	proof.verify = func(requireFresh bool) error {
		if e := o.VerifyConfiguration(ctx, c); e != nil {
			return e
		}
		actual := base
		if requireFresh {
			var e error
			actual, e = mysqlbackup.VerifyRestoredContext(ctx, fresh, c, accounts, path, true)
			if e != nil {
				return e
			}
		} else if e := mysqlbackup.VerifyRestoredReceipt(ctx, o, fresh, c, accounts, path, base); e != nil {
			return e
		}
		raw, e := mysqloperator.ReadPrivateBytes(path, c.Database.MySQLBackup.MaximumManifestBytes)
		if e != nil || !bytes.Equal(raw, original) || actual != base {
			return errors.New("MYSQL_RUNTIME_ACCESS_RESTORE_CONTEXT_CHANGED")
		}
		return nil
	}
	if e = proof.verify(false); e != nil {
		return nil, e
	}
	return proof, nil
}

// RestoredMySQLAccess owns the MySQL restored account variant. It accepts only
// the actual native restore owner/context; PostgreSQL attestations cannot enter.
func RestoredMySQLAccess(ctx context.Context, o *mysqloperator.Context, fresh *mysqloperator.Fresh, c config.Config, accounts *identity.Service, i MySQLRuntimeInput, path, command string) (string, error) {
	modes := map[string]string{"prepare-restored-runtime-access": "prepare", CheckRestoredMySQLCommand: "check", ReconcileRestoredMySQLCommand: "reconcile", ResumeRestoredMySQLCommand: "resume"}
	mode, ok := modes[command]
	if !ok {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_COMMAND_INVALID")
	}
	if e := i.verify(); e != nil {
		return "", e
	}
	proof, e := newMySQLRestoreProof(ctx, o, fresh, c, accounts, i, path)
	if e != nil {
		return "", e
	}
	return mysqlAccessWithProof(ctx, o, i, mode, proof)
}
