package mysqlbackup

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// VerifyRestoredReceipt rechecks the original private records and actual current
// target/lock/committed audit. It proves no fresh current row/file image and does
// not repeat a native SQL/archive scan for each operator GRANT. Its caller first
// obtains VerifyRestoredContext and uses fresh verification at mutation boundaries.
func VerifyRestoredReceipt(ctx context.Context, o *mysqloperator.Context, fresh *mysqloperator.Fresh, c config.Config, accounts *identity.Service, path string, expected RestoredContext) error {
	if ctx == nil || o == nil || fresh == nil || accounts == nil || c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil || !expected.valid() {
		return errors.New("MYSQL_RESTORE_CONTEXT_INVALID")
	}
	p := *c.Database.MySQLBackup
	if e := o.VerifyConfiguration(ctx, c); e != nil {
		return e
	}
	if e := o.VerifyIdentity(ctx, accounts); e != nil {
		return e
	}
	var actual RestoredContext
	if _, e := readPrivateDocument(path, p.MaximumManifestBytes, &actual); e != nil {
		return e
	}
	if actual != expected {
		return errors.New("MYSQL_RESTORE_CONTEXT_CHANGED")
	}
	var i RestoreIntent
	raw, e := readPrivateDocument(path+".intent", p.MaximumManifestBytes, &i)
	if e != nil || mysqloperator.SHA256(raw) != expected.IntentSHA256 {
		return errors.New("MYSQL_RESTORE_ORIGINAL_RECORD_CHANGED")
	}
	if e = verifyFreshIntent(ctx, fresh, c, i); e != nil {
		return e
	}
	var files RestoredFiles
	raw, e = readPrivateDocument(path+".files", p.MaximumManifestBytes, &files)
	if e != nil || mysqloperator.SHA256(raw) != expected.FilesSHA256 {
		return errors.New("MYSQL_RESTORE_ORIGINAL_RECORD_CHANGED")
	}
	if e = files.validate(i, expected.IntentSHA256, p); e != nil {
		return e
	}
	if contextFrom(i, expected.IntentSHA256, expected.FilesSHA256, expected.PostDataSHA256) != expected {
		return errors.New("MYSQL_RESTORE_CONTEXT_CHANGED")
	}
	post, e := readCommittedRestore(ctx, o, i, expected.IntentSHA256, expected.FilesSHA256, BackupManifest{}, p, false)
	if e != nil {
		return e
	}
	if post != expected.PostDataSHA256 {
		return errors.New("MYSQL_RESTORE_AUDIT_CONFLICT")
	}
	return nil
}
