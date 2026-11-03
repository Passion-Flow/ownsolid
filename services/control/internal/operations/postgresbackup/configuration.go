package postgresbackup

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func contained(root, path string) bool {
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func configurationSnapshot(operator, runtime config.Config, operatorPath string, r release.PostgreSQLBackupRequest) ([]byte, error) {
	bad := errors.New("POSTGRES_BACKUP_CONFIGURATION_CLOSURE_REQUIRED")
	if operator.DatabaseEngine() != config.PostgreSQLDatabaseEngine || runtime.DatabaseEngine() != config.PostgreSQLDatabaseEngine || operator.Server.Profile() != r.EffectiveApplicationProfile() || runtime.Server.Profile() != r.EffectiveApplicationProfile() {
		return nil, bad
	}
	if _, e := selectedBackupSchema(r.EffectiveApplicationProfile()); e != nil {
		return nil, e
	}
	// An enabled registry must have its original typed supplementary payload owner.
	if r.EffectiveApplicationProfile() == release.MVPPostgreSQLCPUApplicationProfile && (operator.ImageRegistryConfigFile != "" && operator.ImageRegistry == nil || runtime.ImageRegistryConfigFile != "" && runtime.ImageRegistry == nil) {
		return nil, errors.New("POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED")
	}
	compare := operator
	compare.Database.DSNFile = runtime.Database.DSNFile
	compare.Database.MaxOpenConnections = runtime.Database.MaxOpenConnections
	compare.Database.MaxIdleConnections = runtime.Database.MaxIdleConnections
	if !reflect.DeepEqual(compare, runtime) {
		return nil, errors.New("POSTGRES_BACKUP_RUNTIME_CONFIGURATION_MISMATCH")
	}
	if validateStoreRoots(runtime, r.ConfigurationRoot, r.BackupRoot, r.Policy.ScratchRoot) != nil || !contained(r.ConfigurationRoot, r.RuntimeConfigurationFile) || r.RuntimeEnvironmentFile != "" && !contained(r.ConfigurationRoot, r.RuntimeEnvironmentFile) || relatedPaths(r.ConfigurationRoot, operatorPath) || relatedPaths(r.ConfigurationRoot, operator.Database.DSNFile) || relatedPaths(r.ConfigurationRoot, runtime.Database.DSNFile) {
		return nil, bad
	}
	// These exact assets are supplied by the source-bound immutable image. All
	// other file/directory references in current typed configuration must be in
	// the explicit private projection. Future configuration cannot escape this
	// closure merely by acquiring a new *_file or *_directory field.
	imageOwned := map[string]string{
		"security.password_blocklist_file":           "/opt/projectone/security/password-blocklist.txt",
		"components.node_exporter_catalog_directory": "/opt/projectone/components/node-exporter/catalog",
		"datasets.processor_file":                    "/opt/projectone/data-processing/process.py",
		"datasets.python_executable":                 "/usr/local/bin/python3.12",
		"knowledge.worker_file":                      "/opt/projectone/knowledge/embed.py",
		"knowledge.python_executable":                "/opt/projectone/knowledge-venv/bin/python3.12",
		"knowledge.model_directory":                  "/opt/projectone/knowledge-encoder",
		"server.portal_assets":                       "/opt/projectone/portal",
		"server.admin_assets":                        "/opt/projectone/admin",
	}
	var visit func(reflect.Value, string) error
	visit = func(v reflect.Value, path string) error {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return nil
			}
			return visit(v.Elem(), path)
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				field := v.Type().Field(i)
				name := strings.Split(field.Tag.Get("yaml"), ",")[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = field.Name
				}
				next := name
				if path != "" {
					next = path + "." + name
				}
				if e := visit(v.Field(i), next); e != nil {
					return e
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				if e := visit(v.Index(i), path); e != nil {
					return e
				}
			}
		case reflect.String:
			value := v.String()
			if value == "" || path == "database.dsn_file" || imageOwned[path] == value {
				return nil
			}
			if path == "components.node_exporter_archive_file" && release.PostgreSQLBackupPath(value) && contained("/opt/projectone/components/node-exporter/catalog", value) {
				return nil
			}
			if strings.HasSuffix(path, "_file") || strings.HasSuffix(path, "_directory") || strings.HasSuffix(path, "_executable") || strings.HasSuffix(path, "_assets") {
				if !contained(r.ConfigurationRoot, value) {
					return bad
				}
				if strings.HasSuffix(path, "_directory") || strings.HasSuffix(path, "_assets") {
					root, e := openPrivateRoot(value)
					if e != nil {
						return bad
					}
					if root.Close() != nil {
						return bad
					}
				} else {
					info, e := os.Lstat(value)
					valid := e == nil && owned(info, false)
					if e == nil && strings.HasSuffix(path, "_executable") {
						owner, ok := info.Sys().(*syscall.Stat_t)
						valid = ok && owner.Uid == uint32(os.Geteuid()) && owner.Nlink == 1 && info.Mode().IsRegular() && info.Mode().Perm()&0022 == 0 && info.Mode().Perm()&0111 != 0
					}
					if !valid || info.Size() < 1 || info.Size() > r.Policy.MaximumConfigurationBytes {
						return bad
					}
					resolved, e := filepath.EvalSymlinks(value)
					if e != nil || resolved != value {
						return bad
					}
				}
			}
		}
		return nil
	}
	if e := visit(reflect.ValueOf(runtime), ""); e != nil {
		return nil, e
	}
	hmac, e := readPrivateBytes(runtime.Security.AuthHMACKeyFile, security.MaximumHMACKeyBytes)
	if e != nil || len(hmac) < security.MinimumHMACKeyBytes {
		return nil, bad
	}
	if _, e = mail.LoadKeyring(runtime.Security.MailKeyringFile); e != nil {
		return nil, bad
	}
	if _, e = security.NewPasswords(runtime.Security.PasswordBlocklistFile, runtime.Policy().Identity); e != nil {
		return nil, bad
	}
	if _, e = localization.New(runtime.Localization, runtime.Policy().Localization); e != nil {
		return nil, bad
	}
	if runtime.SSO.KeyringFile != "" {
		if _, e = sso.LoadKeyring(runtime.SSO.KeyringFile); e != nil {
			return nil, bad
		}
	}
	raw, e := yaml.Marshal(runtime)
	if e != nil || int64(len(raw)) > r.Policy.MaximumConfigurationBytes {
		return nil, bad
	}
	// The typed configuration contains references, never the operator URI.
	return raw, nil
}
