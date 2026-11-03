package postgresbackup

import (
	"context"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresmaintenance"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestMVPBackupProfileBindsExactNewLineage(t *testing.T) {
	legacy := DatabaseReadback{SchemaVersion: 22, Tables: 86}
	current := DatabaseReadback{SchemaVersion: database.MVPPostgreSQLCPUV1SchemaVersion, Tables: database.MVPPostgreSQLCPUV1TableCount}
	if !matchesDatabaseProfile(postgresmaintenance.ApplicationProfile, legacy) || !matchesDatabaseProfile(postgresmaintenance.MVPApplicationProfile, current) {
		t.Fatal("declared independent profiles refused")
	}
	for _, v := range []struct {
		profile string
		data    DatabaseReadback
	}{{"platform", current}, {"releases-cache-v1", legacy}, {postgresmaintenance.ApplicationProfile, current}, {postgresmaintenance.MVPApplicationProfile, legacy}, {postgresmaintenance.MVPApplicationProfile, DatabaseReadback{SchemaVersion: 27, Tables: 108}}} {
		if matchesDatabaseProfile(v.profile, v.data) {
			t.Fatal("foreign lineage admitted", v)
		}
	}
}

func TestMVPConfigurationRequiresRequestAndBothConfigurationOrigins(t *testing.T) {
	f := newRestoreInputFixture(t)
	r := f.manifest.Request
	r.ApplicationProfile = release.MVPPostgreSQLCPUApplicationProfile
	// Refuse before source files or SQL: selectors cannot silently switch the
	// legacy configuration closure into a different serving graph.
	if _, e := configurationSnapshot(f.operator, f.runtime, f.operatorPath, r); e == nil || e.Error() != "POSTGRES_BACKUP_CONFIGURATION_CLOSURE_REQUIRED" {
		t.Fatal("mismatched profile accepted", e)
	}
}

func mvpBindingManifestFixture() Manifest {
	r := requestFixture()
	r.ApplicationProfile = release.MVPPostgreSQLCPUApplicationProfile
	b := &MVPSourceBinding{ApplicationProfile: r.EffectiveApplicationProfile(), SchemaVersion: database.MVPPostgreSQLCPUV1SchemaVersion, SystemIdentifier: "7702686790272344104", DatabaseOID: 16384, BackendPID: 400, RuntimePrincipal: "synthetic_runtime", PrincipalOID: 16385, PrincipalVerifierSHA256: strings.Repeat("a", 64)}
	for _, kind := range []string{"artifactstores", "imageregistry"} {
		b.ExternalArtifacts = append(b.ExternalArtifacts, MVPExternalArtifactOwnership{Owner: kind, ContentSHA256: strings.Repeat("b", 64), PayloadAuthority: "original-owner-external-payload-and-reconciliation-required"})
	}
	return Manifest{Request: r, Database: DatabaseReadback{BackendPID: 400}, MVPSource: b}
}
func TestMVPFreshTargetCannotAliasSourceNativeIdentity(t *testing.T) {
	m := mvpBindingManifestFixture()
	if e := validMVPSourceBinding(m); e != nil {
		t.Fatal(e)
	}
	if e := distinctMVPSourceTarget(m, RestoreTarget{SystemIdentifier: m.MVPSource.SystemIdentifier, DatabaseOID: m.MVPSource.DatabaseOID}); e == nil || e.Error() != "POSTGRES_RESTORE_MVP_SOURCE_TARGET_ALIAS" {
		t.Fatal("source database admitted as fresh target", e)
	}
	for _, target := range []RestoreTarget{{SystemIdentifier: m.MVPSource.SystemIdentifier, DatabaseOID: m.MVPSource.DatabaseOID + 1}, {SystemIdentifier: "7702686790272344105", DatabaseOID: m.MVPSource.DatabaseOID}} {
		if e := distinctMVPSourceTarget(m, target); e != nil {
			t.Fatal(e)
		}
	}
	m.Request.ApplicationProfile = ""
	if e := validMVPSourceBinding(m); e == nil {
		t.Fatal("MVP metadata injected into legacy format")
	}
	if e := recheckMVPSourceBinding(context.Background(), nil, requestFixture(), nil); e != nil {
		t.Fatal("historical release unexpectedly uses new source binding", e)
	}
}

func TestMVPEnabledRegistryCannotPublishMetadataOnlyBackup(t *testing.T) {
	f := newRestoreInputFixture(t)
	r := f.manifest.Request
	r.ApplicationProfile = release.MVPPostgreSQLCPUApplicationProfile
	profile := r.ApplicationProfile
	f.operator.Server.ApplicationProfile = &profile
	f.runtime.Server.ApplicationProfile = &profile
	f.operator.ImageRegistryConfigFile = "/absent/registry.json"
	f.runtime.ImageRegistryConfigFile = f.operator.ImageRegistryConfigFile
	if _, e := configurationSnapshot(f.operator, f.runtime, f.operatorPath, r); e == nil || e.Error() != "POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED" {
		t.Fatal("metadata-only enabled registry backup admitted", e)
	}
}

func TestMVPPrincipalAndDisabledRetainedRegistryCannotEscapeOwners(t *testing.T) {
	r := requestFixture()
	if e := verifyMVPPrincipal(nil, nil, nil, r); e != nil {
		t.Fatal("legacy principal path changed", e)
	}
	r.ApplicationProfile = release.MVPPostgreSQLCPUApplicationProfile
	if e := verifyMVPPrincipal(t.Context(), nil, nil, r); e == nil || e.Error() != "POSTGRES_BACKUP_LOCKED_CONTEXT_REQUIRED" {
		t.Fatal("new pool/implicit context was admitted", e)
	}
	m := mvpBindingManifestFixture()
	m.MVPSource.ExternalArtifacts[1].Records = 1
	if e := validMVPSourceBinding(m); e == nil || e.Error() != "POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED" {
		t.Fatal("disabled but retained registry payload silently omitted", e)
	}
}
