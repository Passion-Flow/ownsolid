//go:build cgo && odbc_manager && dameng_recovery_integration

package damengbackup

import (
	"context"
	"flag"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	accountidentity "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng/migrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Test-only protected references. Enabling this tag never skips a missing input,
// starts a server, creates a DB/user, accepts terms or retries an uncertain effect.
var recoverySourceFile = flag.String("dameng-recovery-source", "", "protected source recovery configuration")
var recoverySourceContext = flag.String("dameng-recovery-source-context", "", "protected source native context")
var recoverySourceApplication = flag.String("dameng-recovery-source-application", "", "protected source platform application YAML")
var recoveryTargetFile = flag.String("dameng-recovery-target", "", "protected fresh target recovery configuration")
var recoveryTargetContext = flag.String("dameng-recovery-target-context", "", "protected fresh target native context")
var recoveryTargetApplication = flag.String("dameng-recovery-target-application", "", "protected fresh target platform application YAML")
var recoveryOutput = flag.String("dameng-recovery-output", "", "new private output root below an existing private parent")
var recoveryDisposableTarget = flag.Bool("dameng-recovery-disposable-target", false, "explicit synthetic fresh-target import/invalidation authorization")

type actualRecoveryTarget struct {
	recovery    Configuration
	context     NativeContextConfiguration
	connector   dameng.ConnectionConfiguration
	schema      dameng.SchemaConfiguration
	application config.Config
	kernel      *dameng.Store
	native      *Native
	accounts    *accountidentity.Service
}

func openActualRecoveryTarget(t *testing.T, ctx context.Context, recoveryFile, contextFile, applicationFile string) actualRecoveryTarget {
	t.Helper()
	var out actualRecoveryTarget
	var e error
	out.recovery, e = LoadConfiguration(recoveryFile, "")
	if e != nil {
		t.Fatal(e)
	}
	out.context, e = LoadNativeContext(contextFile, "")
	if e != nil {
		t.Fatal(e)
	}
	out.connector, e = dameng.LoadConnectionConfiguration(out.context.ConnectorConfigurationFile, "")
	if e != nil {
		t.Fatal(dameng.SafeError(e))
	}
	out.schema, e = dameng.LoadSchemaConfiguration(out.context.SchemaConfigurationFile, "")
	if e != nil {
		t.Fatal(dameng.SafeError(e))
	}
	raw, e := readPrivate(applicationFile, maximumDocumentBytes)
	if e != nil {
		t.Fatal(e)
	}
	out.application, e = config.Decode(raw)
	if e != nil || out.application.DatabaseEngine() != config.DamengDatabaseEngine {
		t.Fatal("protected complete Dameng platform configuration required")
	}

	opening, stopOpening := context.WithTimeout(ctx, time.Duration(out.recovery.Policy.OperationSeconds)*time.Second)
	defer stopOpening()
	out.kernel, _, e = dameng.Open(opening, out.connector)
	if out.kernel != nil {
		kernel := out.kernel
		cleanupSeconds := out.connector.CleanupSeconds
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), time.Duration(cleanupSeconds)*time.Second)
			defer stop()
			status, e := kernel.Close(cleanup)
			if e != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 || status.MemoryLeases != 0 || status.RetainedBytes != 0 {
				t.Error("native cleanup pending; retain original owner diagnostics", dameng.SafeError(e))
			}
		})
	}
	if e != nil {
		t.Fatal(dameng.SafeError(e))
	}
	out.native, e = NewNativeContext(opening, out.recovery, out.connector)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := transaction.FromDameng(out.kernel)
	if e != nil {
		t.Fatal(e)
	}
	out.accounts = &accountidentity.Service{Storage: repo, Config: out.application}
	return out
}
func (a actualRecoveryTarget) with(ctx context.Context, mode MaintenanceMode, operation func(context.Context, *NativeCustody) error) error {
	return WithNativeCustody(ctx, a.native, a.kernel, a.connector, a.schema, a.context.Snapshot, mode, operation)
}

// This deliberately requires a complete stopped synthetic platform source and
// independently provisioned empty native server. It cannot turn a partial table
// fixture into whole-platform readiness. Archives, stores and journals stay on
// the caller's explicitly admitted durable filesystem for independent readback.
func TestActualDamengNativeMatchedRestoreAndCommittedAuthority(t *testing.T) {
	if !*recoveryDisposableTarget {
		t.Fatal("explicit synthetic fresh-target authorization required")
	}
	if e := migrate.RequireControlServingCatalog(); e != nil {
		t.Fatal(e)
	}
	ctx := t.Context()
	source := openActualRecoveryTarget(t, ctx, *recoverySourceFile, *recoverySourceContext, *recoverySourceApplication)
	target := openActualRecoveryTarget(t, ctx, *recoveryTargetFile, *recoveryTargetContext, *recoveryTargetApplication)
	if e := validatePlatformRoots(source.application, *recoveryOutput); e != nil {
		t.Fatal(e)
	}
	if e := validatePlatformRoots(target.application, *recoveryOutput); e != nil {
		t.Fatal(e)
	}
	for _, a := range platformRoots(source.application) {
		for _, b := range platformRoots(target.application) {
			if related(a, b) {
				t.Fatal("source and fresh target stores overlap")
			}
		}
	}
	root, e := newRoot(*recoveryOutput)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	const reason = "Synthetic explicit native matched restore acceptance"
	backup, matched := filepath.Join(*recoveryOutput, "backup"), filepath.Join(*recoveryOutput, "matched")
	restored, files, authority := filepath.Join(*recoveryOutput, "restored"), filepath.Join(*recoveryOutput, "files"), filepath.Join(*recoveryOutput, "authority")
	restoreID := uuid.NewString()
	if e = writeNew(root, "integration.intent.json", jsonBytes(struct {
		FormatVersion                                        int
		RestoreID                                            string
		SourceConfigurationSHA256, TargetConfigurationSHA256 string
	}{1, restoreID, fingerprint(source.recovery), fingerprint(target.recovery)})); e != nil {
		t.Fatal(e)
	}
	e = source.with(ctx, SourceMaintenance, func(ctx context.Context, c *NativeCustody) error {
		for _, table := range []string{"MODEL_VERSIONS", "DATASET_ROWS", "TRAINING_CHECKPOINTS", "MODEL_PREPARATION_ARTIFACTS"} {
			var count int64
			if c.query.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+nativeQuote(table)).Scan(&count) != nil || count < 1 {
				return fail("POPULATED_FOUR_STORE_FIXTURE_REQUIRED")
			}
		}
		for _, audience := range []string{"admin", "portal"} {
			var count int64
			if c.query.QueryRowContext(ctx, `SELECT COUNT(*) FROM "SESSIONS" S JOIN "USERS" U ON U.ID=S.USER_ID WHERE S.AUDIENCE=? AND S.REVOKED_AT IS NULL AND S.ABSOLUTE_EXPIRES_AT>CURRENT_TIMESTAMP AND S.AUTH_EPOCH=U.AUTH_EPOCH AND U.STATUS='active'`, audience).Scan(&count) != nil || count < 1 {
				return fail("LIVE_PREBACKUP_SESSION_FIXTURE_REQUIRED")
			}
		}
		if _, e := source.native.Backup(ctx, backup, reason, c); e != nil {
			return e
		}
		_, e := CaptureMatchedStores(ctx, c, source.accounts, source.application, backup, matched)
		return e
	})
	if e != nil {
		t.Fatal("source capture failed; retained original scope", e)
	}
	e = target.with(ctx, FreshRestoreMaintenance, func(ctx context.Context, c *NativeCustody) error {
		if _, e := target.native.Restore(ctx, backup, restored, reason, c); e != nil {
			return e
		}
		if _, e := RestoreMatchedStores(ctx, c, target.accounts, target.application, restored, matched, files); e != nil {
			return e
		}
		_, e := PrepareRestoredAuthority(ctx, c, target.accounts, target.application, restored, files, authority, restoreID)
		return e
	})
	if e != nil {
		t.Fatal("target failed or unknown; never replay original import/invalidation", e)
	}
	var completed RestoredAuthority
	e = target.with(ctx, FreshRestoreMaintenance, func(ctx context.Context, c *NativeCustody) error {
		var e error
		completed, e = VerifyCommittedAuthority(ctx, c, target.accounts, target.application, authority)
		if e != nil {
			return e
		}
		var live int64
		if c.query.QueryRowContext(ctx, `SELECT COUNT(*) FROM "SESSIONS" WHERE "REVOKED_AT" IS NULL`).Scan(&live) != nil || live != 0 {
			return fail("RESTORED_SESSIONS_NOT_INVALIDATED")
		}
		return nil
	})
	if e != nil {
		t.Fatal("independent committed readback failed or unknown; target remains isolated", e)
	}
	if completed.RestoreID != restoreID || completed.Status != "authority_invalidated_audit_verified_runtime_access_pending" {
		t.Fatal("incorrect authority completion scope")
	}
	if e = writeNew(root, "integration.complete.json", jsonBytes(completed)); e != nil {
		t.Fatal(e)
	}
	t.Log("actual native database, matched four stores and committed invalidation verified; runtime access and secret/configuration closure remain separate")
}
