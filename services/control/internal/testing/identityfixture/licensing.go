package identityfixture

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func configureLicense(t *testing.T, c *config.Config, root string, automatic bool) {
	t.Helper()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	key, trust, e := wire.GenerateKey("synthetic-isolated-fixture")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "license-trust.json")
	for name, value := range map[string]any{path: trust, path + ".test-key": key} {
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if automatic {
		if e = os.WriteFile(path+".auto", nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	c.Licensing = &config.LicensingConfiguration{TrustFile: path}
}

// LicensePrivateKey reads only the synthetic fixture sidecar. This test package
// is never linked by the product server or distributed in a runtime bundle.
func LicensePrivateKey(t *testing.T, s *identity.Service) wire.PrivateKey {
	t.Helper()
	raw, e := os.ReadFile(s.Config.Licensing.TrustFile + ".test-key")
	if e != nil {
		t.Fatal(e)
	}
	key, e := wire.ParsePrivate(raw)
	if e != nil {
		t.Fatal(e)
	}
	return key
}

// SeedLicense sets up an identified signed synthetic license in the isolated
// test schema, without inventing product audit events or an allowing gate stub.
// Tests of import/audit use NewUnlicensed and the real HTTP/service import.
func SeedLicense(t *testing.T, s *identity.Service) {
	t.Helper()
	var deployment, user string
	if e := s.DB.QueryRow(context.Background(), "SELECT l.deployment_id::text,i.primary_admin_user_id::text FROM platform_license_state l CROSS JOIN installation_state i WHERE l.id=1 AND i.id=1").Scan(&deployment, &user); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	c := wire.Claims{FormatVersion: wire.FormatVersion, LicenseID: uuid.NewString(), CustomerReference: "Synthetic isolated fixture", DeploymentID: deployment, IssuedAt: now.Add(-time.Hour).Format(time.RFC3339), NotBefore: now.Add(-time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), GraceUntil: now.Add(25 * time.Hour).Format(time.RFC3339), Scope: wire.WholePlatformScope}
	raw, e := wire.Issue(c, LicensePrivateKey(t, s))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(context.Background(), "INSERT INTO platform_license_artifacts(license_id,sequence,deployment_id,artifact_sha256,artifact,imported_by,reason) VALUES($1,1,$2,$3,$4,$5,$6)", c.LicenseID, deployment, wire.Digest(raw), raw, user, "Synthetic fixture setup"); e != nil {
		t.Fatal(e)
	}
}
