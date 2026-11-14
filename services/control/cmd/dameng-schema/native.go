//go:build cgo && odbc_manager

package main

import (
	"context"
	"regexp"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/bootstrap"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng/migrate"
)

func operateNative(mode string, c dameng.ConnectionConfiguration, sc dameng.SchemaConfiguration, rc *dameng.ConnectionConfiguration, options nativeOperatorOptions) (r result) {
	r = emptyResult()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(sc.TotalSeconds)*time.Second)
	defer cancel()
	// Store is retained even after failed Open. Close cannot convert pending
	// native ownership into successful schema completion.
	closeStore := func(store *dameng.Store, seconds int, runtime bool) {
		cleanup, stop := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
		defer stop()
		status, e := store.Close(cleanup)
		if runtime {
			r.RuntimeCleanupState = status.State
		} else {
			r.CleanupState = status.State
		}
		if e != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 {
			r.Success = false
			r.Report.FoundationReady = false
			r.Report.RuntimeReady = false
			r.Code = "DAMENG_SCHEMA_CLEANUP_PENDING"
		}
	}
	owner, observation, e := dameng.Open(ctx, c)
	if owner != nil {
		defer closeStore(owner, c.CleanupSeconds, false)
	}
	if e != nil {
		r.Code = dameng.FailureCode(e)
		return r
	}
	r.Observation = observation
	switch mode {
	case "grant", "bootstrap-admin-file", "reconcile-bootstrap-file", "resume-bootstrap-file", "check-license":
		if rc == nil {
			r.Code = "DAMENG_SCHEMA_ARGUMENTS_INVALID"
			return r
		}
		var runtime *dameng.Store
		runtime, _, e = dameng.Open(ctx, *rc)
		if runtime != nil {
			defer closeStore(runtime, rc.CleanupSeconds, true)
		}
		if e != nil {
			r.Code = dameng.FailureCode(e)
			return r
		}
		if mode == "grant" {
			r.Report, e = migrate.GrantRuntime(ctx, owner, runtime, sc)
		} else {
			policy, err := bootstrap.LoadDamengPolicy(options.Policy, options.Environment)
			if err != nil {
				r.Code = "DAMENG_OPERATOR_POLICY_INVALID"
				return r
			}
			configuration, err := policy.Configuration()
			if err != nil {
				r.Code = "DAMENG_OPERATOR_POLICY_INVALID"
				return r
			}
			r.Report, e = databaseaccess.WithDameng(ctx, owner, runtime, sc, configuration, func(ctx context.Context, operator *databaseaccess.DamengContext) error {
				store, err := operator.Store(ctx)
				if err != nil {
					return err
				}
				service := &identity.Service{Storage: store, Config: configuration}
				if mode == "check-license" {
					license, err := licensing.FromConfig(service)
					if err != nil {
						return err
					}
					err = license.Check(ctx)
					if err == nil {
						r.OperatorStatus = "LICENSE_NEW_BUSINESS_ALLOWED"
					}
					return err
				}
				service.Passwords, err = security.NewPasswords(configuration.Security.PasswordBlocklistFile, configuration.Policy().Identity)
				if err != nil {
					return err
				}
				r.OperatorStatus, err = bootstrap.RunDameng(ctx, service, operator, mode, options.Input, options.Output)
				return err
			})
		}
	case "check-runtime":
		r.Report, e = migrate.CheckRuntime(ctx, owner, sc)
	default:
		r.Report, e = migrate.Run(ctx, owner, sc, migrate.Mode(mode))
	}
	r.Code = migrate.FailureCode(e)
	if operatorMode(mode) && e != nil && regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,95}$`).MatchString(e.Error()) {
		r.Code = e.Error()
	}
	r.Success = e == nil
	if e != nil {
		r.Report.FoundationReady = false
		r.Report.RuntimeReady = false
	}
	return r
}
