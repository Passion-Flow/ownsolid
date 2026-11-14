//go:build cgo && odbc_manager

package databaseaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng/migrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type DamengContext struct {
	kernel        *dameng.Store
	store         transaction.Store
	configuration config.Config
	schema        dameng.SchemaConfiguration
	verify        func(context.Context) error
	target        string
	active        atomic.Bool
}

func (*DamengContext) String() string   { return "DAMENG_OPERATOR_REDACTED" }
func (*DamengContext) GoString() string { return "DAMENG_OPERATOR_REDACTED" }
func nativeDigest(b []byte) string      { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// WithDameng retains the native installation lock, exact Store provenance and
// restricted runtime check throughout a bounded callback. It does not enroll
// Dameng into the control server selector or create/login a database account.
func WithDameng(ctx context.Context, owner, runtime *dameng.Store, schema dameng.SchemaConfiguration, c config.Config, operation func(context.Context, *DamengContext) error) (migrate.Report, error) {
	if operation == nil || c.Policy().Validate() != nil {
		return migrate.Report{}, errors.New("DAMENG_OPERATOR_CONFIGURATION_REQUIRED")
	}
	store, e := transaction.FromDameng(runtime)
	if e != nil {
		return migrate.Report{}, e
	}
	return migrate.WithReadyRuntime(ctx, owner, runtime, schema, func(ctx context.Context, verify func(context.Context) error) error {
		o := &DamengContext{kernel: runtime, store: store, configuration: c, schema: schema, verify: verify}
		o.active.Store(true)
		defer o.active.Store(false)
		o.target, e = o.readTarget(ctx)
		if e != nil {
			return e
		}
		return operation(ctx, o)
	})
}
func (o *DamengContext) readTarget(ctx context.Context) (string, error) {
	var schema, principal, deployment string
	var objectID int64
	e := o.store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT SYS_CONTEXT('USERENV','CURRENT_SCHEMA'),SYS_CONTEXT('USERENV','SESSION_USER'),l."DEPLOYMENT_ID",a.OBJECT_ID FROM "PLATFORM_LICENSE_STATE" l JOIN ALL_OBJECTS a ON a.OWNER=? AND a.OBJECT_NAME='INSTALLATION_STATE' AND a.OBJECT_TYPE='TABLE' WHERE l."ID"=1`, o.schema.Schema)).Scan(&schema, &principal, &deployment, &objectID)
	id, parse := uuid.Parse(deployment)
	if e != nil || parse != nil || id == uuid.Nil || id.String() != deployment || schema != o.schema.Schema || principal != o.schema.RuntimeUser || objectID <= 0 {
		return "", errors.New("DAMENG_OPERATOR_TARGET_UNAVAILABLE")
	}
	raw, e := json.Marshal(struct {
		Format, Schema, Principal, Deployment string
		ObjectID                              int64
		Configuration                         config.Config
	}{"projectone.dameng.operator-target.v1", schema, principal, deployment, objectID, o.configuration})
	if e != nil {
		return "", errors.New("DAMENG_OPERATOR_CONFIGURATION_INVALID")
	}
	return nativeDigest(raw), nil
}
func (o *DamengContext) Verify(ctx context.Context) error {
	if o == nil || !o.active.Load() || ctx == nil || o.verify == nil {
		return errors.New("DAMENG_OPERATOR_CONTEXT_EXPIRED")
	}
	if e := o.verify(ctx); e != nil {
		return e
	}
	actual, e := o.readTarget(ctx)
	if e != nil || actual != o.target {
		return errors.New("DAMENG_OPERATOR_TARGET_MISMATCH")
	}
	return nil
}
func (o *DamengContext) Binding(ctx context.Context) (string, error) {
	if e := o.Verify(ctx); e != nil {
		return "", e
	}
	return o.target, nil
}
func (o *DamengContext) Store(ctx context.Context) (transaction.Store, error) {
	if e := o.Verify(ctx); e != nil {
		return nil, e
	}
	return o.store, nil
}
func (o *DamengContext) VerifyConfiguration(ctx context.Context, c config.Config) error {
	if e := o.Verify(ctx); e != nil {
		return e
	}
	actual, e := json.Marshal(c)
	expected, ee := json.Marshal(o.configuration)
	if e != nil || ee != nil || nativeDigest(actual) != nativeDigest(expected) {
		return errors.New("DAMENG_OPERATOR_CONFIGURATION_MISMATCH")
	}
	return nil
}
func (o *DamengContext) VerifyIdentity(ctx context.Context, s *identity.Service) error {
	if s == nil || s.DB != nil {
		return errors.New("DAMENG_OPERATOR_IDENTITY_MISMATCH")
	}
	if e := o.VerifyConfiguration(ctx, s.Config); e != nil {
		return e
	}
	store, e := s.Repository()
	if e != nil || !transaction.BoundToDameng(store, o.kernel) {
		return errors.New("DAMENG_OPERATOR_IDENTITY_MISMATCH")
	}
	return nil
}
func (o *DamengContext) VerifyInstallation(ctx context.Context) error { return o.Verify(ctx) }
