package postgresbackup

import (
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestBackupBindsExactCurrentLineage(t *testing.T) {
	current := DatabaseReadback{SchemaVersion: database.SchemaVersion, Tables: database.TableCount}
	if !matchesDatabaseProfile(database.LineageName, current) {
		t.Fatal("current lineage refused")
	}
	for _, v := range []struct {
		profile string
		data    DatabaseReadback
	}{{"platform", current}, {"releases", DatabaseReadback{SchemaVersion: 22, Tables: 86}}, {"releases", current}, {database.LineageName, DatabaseReadback{SchemaVersion: 22, Tables: 86}}, {database.LineageName, DatabaseReadback{SchemaVersion: 27, Tables: 108}}} {
		if matchesDatabaseProfile(v.profile, v.data) {
			t.Fatal("foreign lineage admitted", v)
		}
	}
}

func mvpBindingManifestFixture() Manifest {
	r := requestFixture()
	r.ApplicationProfile = release.MVPPostgreSQLCPUApplicationProfile
	b := &MVPSourceBinding{ApplicationProfile: r.EffectiveApplicationProfile(), SchemaVersion: database.SchemaVersion, SystemIdentifier: "7702686790272344104", DatabaseOID: 16384, BackendPID: 400, RuntimePrincipal: "synthetic_runtime", PrincipalOID: 16385, PrincipalVerifierSHA256: strings.Repeat("a", 64)}
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
}

func TestMVPEnabledRegistryCannotPublishMetadataOnlyBackup(t *testing.T) {
	f := newRestoreInputFixture(t)
	r := f.manifest.Request
	r.ApplicationProfile = release.MVPPostgreSQLCPUApplicationProfile
	f.operator.ImageRegistryConfigFile = "/absent/registry.json"
	f.runtime.ImageRegistryConfigFile = f.operator.ImageRegistryConfigFile
	if _, e := configurationSnapshot(f.operator, f.runtime, f.operatorPath, r); e == nil || e.Error() != "POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED" {
		t.Fatal("metadata-only enabled registry backup admitted", e)
	}
}

func TestMVPPrincipalAndDisabledRetainedRegistryCannotEscapeOwners(t *testing.T) {
	r := requestFixture()
	if e := verifyMVPPrincipal(t.Context(), nil, nil, r); e == nil || e.Error() != "POSTGRES_BACKUP_LOCKED_CONTEXT_REQUIRED" {
		t.Fatal("new pool/implicit context was admitted", e)
	}
	m := mvpBindingManifestFixture()
	m.MVPSource.ExternalArtifacts[1].Records = 1
	if e := validMVPSourceBinding(m); e == nil || e.Error() != "POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED" {
		t.Fatal("disabled but retained registry payload silently omitted", e)
	}
}
