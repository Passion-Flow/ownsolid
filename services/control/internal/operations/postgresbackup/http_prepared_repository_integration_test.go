//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type restoredHTTPPrepared struct {
	Format                string            `json:"format"`
	InputsSHA256          string            `json:"inputs_sha256"`
	ConfigurationSHA256   string            `json:"configuration_sha256"`
	LicenseID             string            `json:"license_id"`
	LicenseArtifactSHA256 string            `json:"license_artifact_sha256"`
	ImportRequestID       string            `json:"import_request_id"`
	Files                 map[string]string `json:"files"`
}

func restoredHTTPSetupInputDigest(in restoredHTTPInputs) string {
	in.AttemptID = ""
	raw, e := canonical(in)
	if e != nil {
		return ""
	}
	return digest(raw)
}

func preparedHTTPFiles(c config.Config, configuration, root string) []string {
	out := []string{configuration, c.Database.DSNFile, c.Security.AuthHMACKeyFile, c.Security.MailKeyringFile, c.Security.PasswordBlocklistFile, c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile, c.SMTP.CAFile, c.Licensing.TrustFile, c.ReleaseViewConfigFile, filepath.Join(root, "setup-intent.json"), filepath.Join(root, "license-artifact.json"), filepath.Join(root, "license-import-intent.json"), filepath.Join(root, "license-import-complete.json"), filepath.Join(root, "license-admin-private.json"), filepath.Join(root, "license-admin-committed.json")}
	for _, audience := range []string{"portal", "admin"} {
		out = append(out, filepath.Join(root, "current-"+audience+"-private.json"), filepath.Join(root, "current-"+audience+"-committed.json"))
	}
	return out
}

func commitRestoredHTTPPrepared(ctx context.Context, in restoredHTTPInputs, c config.Config, configuration string, pool *pgxpool.Pool, current map[string]retainedOriginalAuthority) error {
	root := filepath.Dir(configuration)
	raw, e := readPrivateBytes(configuration, 256<<10)
	if e != nil {
		return e
	}
	intentRaw, e := readPrivateBytes(filepath.Join(root, "license-import-intent.json"), 16<<10)
	if e != nil {
		return e
	}
	var intent struct {
		LicenseID   string `json:"license_id"`
		ArtifactSHA string `json:"artifact_sha256"`
		RequestID   string `json:"request_id"`
	}
	if json.Unmarshal(intentRaw, &intent) != nil || intent.LicenseID == "" || intent.RequestID == "" || !digestPattern.MatchString(intent.ArtifactSHA) {
		return errors.New("M18_EXACT_HTTP_LICENSE_INTENT_REQUIRED")
	}
	accounts := &identity.Service{DB: pool, Config: c}
	auth, e := accounts.Authenticate(ctx, current["admin"].Token, "admin")
	if e != nil {
		return e
	}
	service, e := licensing.FromConfig(accounts)
	if e != nil {
		return e
	}
	state, e := service.StatusAdmin(ctx, auth)
	if e != nil || string(state.State) != "active" || state.Current == nil || state.Current.LicenseID != intent.LicenseID || state.Current.ArtifactSHA256 != intent.ArtifactSHA {
		return errors.Join(e, errors.New("M18_EXACT_HTTP_COMMITTED_LICENSE_REQUIRED"))
	}
	if e = service.VerifyStored(ctx); e != nil {
		return e
	}
	prepared := restoredHTTPPrepared{Format: "projectone.m18.http-prepared.v1", InputsSHA256: restoredHTTPSetupInputDigest(in), ConfigurationSHA256: digest(raw), LicenseID: intent.LicenseID, LicenseArtifactSHA256: intent.ArtifactSHA, ImportRequestID: intent.RequestID, Files: map[string]string{}}
	for _, path := range preparedHTTPFiles(c, configuration, root) {
		if !contained("/fixture", path) {
			return errors.New("M18_HTTP_PREPARED_FILE_ESCAPE")
		}
		raw, e := readPrivateBytes(path, 8<<20)
		if e != nil {
			return e
		}
		prepared.Files[path] = digest(raw)
	}
	return fixtureJSON(filepath.Join(root, "setup-complete.json"), prepared)
}

// An existing incomplete setup never falls through to a new license import or
// new session issuance. Unknown effects require exact intent/state observation,
// not a fresh UUID or resetting target data. Only a fully committed identical
// setup can start a distinct new serving attempt.
func loadRestoredHTTPPrepared(ctx context.Context, in restoredHTTPInputs, pool *pgxpool.Pool) (config.Config, string, []byte, map[string]retainedOriginalAuthority, bool, error) {
	var zero config.Config
	root := filepath.Join("/fixture", "http-"+in.RunID)
	_, e := os.Lstat(root)
	if os.IsNotExist(e) {
		return zero, "", nil, nil, false, nil
	}
	if e != nil {
		return zero, "", nil, nil, false, e
	}
	directory, e := openPrivateRoot(root)
	if e != nil {
		return zero, "", nil, nil, false, e
	}
	directory.Close()
	var prepared restoredHTTPPrepared
	if _, e = readPrivateDocument(filepath.Join(root, "setup-complete.json"), 1<<20, &prepared); e != nil {
		return zero, "", nil, nil, false, errors.Join(e, errors.New("M18_HTTP_PARTIAL_SETUP_REQUIRES_EXACT_RECONCILIATION_NO_RESEND"))
	}
	if prepared.Format != "projectone.m18.http-prepared.v1" || prepared.InputsSHA256 != restoredHTTPSetupInputDigest(in) || prepared.ImportRequestID == "" {
		return zero, "", nil, nil, false, errors.New("M18_HTTP_PREPARED_BINDING_CHANGED")
	}
	intentBytes, e := readPrivateBytes(filepath.Join(root, "license-import-intent.json"), 16<<10)
	if e != nil {
		return zero, "", nil, nil, false, e
	}
	var intent struct {
		LicenseID      string `json:"license_id"`
		ArtifactSHA256 string `json:"artifact_sha256"`
		RequestID      string `json:"request_id"`
	}
	if json.Unmarshal(intentBytes, &intent) != nil || intent.RequestID != prepared.ImportRequestID || intent.LicenseID != prepared.LicenseID || intent.ArtifactSHA256 != prepared.LicenseArtifactSHA256 {
		return zero, "", nil, nil, false, errors.New("M18_HTTP_PREPARED_IMPORT_INTENT_CHANGED")
	}
	path := filepath.Join(root, "runtime.yaml")
	raw, e := readPrivateBytes(path, 256<<10)
	if e != nil || digest(raw) != prepared.ConfigurationSHA256 {
		return zero, "", nil, nil, false, errors.New("M18_HTTP_PREPARED_CONFIGURATION_CHANGED")
	}
	c, e := config.LoadProtected(path)
	if e != nil || c.Licensing == nil || !c.Knowledge.Enabled || !c.Workflows.Enabled {
		return zero, "", nil, nil, false, errors.Join(e, errors.New("M18_HTTP_FULL_PREPARED_PROFILE_REQUIRED"))
	}
	expected := preparedHTTPFiles(c, path, root)
	if len(prepared.Files) != len(expected) {
		return zero, "", nil, nil, false, errors.New("M18_HTTP_PREPARED_FILE_SET_CHANGED")
	}
	for _, name := range expected {
		if !contained("/fixture", name) {
			return zero, "", nil, nil, false, errors.New("M18_HTTP_PREPARED_FILE_ESCAPE")
		}
		body, e := readPrivateBytes(name, 8<<20)
		if e != nil || digest(body) != prepared.Files[name] {
			return zero, "", nil, nil, false, errors.New("M18_HTTP_PREPARED_FILE_CHANGED")
		}
	}
	current := map[string]retainedOriginalAuthority{}
	accounts := &identity.Service{DB: pool, Config: c}
	for _, audience := range []string{"portal", "admin"} {
		var value retainedOriginalAuthority
		if _, e = readPrivateDocument(filepath.Join(root, "current-"+audience+"-private.json"), 16<<10, &value); e != nil || value.Audience != audience || value.Epoch != 2 {
			return zero, "", nil, nil, false, errors.Join(e, errors.New("M18_HTTP_PREPARED_CREDENTIAL_CHANGED"))
		}
		if _, e = accounts.Authenticate(ctx, value.Token, audience); e != nil {
			return zero, "", nil, nil, false, e
		}
		current[audience] = value
	}
	auth, e := accounts.Authenticate(ctx, current["admin"].Token, "admin")
	if e != nil {
		return zero, "", nil, nil, false, e
	}
	service, e := licensing.FromConfig(accounts)
	if e != nil {
		return zero, "", nil, nil, false, e
	}
	state, e := service.StatusAdmin(ctx, auth)
	if e != nil || string(state.State) != "active" || state.Current == nil || state.Current.LicenseID != prepared.LicenseID || state.Current.ArtifactSHA256 != prepared.LicenseArtifactSHA256 {
		return zero, "", nil, nil, false, errors.Join(e, errors.New("M18_HTTP_PREPARED_LICENSE_CHANGED"))
	}
	if e = service.VerifyStored(ctx); e != nil {
		return zero, "", nil, nil, false, e
	}
	ca, e := readPrivateBytes(c.SMTP.CAFile, 1<<20)
	return c, path, ca, current, true, e
}

func TestRestoredHTTPSetupBindsAuthorityAndArtifactsButSeparatesServingAttempt(t *testing.T) {
	original := restoredHTTPInputs{Format: "projectone.m18.restored-http-inputs.v1", Mode: "populated_positive", RunID: "setup", AttemptID: "first", TargetScopeID: "target", SourceManifestSHA256: "manifest", NativeCompletionSHA256: "complete", CurrentControlSHA256: "current-control", NativeControlSHA256: "native-control", ImageID: "image", ProjectionDirectory: "/projection", SourceCommit: "source"}
	same := original
	same.AttemptID = "next-serving-attempt"
	if restoredHTTPSetupInputDigest(same) != restoredHTTPSetupInputDigest(original) {
		t.Fatal("a new serving attempt must reuse exact committed setup")
	}
	for _, change := range []func(*restoredHTTPInputs){func(v *restoredHTTPInputs) { v.TargetScopeID = "other" }, func(v *restoredHTTPInputs) { v.SourceManifestSHA256 = "other" }, func(v *restoredHTTPInputs) { v.NativeCompletionSHA256 = "other" }, func(v *restoredHTTPInputs) { v.CurrentControlSHA256 = "other" }, func(v *restoredHTTPInputs) { v.ImageID = "other" }, func(v *restoredHTTPInputs) { v.Mode = "original_authority" }} {
		value := original
		change(&value)
		if restoredHTTPSetupInputDigest(value) == restoredHTTPSetupInputDigest(original) {
			t.Fatal("changed setup authority/artifact accepted")
		}
	}
}
