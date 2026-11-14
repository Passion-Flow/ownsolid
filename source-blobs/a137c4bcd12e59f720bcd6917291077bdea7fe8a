//go:build mysql_integration

package mysqlmigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// These schema fixtures are synthetic bytes,not signed License artifacts or SSO
// provider/crypto qualification. All SQL,constraints,migration and TLS are real.
func licenseSchemaPrefix(t *testing.T) (*mysqlstore.Store, Catalog) {
	t.Helper()
	s, c, _ := actualMigration(t)
	prefix := c
	prefix.steps = c.steps[:38]
	migrationRun(t, s, prefix, Apply)
	b := seedTriggerBusiness(t, s)
	finishTriggerFlow(t, s, b)
	migrationExec(t, s, triggerSessionInsert, triggerSessionArguments(b, uuid.NewString())...)
	return s, c
}
func licenseSchemaJournalPrefix(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	return licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step<=38 ORDER BY step`)
}
func licenseSchemaQueryDigest(t *testing.T, s *mysqlstore.Store, query string) string {
	t.Helper()
	rows, e := s.Pool().QueryContext(t.Context(), query)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer rows.Close()
	values := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			t.Fatal("actual License schema canonical row")
		}
		values = append(values, raw)
	}
	if rows.Err() != nil {
		t.Fatal(mysqlstore.SafeError(rows.Err()))
	}
	raw, _ := json.Marshal(values)
	return hash(string(raw))
}
func licenseSchemaOldDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	return hash(ssoSchemaDigest(t, s) + membershipRowsDigest(t, s) + licenseSchemaJournalPrefix(t, s))
}
func licenseSchemaDataDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	state := licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(id,deployment_id,observed_at) FROM platform_license_state ORDER BY id`)
	artifacts := licenseSchemaQueryDigest(t, s, `SELECT JSON_ARRAY(license_id,sequence,deployment_id,artifact_sha256,HEX(artifact),predecessor_id,imported_by,imported_at,reason) FROM platform_license_artifacts ORDER BY sequence`)
	return hash(licenseSchemaOldDigest(t, s) + state + artifacts)
}
func licenseSchemaDeployment(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	var id string
	var observed, now time.Time
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT deployment_id,observed_at,UTC_TIMESTAMP(6) FROM platform_license_state WHERE id=1`).Scan(&id, &observed, &now); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	parsed, e := uuid.Parse(id)
	if e != nil || parsed == uuid.Nil || parsed.String() != id || observed.Year() < 1970 || observed.Year() > 9999 || observed.Nanosecond()%1000 != 0 || observed.After(now.Add(time.Second)) {
		t.Fatal("actual canonical deployment/UTC microsecond fence invalid")
	}
	return id
}
func licenseSchemaSeedArtifacts(t *testing.T, s *mysqlstore.Store) [2]string {
	t.Helper()
	deployment := licenseSchemaDeployment(t, s)
	var user string
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT primary_admin_user_id FROM installation_state WHERE id=1`).Scan(&user); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	ids := [2]string{uuid.NewString(), uuid.NewString()}
	for index, id := range ids {
		raw := append([]byte{0, 255, byte(index)}, []byte("Explicit synthetic License schema bytes")...)
		predecessor := any(nil)
		if index > 0 {
			predecessor = ids[index-1]
		}
		migrationExec(t, s, `INSERT INTO platform_license_artifacts(license_id,sequence,deployment_id,artifact_sha256,artifact,predecessor_id,imported_by,reason) VALUES(?,?,?,?,?,?,?,?)`, id, index+1, deployment, hash(string(raw)), raw, predecessor, user, "Schema reason \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629")
	}
	return ids
}
func TestActualMySQLLicenseSchemaFresh(t *testing.T) {
	s, c, configuration := actualMigration(t)
	migrationRun(t, s, c, Apply)
	id := licenseSchemaDeployment(t, s)
	seedTriggerBusiness(t, s)
	licenseSchemaSeedArtifacts(t, s)
	before := licenseSchemaDataDigest(t, s)
	migrationRun(t, s, c, Readiness)
	events := 0
	r, e := Run(t.Context(), s, c, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !r.Ready || events != 0 || licenseSchemaDeployment(t, s) != id || licenseSchemaDataDigest(t, s) != before {
		t.Fatal("fresh41 repeat changed state/data", e)
	}
	reopened, observation, e := mysqlstore.Open(t.Context(), configuration)
	if e != nil || !observation.TLSAuthenticated {
		t.Fatal("independent verified TLS License schema reopen", e)
	}
	defer reopened.Close()
	migrationRun(t, reopened, c, Readiness)
	if licenseSchemaDataDigest(t, reopened) != before {
		t.Fatal("independent actual reopen changed License data")
	}
	sessionFKDefinition(t, s, c)
}
func TestActualMySQLLicenseSchemaUpgrade(t *testing.T) {
	s, c := licenseSchemaPrefix(t)
	before := licenseSchemaOldDigest(t, s)
	migrationRun(t, s, c, Apply)
	if licenseSchemaOldDigest(t, s) != before {
		t.Fatal("38 to41 changed retained business/source/session/journal values")
	}
	licenseSchemaDeployment(t, s)
	licenseSchemaSeedArtifacts(t, s)
	migrationRun(t, s, c, Readiness)
	sessionFKDefinition(t, s, c)
	t.Log("MYSQL_LICENSE_SCHEMA_DATA_SHA256=" + licenseSchemaDataDigest(t, s))
}
func licenseSchemaRefuses(t *testing.T, s *mysqlstore.Store, number uint16, query string, args ...any) {
	t.Helper()
	before := licenseSchemaDataDigest(t, s)
	_, e := s.Pool().ExecContext(t.Context(), query, args...)
	var safe *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != number {
		t.Fatal("actual License schema invalid input outcome", mysqlstore.SafeError(e))
	}
	if licenseSchemaDataDigest(t, s) != before {
		t.Fatal("failed License schema mutation changed accepted data")
	}
}
func TestActualMySQLLicenseSchemaConstraints(t *testing.T) {
	s, c := licenseSchemaPrefix(t)
	migrationRun(t, s, c, Apply)
	ids := licenseSchemaSeedArtifacts(t, s)
	for _, item := range []struct {
		name, query string
		args        []any
		number      uint16
	}{
		{"singleton", `INSERT INTO platform_license_state(id) VALUES(2)`, nil, 3819},
		{"deployment_uuid", `UPDATE platform_license_state SET deployment_id='invalid' WHERE id=1`, nil, 3819},
		{"observed_range", `UPDATE platform_license_state SET observed_at='1969-12-31 23:59:59' WHERE id=1`, nil, 3819},
		{"license_uuid", `UPDATE platform_license_artifacts SET license_id='invalid' WHERE license_id=?`, []any{ids[1]}, 3819},
		{"hash", `UPDATE platform_license_artifacts SET artifact_sha256=? WHERE license_id=?`, []any{strings.Repeat("A", 64), ids[1]}, 3819},
		{"sequence_zero", `UPDATE platform_license_artifacts SET sequence=0 WHERE license_id=?`, []any{ids[1]}, 3819},
		{"sequence_overflow", `UPDATE platform_license_artifacts SET sequence=1025 WHERE license_id=?`, []any{ids[1]}, 3819},
		{"artifact_empty", `UPDATE platform_license_artifacts SET artifact=? WHERE license_id=?`, []any{[]byte{}, ids[1]}, 3819},
		{"artifact_overflow", `UPDATE platform_license_artifacts SET artifact=? WHERE license_id=?`, []any{make([]byte, 65537), ids[1]}, 3819},
		{"reason_empty", `UPDATE platform_license_artifacts SET reason='' WHERE license_id=?`, []any{ids[1]}, 3819},
		{"reason_overflow", `UPDATE platform_license_artifacts SET reason=? WHERE license_id=?`, []any{strings.Repeat("\u4e16", 501), ids[1]}, 1406},
		{"self_predecessor", `UPDATE platform_license_artifacts SET predecessor_id=license_id WHERE license_id=?`, []any{ids[1]}, 3819},
		{"missing_deployment", `UPDATE platform_license_artifacts SET deployment_id=? WHERE license_id=?`, []any{uuid.NewString(), ids[1]}, 1452},
		{"missing_importer", `UPDATE platform_license_artifacts SET imported_by=? WHERE license_id=?`, []any{uuid.NewString(), ids[1]}, 1452},
		{"missing_predecessor", `UPDATE platform_license_artifacts SET predecessor_id=? WHERE license_id=?`, []any{uuid.NewString(), ids[1]}, 1452},
		{"duplicate_sequence", `UPDATE platform_license_artifacts SET sequence=1 WHERE license_id=?`, []any{ids[1]}, 1062},
		{"referenced_predecessor", `DELETE FROM platform_license_artifacts WHERE license_id=?`, []any{ids[0]}, 1451},
		{"referenced_deployment", `DELETE FROM platform_license_state WHERE id=1`, nil, 1451},
	} {
		t.Run(item.name, func(t *testing.T) { licenseSchemaRefuses(t, s, item.number, item.query, item.args...) })
	}
	t.Run("duplicate_hash", func(t *testing.T) {
		var sha string
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT artifact_sha256 FROM platform_license_artifacts WHERE license_id=?`, ids[0]).Scan(&sha); e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		licenseSchemaRefuses(t, s, 1062, `UPDATE platform_license_artifacts SET artifact_sha256=? WHERE license_id=?`, sha, ids[1])
	})
	t.Run("binary_and_unicode_upper_bounds", func(t *testing.T) {
		raw := make([]byte, 65536)
		for index := range raw {
			raw[index] = byte(index)
		}
		migrationExec(t, s, `UPDATE platform_license_artifacts SET artifact=?,artifact_sha256=?,reason=? WHERE license_id=?`, raw, hash(string(raw)), strings.Repeat("\u4e16", 500), ids[1])
		var actual []byte
		var reason string
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT artifact,reason FROM platform_license_artifacts WHERE license_id=?`, ids[1]).Scan(&actual, &reason); e != nil || !bytes.Equal(actual, raw) || reason != strings.Repeat("\u4e16", 500) {
			t.Fatal("actual binary65536/Unicode500 readback", mysqlstore.SafeError(e))
		}
	})
	migrationRun(t, s, c, Readiness)
}
func licenseSchemaInterrupted(t *testing.T, step int, stage string) {
	t.Helper()
	s, c := licenseSchemaPrefix(t)
	before := licenseSchemaOldDigest(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, e := Run(ctx, s, c, testOptions(), Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || r.Ready || r.DirtyStep != step || !r.LockReleased {
		t.Fatal("exact interrupted License step must stay dirty", r, e)
	}
	var prior string
	if step > 40 || step == 40 && stage == Verified {
		prior = licenseSchemaDeployment(t, s)
	}
	if _, e = Run(t.Context(), s, c, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty Apply was silently repeated", e)
	}
	if _, e = Run(t.Context(), s, c, testOptions(), Readiness, nil); e == nil {
		t.Fatal("dirty License catalog served ready")
	}
	migrationRun(t, s, c, Reconcile)
	id := licenseSchemaDeployment(t, s)
	if prior != "" && id != prior || licenseSchemaOldDigest(t, s) != before {
		t.Fatal("explicit License reconciliation replaced identity or prior data")
	}
	migrationRun(t, s, c, Readiness)
}
func TestActualMySQLLicenseSchemaBeforeState(t *testing.T) { licenseSchemaInterrupted(t, 39, Applying) }
func TestActualMySQLLicenseSchemaAfterState(t *testing.T)  { licenseSchemaInterrupted(t, 39, Verified) }
func TestActualMySQLLicenseSchemaBeforeSeed(t *testing.T)  { licenseSchemaInterrupted(t, 40, Applying) }
func TestActualMySQLLicenseSchemaAfterSeed(t *testing.T)   { licenseSchemaInterrupted(t, 40, Verified) }
func TestActualMySQLLicenseSchemaBeforeHistory(t *testing.T) {
	licenseSchemaInterrupted(t, 41, Applying)
}
func TestActualMySQLLicenseSchemaAfterHistory(t *testing.T) {
	licenseSchemaInterrupted(t, 41, Verified)
}
func TestActualMySQLLicenseSchemaUnjournaledSeed(t *testing.T) {
	s, c := licenseSchemaPrefix(t)
	prefix := c
	prefix.steps = c.steps[:39]
	migrationRun(t, s, prefix, Apply)
	migrationExec(t, s, licenseStateSeedSQL)
	id := licenseSchemaDeployment(t, s)
	before := licenseSchemaOldDigest(t, s)
	r, e := Run(t.Context(), s, c, testOptions(), Apply, nil)
	if e == nil || e.Error() != CodeUnjournaledObject || r.Ready || licenseSchemaDeployment(t, s) != id || licenseSchemaOldDigest(t, s) != before {
		t.Fatal("unjournaled deployment silently adopted or replaced", r, e)
	}
}
func TestActualMySQLLicenseSchemaMissingAppliedSeed(t *testing.T) {
	s, c := licenseSchemaPrefix(t)
	migrationRun(t, s, c, Apply)
	before := licenseSchemaOldDigest(t, s)
	migrationExec(t, s, `DELETE FROM platform_license_state WHERE id=1`)
	for _, mode := range []Mode{Readiness, Apply, Reconcile} {
		r, e := Run(t.Context(), s, c, testOptions(), mode, nil)
		if e == nil || e.Error() != CodeDefinitionDrift || r.Ready {
			t.Fatal("missing applied deployment was repaired", r, e)
		}
		var count int
		if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM platform_license_state`).Scan(&count); e != nil || count != 0 || licenseSchemaOldDigest(t, s) != before {
			t.Fatal("missing deployment generated a replacement", mysqlstore.SafeError(e))
		}
	}
}
func TestActualMySQLLicenseSchemaWrongDefinition(t *testing.T) {
	s, c := licenseSchemaPrefix(t)
	migrationRun(t, s, c, Apply)
	licenseSchemaSeedArtifacts(t, s)
	before := licenseSchemaDataDigest(t, s)
	migrationExec(t, s, `ALTER TABLE platform_license_artifacts ADD INDEX unexpected_license_index(imported_at)`)
	for _, mode := range []Mode{Readiness, Apply, Reconcile} {
		r, e := Run(t.Context(), s, c, testOptions(), mode, nil)
		if e == nil || e.Error() != CodeDefinitionDrift || r.Ready || licenseSchemaDataDigest(t, s) != before {
			t.Fatal("wrong License definition accepted or data changed", r, e)
		}
	}
}
func TestActualMySQLLicenseSchemaReadback(t *testing.T) {
	s, c, _ := actualMigration(t)
	migrationRun(t, s, c, Readiness)
	licenseSchemaDeployment(t, s)
	sessionFKDefinition(t, s, c)
	var count int
	if e := s.Pool().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM platform_license_artifacts`).Scan(&count); e != nil || count != 2 {
		t.Fatal("retained actual License schema artifacts missing", mysqlstore.SafeError(e))
	}
	t.Log("MYSQL_LICENSE_SCHEMA_DATA_SHA256=" + licenseSchemaDataDigest(t, s))
}
