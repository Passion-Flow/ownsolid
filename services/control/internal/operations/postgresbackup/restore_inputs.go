package postgresbackup

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func LoadRestoreRequest(path, environment string) (release.PostgreSQLRestoreRequest, string, error) {
	var r release.PostgreSQLRestoreRequest
	if _, e := readPrivateDocument(path, MaximumRequestBytes, &r); e != nil {
		return r, "", e
	}
	if environment != "" {
		raw, e := readPrivateBytes(environment, MaximumRequestBytes)
		if e != nil || applyPolicyEnvironment(&r.Policy, raw, "PROJECT_ONE_POSTGRES_RESTORE_") != nil {
			return release.PostgreSQLRestoreRequest{}, "", errors.New("POSTGRES_RESTORE_POLICY_ENVIRONMENT_INVALID")
		}
	}
	if !r.Valid() {
		return release.PostgreSQLRestoreRequest{}, "", errors.New("POSTGRES_RESTORE_REQUEST_INVALID")
	}
	raw, e := canonical(r)
	if e != nil {
		return release.PostgreSQLRestoreRequest{}, "", e
	}
	return r, digest(raw), nil
}

func loadRestoreSource(ctx context.Context, r release.PostgreSQLRestoreRequest) (Manifest, error) {
	var m Manifest
	if !r.Valid() {
		return m, errors.New("POSTGRES_RESTORE_REQUEST_INVALID")
	}
	raw, e := readPrivateDocument(filepath.Join(r.BackupRoot, candidateName), r.Policy.MaximumManifestBytes, &m)
	if e != nil || digest(raw) != r.SourceManifestSHA256 || !m.Request.Valid() || m.Request.BackupRoot != r.BackupRoot || m.Request.SourceScopeID == r.TargetScopeID || m.Request.SourceScopeID == r.RestoreID || m.Request.BackupID == r.RestoreID || m.Request.Database == r.TargetDatabase || m.Request.EffectiveApplicationProfile() != r.EffectiveApplicationProfile() {
		return Manifest{}, errors.New("POSTGRES_RESTORE_SOURCE_BINDING_INVALID")
	}
	if _, e = verifyPayload(ctx, m.Request, r.SourceManifestSHA256, true, r.Policy); e != nil {
		return Manifest{}, e
	}
	var bytes int64
	files := 0
	for _, store := range m.Stores {
		if store.Tree.Bytes < 0 || store.Tree.Files < 0 || store.Tree.Bytes > r.Policy.MaximumStoreBytes-bytes || store.Tree.Files > r.Policy.MaximumStoreFiles-files {
			return Manifest{}, errors.New("POSTGRES_RESTORE_SOURCE_LIMIT_REACHED")
		}
		bytes += store.Tree.Bytes
		files += store.Tree.Files
	}
	if m.Archive.Bytes > r.Policy.MaximumSQLBytes || m.Configuration.Bytes > r.Policy.MaximumConfigurationBytes {
		return Manifest{}, errors.New("POSTGRES_RESTORE_SOURCE_LIMIT_REACHED")
	}
	return m, nil
}

// AdmitRestore validates every non-SQL input before the caller opens a target
// connection. The actual operation repeats these checks under its held lease.
func AdmitRestore(ctx context.Context, operator, runtime config.Config, operatorPath string, r release.PostgreSQLRestoreRequest, fresh bool) error {
	if ctx == nil || !r.Valid() {
		return errors.New("POSTGRES_RESTORE_REQUEST_INVALID")
	}
	if e := RefuseAmbient(); e != nil {
		return e
	}
	m, e := loadRestoreSource(ctx, r)
	if e != nil {
		return e
	}
	if fresh {
		e = restoreInputClosure(ctx, operator, runtime, operatorPath, m, r)
	} else {
		e = restoreInputBindings(ctx, operator, runtime, operatorPath, m, r)
	}
	if e != nil {
		return e
	}
	if e = verifyTools(ctx, r.Policy, false); e != nil {
		return e
	}
	if e = trustedTool(ctx, r.Policy.ControlFile, r.Policy.ControlSHA256); e != nil {
		return e
	}
	if e = AdmitConnection(operator.Database.DSNFile, targetContext(m, r)); e != nil {
		return e
	}
	var bytes int64
	for _, s := range m.Stores {
		bytes += s.Tree.Bytes
	}
	for _, path := range append(mvpRestoreAllocationRoots(runtime, m), filepath.Dir(r.RestoreRoot), r.Policy.ScratchRoot) {
		if e = requireSpace(path, 2*bytes+mvpRegistryRestoreBytes(m)+4*r.Policy.MaximumManifestBytes, r.Policy.MinimumFreeBytes); e != nil {
			return e
		}
	}
	return nil
}

func targetContext(m Manifest, r release.PostgreSQLRestoreRequest) release.PostgreSQLBackupRequest {
	c := m.Request
	c.ApplicationProfile = r.ApplicationProfile
	c.BackupID = r.RestoreID
	c.Database = r.TargetDatabase
	c.Operator = r.TargetOperator
	c.RuntimeConfigurationFile = r.RuntimeConfigurationFile
	c.RuntimeEnvironmentFile = r.RuntimeEnvironmentFile
	c.ConfigurationRoot = r.ConfigurationRoot
	c.BackupRoot = r.BackupRoot
	c.Reason = r.Reason
	c.Policy = r.Policy
	return c
}

// Only extract recorded file references from the already hash-verified effective
// YAML; runtime configuration is still loaded/validated by its existing owner.
// No source environment/DSN is opened or made a target configuration authority.
type sourceConfigurationReferences struct {
	Security struct {
		HMAC string `yaml:"auth_hmac_key_file"`
		Mail string `yaml:"mail_keyring_file"`
	} `yaml:"security"`
	SSO struct {
		Keyring string `yaml:"keyring_file"`
	} `yaml:"sso"`
	Localization struct {
		Directory string   `yaml:"package_directory"`
		Required  []string `yaml:"required_locales"`
	} `yaml:"localization"`
}

func sourceReferencePath(m Manifest, path string) (string, error) {
	if !contained(m.Request.ConfigurationRoot, path) {
		return "", errors.New("POSTGRES_RESTORE_SOURCE_CONFIGURATION_INVALID")
	}
	rel, e := filepath.Rel(m.Request.ConfigurationRoot, path)
	if e != nil || rel == "." {
		return "", errors.New("POSTGRES_RESTORE_SOURCE_CONFIGURATION_INVALID")
	}
	return filepath.Join(m.Request.BackupRoot, "configuration", rel), nil
}

func restoreInputClosure(ctx context.Context, operator, runtime config.Config, operatorPath string, m Manifest, r release.PostgreSQLRestoreRequest) error {
	if e := restoreInputBindings(ctx, operator, runtime, operatorPath, m, r); e != nil {
		return e
	}
	if _, e := mvpRegistryTargetRoot(runtime, m, r, true); e != nil {
		return e
	}
	if _, e := os.Lstat(r.RestoreRoot); !os.IsNotExist(e) {
		return errors.New("POSTGRES_RESTORE_SCOPE_EXISTS")
	}
	return nil
}

// Reusable bound-input validation, distinct from one-time fresh record admission.
func restoreInputBindings(ctx context.Context, operator, runtime config.Config, operatorPath string, m Manifest, r release.PostgreSQLRestoreRequest) error {
	if r.EffectiveApplicationProfile() == database.LineageName {
		if e := registryConfigurationClosure(ctx, runtime, targetContext(m, r)); e != nil {
			return e
		}
	}
	if e := admitExpectedRestoreRoots(r.TargetRoots, storeRoots(runtime)); e != nil {
		return e
	}
	if _, e := configurationSnapshot(operator, runtime, operatorPath, targetContext(m, r)); e != nil {
		return e
	}
	if e := admitMVPRegistryTargetConfiguration(ctx, runtime, m, r); e != nil {
		return e
	}
	if e := restoreConnectionBindings(operator, runtime, m, r); e != nil {
		return e
	}
	if e := validateStoreRoots(runtime, r.BackupRoot, r.RestoreRoot, r.ConfigurationRoot, r.Policy.ScratchRoot); e != nil {
		return e
	}
	raw, e := readPrivateBytes(filepath.Join(r.BackupRoot, effectiveName), r.Policy.MaximumConfigurationBytes)
	if e != nil {
		return e
	}
	var source sourceConfigurationReferences
	if yaml.Unmarshal(raw, &source) != nil {
		return errors.New("POSTGRES_RESTORE_SOURCE_CONFIGURATION_INVALID")
	}
	for _, pair := range [][2]string{{source.Security.HMAC, runtime.Security.AuthHMACKeyFile}, {source.Security.Mail, runtime.Security.MailKeyringFile}, {source.SSO.Keyring, runtime.SSO.KeyringFile}} {
		if pair[0] == "" {
			if pair[1] != "" {
				return errors.New("POSTGRES_RESTORE_KEYRING_BINDING_MISMATCH")
			}
			continue
		}
		path, e := sourceReferencePath(m, pair[0])
		if e != nil {
			return e
		}
		original, e := fileDigest(ctx, path, r.Policy.MaximumConfigurationBytes)
		if e != nil {
			return e
		}
		actual, e := fileDigest(ctx, pair[1], r.Policy.MaximumConfigurationBytes)
		if e != nil || actual != original {
			return errors.New("POSTGRES_RESTORE_KEYRING_BINDING_MISMATCH")
		}
	}
	if !reflect.DeepEqual(source.Localization.Required, runtime.Localization.RequiredLocales) {
		return errors.New("POSTGRES_RESTORE_LOCALE_BINDING_MISMATCH")
	}
	if source.Localization.Directory == "" {
		if runtime.Localization.PackageDirectory != "" {
			return errors.New("POSTGRES_RESTORE_LOCALE_BINDING_MISMATCH")
		}
	} else {
		path, e := sourceReferencePath(m, source.Localization.Directory)
		if e != nil {
			return e
		}
		a, e := TreeDigest(ctx, path, r.Policy.MaximumConfigurationBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes)
		if e != nil {
			return e
		}
		b, e := TreeDigest(ctx, runtime.Localization.PackageDirectory, r.Policy.MaximumConfigurationBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes)
		if e != nil || a != b {
			return errors.New("POSTGRES_RESTORE_LOCALE_BINDING_MISMATCH")
		}
	}
	if relatedPaths(r.RestoreRoot, operatorPath) || relatedPaths(r.RestoreRoot, operator.Database.DSNFile) || relatedPaths(r.RestoreRoot, runtime.Database.DSNFile) {
		return errors.New("POSTGRES_RESTORE_RELATED_PATHS_REFUSED")
	}
	return nil
}

func restoreConnectionBindings(operator, runtime config.Config, m Manifest, r release.PostgreSQLRestoreRequest) error {
	bad := errors.New("POSTGRES_RESTORE_RUNTIME_CONNECTION_MISMATCH")
	request := targetContext(m, r)
	o, e := readConnection(operator.Database.DSNFile, request, nil)
	if e != nil {
		return e
	}
	raw, e := readPrivateBytes(runtime.Database.DSNFile, 16<<10)
	if e != nil {
		return e
	}
	u, e := url.Parse(strings.TrimSpace(string(raw)))
	if e != nil || u.User == nil {
		return bad
	}
	request.Operator = u.User.Username()
	if !request.Valid() || request.Operator == r.TargetOperator {
		return bad
	}
	c, e := readConnection(runtime.Database.DSNFile, request, nil)
	if e != nil || c.host != o.host || c.port != o.port || c.database != o.database || c.sslmode != o.sslmode || c.ca != o.ca || c.certificate != o.certificate || c.key != o.key {
		return bad
	}
	return nil
}
