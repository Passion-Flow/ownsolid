// Package licensing owns durable whole-platform license history and admission.
package licensing

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

type Phase string

const (
	Missing      Phase = "missing"
	NotYetValid  Phase = "not_yet_valid"
	Active       Phase = "active"
	Grace        Phase = "grace"
	Expired      Phase = "expired"
	ClockSuspect Phase = "clock_suspect"
)

type Summary struct {
	LicenseID         string `json:"license_id"`
	ArtifactSHA256    string `json:"artifact_sha256"`
	CustomerReference string `json:"customer_reference"`
	NotBefore         string `json:"not_before"`
	ExpiresAt         string `json:"expires_at"`
	GraceUntil        string `json:"grace_until"`
}
type State struct {
	State                Phase     `json:"state"`
	NewBusinessAllowed   bool      `json:"new_business_allowed"`
	Warning              bool      `json:"warning"`
	DeploymentID         string    `json:"deployment_id"`
	Current              *Summary  `json:"current"`
	Pending              *Summary  `json:"pending"`
	AcceptedLicenses     int       `json:"accepted_licenses"`
	HistoryHeadLicenseID string    `json:"history_head_license_id"`
	ObservedAt           time.Time `json:"observed_at"`
	PublicKeyFingerprint string    `json:"public_key_fingerprint"`
}
type PortalState struct {
	State              Phase `json:"state"`
	NewBusinessAllowed bool  `json:"new_business_allowed"`
	Warning            bool  `json:"warning"`
}
type ImportInput struct {
	Artifact                 string `json:"artifact"`
	Reason                   string `json:"reason"`
	ExpectedCurrentLicenseID string `json:"expected_current_license_id"`
}
type ImportResult struct {
	State             State  `json:"state"`
	ImportedLicenseID string `json:"imported_license_id"`
	Repeated          bool   `json:"repeated"`
}

type Service struct {
	identity         *identity.Service
	trust            wire.Trust
	policy           wire.Policy
	now              func() time.Time
	fingerprint      string
	operationTimeout time.Duration
}

func fail(code string) error { return errors.New(code) }

// Clock belongs to the trusted component boundary, never an HTTP request.
// Production supplies the host wall clock; tests explicitly inject their clock.
func New(owner *identity.Service, trust wire.Trust, policy wire.Policy, clock func() time.Time) (*Service, error) {
	fingerprint, e := trust.Fingerprint()
	if owner == nil || policy.Validate() != nil || clock == nil || e != nil {
		return nil, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	if owner.Config.Policy().Validate() != nil {
		return nil, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	store, e := owner.Repository()
	if e != nil || store == nil || store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL && store.Dialect() != transaction.Dameng {
		return nil, fail("LICENSE_CONFIGURATION_REQUIRED")
	}
	return &Service{identity: owner, trust: trust, policy: policy, now: clock, fingerprint: fingerprint, operationTimeout: config.Seconds(owner.Config.Policy().HTTP.RequestSeconds)}, nil
}
func (s *Service) actor(ctx context.Context, tx transaction.Executor, a identity.Auth, admin bool) error {
	if a.Me.Audience != "portal" && a.Me.Audience != "admin" || admin && (a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return fail("ACCESS_DENIED")
	}
	return s.identity.RecheckOrderedWithin(ctx, tx, a)
}
func summary(v wire.Verified) *Summary {
	c := v.Claims
	return &Summary{c.LicenseID, v.SHA256, c.CustomerReference, c.NotBefore, c.ExpiresAt, c.GraceUntil}
}
func renewal(previous, next wire.Claims) bool {
	priorIssued, _ := wire.Stamp(previous.IssuedAt)
	issued, _ := wire.Stamp(next.IssuedAt)
	priorStart, _ := wire.Stamp(previous.NotBefore)
	start, _ := wire.Stamp(next.NotBefore)
	priorExpires, _ := wire.Stamp(previous.ExpiresAt)
	expires, _ := wire.Stamp(next.ExpiresAt)
	priorGrace, _ := wire.Stamp(previous.GraceUntil)
	return next.ReplacesLicenseID == previous.LicenseID && next.DeploymentID == previous.DeploymentID && !issued.Before(priorIssued) && !start.Before(priorStart) && !start.After(priorGrace) && expires.After(priorExpires)
}

// Every read validates actual signed history, not cached booleans or a client
// license/deployment override. The original singleton serializes all transitions.
func (s *Service) readLocked(ctx context.Context, tx transaction.Executor) (State, []wire.Verified, time.Time, error) {
	return s.read(ctx, tx, " FOR UPDATE")
}

// read is readLocked with the lock of the PostgreSQL statement chosen by the caller.
func (s *Service) read(ctx context.Context, tx transaction.Executor, lock string) (State, []wire.Verified, time.Time, error) {
	var out State
	var observed time.Time
	if e := tx.QueryRow(ctx, licenseSQL(tx, "SELECT deployment_id::text,observed_at FROM platform_license_state WHERE id=1"+lock, "SELECT deployment_id,observed_at FROM platform_license_state WHERE id=1 FOR UPDATE", `SELECT "DEPLOYMENT_ID","OBSERVED_AT" FROM "PLATFORM_LICENSE_STATE" WHERE "ID"=1 FOR UPDATE`)).Scan(&out.DeploymentID, &observed); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return State{}, nil, time.Time{}, fail("LICENSE_STORED_STATE_INVALID")
		}
		return State{}, nil, time.Time{}, repositoryError(e)
	}
	if tx.Dialect() == transaction.Dameng && (observed.Location() != time.UTC || observed.Nanosecond()%1000 != 0) {
		return State{}, nil, time.Time{}, fail("LICENSE_STORED_STATE_INVALID")
	}
	effective, clockSuspect, e := observeTime(s.now(), observed, s.policy.RollbackToleranceSeconds)
	if e != nil || !wire.ValidID(out.DeploymentID) {
		return State{}, nil, time.Time{}, fail("LICENSE_STORED_STATE_INVALID")
	}
	rows, e := tx.Query(ctx, licenseHistorySQL(tx))
	if e != nil {
		return State{}, nil, time.Time{}, repositoryError(e)
	}
	defer rows.Close()
	history := make([]wire.Verified, 0)
	for rows.Next() {
		var sequence int
		var id, deployment, sha string
		var predecessor sql.NullString
		var raw []byte
		if e := rows.Scan(&sequence, &id, &deployment, &sha, &raw, &predecessor); e != nil {
			return State{}, nil, time.Time{}, repositoryError(e)
		}
		if len(history) >= wire.MaximumRetainedImports {
			return State{}, nil, time.Time{}, fail("LICENSE_STORED_STATE_INVALID")
		}
		proof, e := wire.Verify(raw, s.trust)
		if e != nil || sequence != len(history)+1 || proof.Claims.LicenseID != id || proof.Claims.DeploymentID != deployment || deployment != out.DeploymentID || proof.SHA256 != sha || !bytes.Equal(proof.Artifact, raw) || proof.Claims.ReplacesLicenseID != predecessor.String || len(history) == 0 && predecessor.String != "" || len(history) > 0 && !renewal(history[len(history)-1].Claims, proof.Claims) {
			return State{}, nil, time.Time{}, fail("LICENSE_STORED_STATE_INVALID")
		}
		history = append(history, proof)
	}
	if e := rows.Err(); e != nil {
		return State{}, nil, time.Time{}, repositoryError(e)
	}
	rows.Close()
	out.State, out.ObservedAt, out.PublicKeyFingerprint = Missing, effective, s.fingerprint
	out.AcceptedLicenses = len(history)
	for _, proof := range history {
		out.HistoryHeadLicenseID = proof.Claims.LicenseID
		start, _ := wire.Stamp(proof.Claims.NotBefore)
		if start.After(effective) {
			if out.Pending != nil {
				return State{}, nil, time.Time{}, fail("LICENSE_STORED_STATE_INVALID")
			}
			out.Pending = summary(proof)
		} else {
			out.Current = summary(proof)
		}
	}
	if out.Current != nil {
		expires, _ := wire.Stamp(out.Current.ExpiresAt)
		grace, _ := wire.Stamp(out.Current.GraceUntil)
		switch {
		case effective.Before(expires):
			out.State = Active
		case effective.Before(grace):
			out.State = Grace
		default:
			out.State = Expired
		}
	} else if out.Pending != nil {
		out.State = NotYetValid
	}
	if clockSuspect {
		out.State = ClockSuspect
	}
	out.NewBusinessAllowed = out.State == Active || out.State == Grace
	out.Warning = out.State != Active
	if _, e = tx.Exec(ctx, licenseSQL(tx, "UPDATE platform_license_state SET observed_at=$1 WHERE id=1", "UPDATE platform_license_state SET observed_at=? WHERE id=1", `UPDATE "PLATFORM_LICENSE_STATE" SET "OBSERVED_AT"=? WHERE "ID"=1`, effective)); e != nil {
		return State{}, nil, time.Time{}, repositoryError(e)
	}
	return out, history, effective, nil
}

func (s *Service) observe(ctx context.Context, a *identity.Auth, admin bool) (State, error) {
	var out State
	e := s.within(ctx, func(ctx context.Context, tx transaction.Executor) error {
		if a != nil {
			if e := s.actor(ctx, tx, *a, admin); e != nil {
				return e
			}
		}
		var e error
		out, _, _, e = s.readLocked(ctx, tx)
		return e
	})
	if e != nil {
		return State{}, e
	}
	return out, nil
}
func (s *Service) StatusAdmin(ctx context.Context, a identity.Auth) (State, error) {
	return s.observe(ctx, &a, true)
}
func (s *Service) StatusPortal(ctx context.Context, a identity.Auth) (PortalState, error) {
	out, e := s.observe(ctx, &a, false)
	if e != nil {
		return PortalState{}, e
	}
	return PortalState{out.State, out.NewBusinessAllowed, out.Warning}, nil
}

// Check is internal admission for a caller that already authenticated its actor.
// Observation commits even for denial, preserving forward observed time.
func (s *Service) Check(ctx context.Context) error {
	out, e := s.observe(ctx, nil, false)
	if e != nil {
		return e
	}
	if !out.NewBusinessAllowed {
		return fail("LICENSE_NEW_BUSINESS_UNAVAILABLE")
	}
	return nil
}

func (s *Service) Import(ctx context.Context, a identity.Auth, input ImportInput, requestID string) (ImportResult, error) {
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return ImportResult{}, fail("ACCESS_DENIED")
	}
	if len(input.Artifact) > wire.MaximumArtifactBytes || !utf8.ValidString(input.Reason) || strings.TrimSpace(input.Reason) != input.Reason || input.Reason == "" || utf8.RuneCountInString(input.Reason) > wire.MaximumReasonRunes || strings.ContainsFunc(input.Reason, unicode.IsControl) || input.ExpectedCurrentLicenseID != "" && !wire.ValidID(input.ExpectedCurrentLicenseID) {
		return ImportResult{}, fail("VALIDATION_FAILED")
	}
	var result ImportResult
	var denial error
	e := s.within(ctx, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.actor(ctx, tx, a, true); e != nil {
			return e
		}
		// Domain denial is returned only after the actual rejection audit commits.
		reject := func(code string) error {
			if e := audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", "license.import_rejected", "failure", "platform_license", "", input.Reason, requestID); e != nil {
				return e
			}
			denial = fail(code)
			return nil
		}
		proof, e := wire.Verify([]byte(input.Artifact), s.trust)
		if e != nil {
			return reject("LICENSE_ARTIFACT_INVALID")
		}
		state, history, now, e := s.readLocked(ctx, tx)
		if e != nil {
			return e
		}
		if proof.Claims.DeploymentID != state.DeploymentID {
			return reject("LICENSE_DEPLOYMENT_MISMATCH")
		}
		for _, old := range history {
			if old.Claims.LicenseID == proof.Claims.LicenseID {
				if old.SHA256 != proof.SHA256 {
					return reject("LICENSE_ID_CONFLICT")
				}
				result = ImportResult{State: state, ImportedLicenseID: proof.Claims.LicenseID, Repeated: true}
				return nil
			}
		}
		if input.ExpectedCurrentLicenseID != state.HistoryHeadLicenseID {
			return reject("LICENSE_CURRENT_CHANGED")
		}
		if len(history) >= s.policy.MaximumImports {
			return reject("LICENSE_IMPORT_CAPACITY_REQUIRED")
		}
		if len(history) == 0 {
			if proof.Claims.ReplacesLicenseID != "" {
				return reject("LICENSE_PREDECESSOR_REQUIRED")
			}
		} else {
			last := history[len(history)-1].Claims
			start, _ := wire.Stamp(last.NotBefore)
			if start.After(now) {
				return reject("LICENSE_PENDING_SUCCESSOR_EXISTS")
			}
			if !renewal(last, proof.Claims) {
				return reject("LICENSE_RENEWAL_WINDOW_INVALID")
			}
		}
		changed, e := tx.Exec(ctx, licenseSQL(tx,
			`INSERT INTO platform_license_artifacts(license_id,sequence,deployment_id,artifact_sha256,artifact,predecessor_id,imported_by,reason) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8)`,
			`INSERT INTO platform_license_artifacts(license_id,sequence,deployment_id,artifact_sha256,artifact,predecessor_id,imported_by,reason) VALUES(?,?,?,?,?,NULLIF(?,''),?,?)`,
			`INSERT INTO "PLATFORM_LICENSE_ARTIFACTS" ("LICENSE_ID","SEQUENCE","DEPLOYMENT_ID","ARTIFACT_SHA256","ARTIFACT","PREDECESSOR_ID","IMPORTED_BY","REASON") VALUES(?,?,?,?,?,?,?,?)`,
			proof.Claims.LicenseID, len(history)+1, proof.Claims.DeploymentID, proof.SHA256, proof.Artifact, licensePredecessor(tx, proof.Claims.ReplacesLicenseID), a.Me.User.ID, input.Reason))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("STORAGE_UNAVAILABLE")
		}
		if e := audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", "license.imported", "success", "platform_license", proof.Claims.LicenseID, input.Reason, requestID); e != nil {
			return e
		}
		current, _, _, e := s.readLocked(ctx, tx)
		if e != nil {
			return e
		}
		result = ImportResult{State: current, ImportedLicenseID: proof.Claims.LicenseID}
		return nil
	})
	if e != nil {
		return ImportResult{}, e
	}
	if denial != nil {
		return ImportResult{}, denial
	}
	return result, nil
}
