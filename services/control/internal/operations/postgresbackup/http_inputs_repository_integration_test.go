//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

type restoredHTTPInputs struct {
	Format                 string `json:"format"`
	Mode                   string `json:"mode"`
	RunID                  string `json:"run_id"`
	AttemptID              string `json:"attempt_id"`
	TargetScopeID          string `json:"target_scope_id"`
	SourceManifestSHA256   string `json:"source_manifest_sha256"`
	NativeCompletionSHA256 string `json:"native_completion_sha256"`
	SourceArchiveSHA256    string `json:"source_archive_sha256"`
	NativeControlSHA256    string `json:"native_control_sha256"`
	CurrentControlSHA256   string `json:"current_control_sha256"`
	SourceCommit           string `json:"source_commit"`
	ImageID                string `json:"image_id"`
	ProjectionDirectory    string `json:"projection_directory"`
}

func loadRestoredHTTPInputs() (restoredHTTPInputs, error) {
	var in restoredHTTPInputs
	if os.Geteuid() != 65534 || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return in, errors.New("M18_EXACT_AMD64_HTTP_FIXTURE_REQUIRED")
	}
	if _, e := readPrivateDocument("/fixture/http-inputs.json", 32<<10, &in); e != nil {
		return in, e
	}
	if in.Format != "projectone.m18.restored-http-inputs.v1" || in.Mode != "populated_positive" && in.Mode != "original_authority" || !view.ValidScopeID(in.RunID) || !view.ValidScopeID(in.AttemptID) || !view.ValidScopeID(in.TargetScopeID) || in.ProjectionDirectory != "/projection" || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(in.SourceCommit) {
		return in, errors.New("M18_HTTP_INPUT_BINDING_INVALID")
	}
	for _, sha := range []string{in.SourceManifestSHA256, in.NativeCompletionSHA256, in.SourceArchiveSHA256, in.NativeControlSHA256, in.CurrentControlSHA256, strings.TrimPrefix(in.ImageID, "sha256:")} {
		if !release.ValidSHA256(sha) {
			return in, errors.New("M18_HTTP_INPUT_BINDING_INVALID")
		}
	}
	if !strings.HasPrefix(in.ImageID, "sha256:") {
		return in, errors.New("M18_HTTP_IMAGE_BINDING_INVALID")
	}
	return in, nil
}

// The ordinary serving configuration is separate from the immutable native
// configuration. It retains full releases/Knowledge/workflows and existing
// restored roots/role, with actual TLS and a real signed whole-platform license.
func restoredHTTPConfiguration(ctx context.Context, in restoredHTTPInputs, request release.PostgreSQLRestoreRequest, complete RestoreCompletion, manifest Manifest, pool *pgxpool.Pool) (config.Config, string, []byte, error) {
	var zero config.Config
	c, e := config.LoadProtected(request.RuntimeConfigurationFile)
	if e != nil {
		return zero, "", nil, e
	}
	if c.Server.Profile() != "releases" || !c.Knowledge.Enabled || !c.Workflows.Enabled || request.TargetScopeID != in.TargetScopeID || complete.ManifestSHA256 != in.SourceManifestSHA256 || manifest.Archive.SHA256 != in.SourceArchiveSHA256 || manifest.Request.Policy.ControlSHA256 != in.NativeControlSHA256 {
		return zero, "", nil, errors.New("M18_FULL_RESTORED_HTTP_PROFILE_REQUIRED")
	}
	root := filepath.Join("/fixture", "http-"+in.RunID)
	owned, e := newDirectory(root)
	if e != nil {
		return zero, "", nil, e
	}
	if e = owned.Close(); e != nil {
		return zero, "", nil, e
	}
	certificate, key, ca, e := restoredHTTPTLS()
	if e != nil {
		return zero, "", nil, e
	}
	for name, raw := range map[string][]byte{"server.pem": certificate, "server.key": key, "ca.pem": ca} {
		if e = fixtureWrite(filepath.Join(root, name), raw); e != nil {
			return zero, "", nil, e
		}
	}
	c.Server.TLSCertificateFile = filepath.Join(root, "server.pem")
	c.Server.TLSPrivateKeyFile = filepath.Join(root, "server.key")
	c.SMTP.CAFile = filepath.Join(root, "ca.pem")
	// The original advertised DNS origins and loopback bind remain explicit.
	if c.Server.PortalOrigin != "https://portal.internal:8080" || c.Server.AdminOrigin != "https://admin.internal:8080" || c.Server.ListenAddress != "127.0.0.1:8080" || c.Server.EffectiveTransportMode() != config.TransportDirectHTTPS {
		return zero, "", nil, errors.New("M18_EXACT_TLS_ORIGINS_REQUIRED")
	}
	c.Knowledge.ScratchRoot = filepath.Join(root, "knowledge-work")
	if e = os.Mkdir(c.Knowledge.ScratchRoot, 0700); e != nil {
		return zero, "", nil, e
	}
	private, trust, e := licensewire.GenerateKey("synthetic-m18-" + in.RunID)
	if e != nil {
		return zero, "", nil, e
	}
	trustBytes, e := json.Marshal(trust)
	if e != nil {
		return zero, "", nil, e
	}
	trustPath := filepath.Join(root, "license-trust.json")
	if e = fixtureWrite(trustPath, trustBytes); e != nil {
		return zero, "", nil, e
	}
	c.Licensing = &config.LicensingConfiguration{TrustFile: trustPath}
	c.ReleaseViewConfigFile = filepath.Join(root, "release-view.json")
	policy := view.Configuration{Directory: in.ProjectionDirectory, ScopeID: in.TargetScopeID, ManifestSHA256: complete.ManifestSHA256, ConfigurationSHA256: manifest.EffectiveConfiguration.SHA256, PublisherUID: 0, PageSize: 10, OperationSeconds: 5}
	if e = policy.Validate(); e != nil {
		return zero, "", nil, e
	}
	if e = fixtureJSON(c.ReleaseViewConfigFile, policy); e != nil {
		return zero, "", nil, e
	}
	if e = c.Validate(); e != nil {
		return zero, "", nil, e
	}
	yamlBytes, e := yaml.Marshal(c)
	if e != nil {
		return zero, "", nil, e
	}
	path := filepath.Join(root, "runtime.yaml")
	if e = fixtureWrite(path, yamlBytes); e != nil {
		return zero, "", nil, e
	}
	c, e = config.LoadProtected(path)
	if e != nil {
		return zero, "", nil, e
	}
	if e = fixtureJSON(filepath.Join(root, "setup-intent.json"), map[string]any{"format": "projectone.m18.http-setup-intent.v1", "inputs_sha256": restoredHTTPSetupInputDigest(in), "runtime_configuration_sha256": digest(yamlBytes), "native_manifest_sha256": in.SourceManifestSHA256, "target_scope_id": in.TargetScopeID}); e != nil {
		return zero, "", nil, e
	}
	accounts := &identity.Service{DB: pool, Config: c}
	var user string
	var epoch int64
	if e = pool.QueryRow(ctx, `SELECT id::text,auth_epoch FROM users WHERE is_platform_admin AND status='active'`).Scan(&user, &epoch); e != nil {
		return zero, "", nil, e
	}
	admin, e := issueRestoredHTTPAuthority(ctx, accounts, pool, root, "license-admin", user, epoch, "admin")
	if e != nil {
		return zero, "", nil, e
	}
	auth, e := accounts.Authenticate(ctx, admin.Token, "admin")
	if e != nil {
		return zero, "", nil, e
	}
	service, e := licensing.FromConfig(accounts)
	if e != nil {
		return zero, "", nil, e
	}
	state, e := service.StatusAdmin(ctx, auth)
	if e != nil || string(state.State) != "missing" {
		return zero, "", nil, errors.Join(e, errors.New("M18_NEW_HTTP_LICENSE_STATE_REQUIRED"))
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic local M18 restored HTTP fixture", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Second).Format(time.RFC3339), NotBefore: now.Add(-time.Second).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	artifact, e := licensewire.Issue(claims, private)
	if e != nil {
		return zero, "", nil, e
	}
	if e = fixtureWrite(filepath.Join(root, "license-artifact.json"), artifact); e != nil {
		return zero, "", nil, e
	}
	importRequest := uuid.NewString()
	if e = fixtureJSON(filepath.Join(root, "license-import-intent.json"), map[string]any{"license_id": claims.LicenseID, "artifact_sha256": digest(artifact), "trust_sha256": digest(trustBytes), "request_id": importRequest, "expected_current_license_id": "", "reason": "Synthetic whole-platform fixture, no new license feature or purchase"}); e != nil {
		return zero, "", nil, e
	}
	if _, e = service.Import(ctx, auth, licensing.ImportInput{Artifact: string(artifact), Reason: "Synthetic full-profile restored HTTP fixture", ExpectedCurrentLicenseID: ""}, importRequest); e != nil {
		return zero, "", nil, e
	}
	state, e = service.StatusAdmin(ctx, auth)
	if e != nil || string(state.State) != "active" {
		return zero, "", nil, errors.Join(e, errors.New("M18_REAL_ACTIVE_LICENSE_REQUIRED"))
	}
	if e = service.Check(ctx); e != nil {
		return zero, "", nil, e
	}
	if e = fixtureJSON(filepath.Join(root, "license-import-complete.json"), map[string]any{"license_id": claims.LicenseID, "artifact_sha256": digest(artifact), "state": state.State}); e != nil {
		return zero, "", nil, e
	}
	// This is a truthful one-event projection of the already completed native
	// restoration, not an invented installation/package/commercial history.
	event := view.Event{Sequence: 1, OperationID: complete.RestoreID, Action: "native_restore_verified", Outcome: "passed", Time: complete.CompletedAt}
	eventBytes, e := canonical(event)
	if e != nil {
		return zero, "", nil, e
	}
	snapshot := view.Snapshot{Format: view.Format, ScopeID: in.TargetScopeID, ReleaseID: "m18-native-fixture", Profile: "releases-native-fixture", ArchiveSHA256: manifest.Archive.SHA256, ManifestSHA256: complete.ManifestSHA256, ConfigurationSHA256: manifest.EffectiveConfiguration.SHA256, Sequence: 1, FirstSequence: 1, HeadSHA256: digest(eventBytes), JournalBytes: int64(len(eventBytes)), RecordedAt: complete.CompletedAt, Events: []view.Event{event}}
	if e = snapshot.Validate(); e != nil {
		return zero, "", nil, e
	}
	if e = fixtureJSON("/fixture/http-publication-request.json", snapshot); e != nil {
		return zero, "", nil, e
	}
	return c, path, ca, nil
}

func issueRestoredHTTPAuthority(ctx context.Context, accounts *identity.Service, pool *pgxpool.Pool, root, label, user string, epoch int64, audience string) (retainedOriginalAuthority, error) {
	var out retainedOriginalAuthority
	tx, e := pool.Begin(ctx)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	issued, e := accounts.IssueSession(ctx, tx, user, epoch, audience, "", uuid.NewString(), nil)
	if e != nil {
		return out, e
	}
	out = retainedOriginalAuthority{audience, issued.Token, issued.SessionID, user, epoch, issued.ExpiresAt.UTC().Format(time.RFC3339Nano)}
	if e = fixtureJSON(filepath.Join(root, label+"-private.json"), out); e != nil {
		return out, e
	}
	if e = tx.Commit(ctx); e != nil {
		return out, e
	}
	e = fixtureJSON(filepath.Join(root, label+"-committed.json"), map[string]any{"audience": audience, "session_id": issued.SessionID, "user_id": user, "epoch": epoch, "token_sha256": digest([]byte(issued.Token))})
	return out, e
}

func restoredHTTPTLS() ([]byte, []byte, []byte, error) {
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, nil, nil, e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return nil, nil, nil, e
	}
	now := time.Now()
	certificate := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Synthetic M18 local TLS fixture"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"portal.internal", "admin.internal"}}
	der, e := x509.CreateCertificate(rand.Reader, certificate, certificate, &private.PublicKey, private)
	if e != nil {
		return nil, nil, nil, e
	}
	key, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		return nil, nil, nil, e
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return certPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), certPEM, nil
}
