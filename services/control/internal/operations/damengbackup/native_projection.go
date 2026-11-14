//go:build cgo && odbc_manager

package damengbackup

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/releases"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

type ConfigurationProjection struct {
	Root            string                      `json:"root"`
	Tree            postgresbackup.TreeReadback `json:"tree"`
	EffectiveSHA256 string                      `json:"effective_sha256"`
	Images          []ImageRootBinding          `json:"images"`
}

func withinRoot(root, path string) bool { return path == root || strings.HasPrefix(path, root+"/") }

// Absolute references remain explicit. Only the existing image-owned fields can
// use packaged assets; caller image pins cannot turn a key/certificate into an
// image exemption. All adjustable/custom references belong to the projection.
var imageOwnedReferences = map[string]string{
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

func projectionReferences(c config.Config, r OperatorRequest) error {
	for _, temporary := range []string{c.Knowledge.ScratchRoot, operatorStagingRoot(c)} {
		if temporary == "" {
			continue
		}
		if !canonical(temporary) {
			return fail("EPHEMERAL_REFERENCE_INVALID")
		}
		protected := append(platformRoots(c), r.ConfigurationRoot, r.OutputRoot)
		if r.SourceRoot != "" {
			protected = append(protected, r.SourceRoot)
		}
		for _, pin := range r.ImageRoots {
			protected = append(protected, pin.Path)
		}
		for _, root := range protected {
			if root != "" && related(temporary, root) {
				return fail("EPHEMERAL_REFERENCE_OVERLAP")
			}
		}
	}
	return projectionOwnerReferences(c, "", r)
}
func operatorStagingRoot(c config.Config) string {
	if c.ObjectStorage == nil {
		return ""
	}
	return c.ObjectStorage.StagingRoot
}

func projectionOwnerReferences(value any, prefix string, r OperatorRequest) error {
	imageAllowed := func(name, path string) bool {
		expected, ok := imageOwnedReferences[name]
		if !ok && name == "components.node_exporter_archive_file" {
			expected = "/opt/projectone/components/node-exporter/catalog"
			ok = withinRoot(expected, path)
		}
		if !ok || (expected != path && name != "components.node_exporter_archive_file") {
			return false
		}
		for _, pin := range r.ImageRoots {
			if withinRoot(pin.Path, path) {
				return true
			}
		}
		return false
	}
	var visit func(reflect.Value, string) error
	visit = func(v reflect.Value, name string) error {
		if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return nil
			}
			return visit(v.Elem(), name)
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				field := v.Type().Field(i)
				key := strings.Split(field.Tag.Get("yaml"), ",")[0]
				if key == "" || key == "-" {
					key = strings.Split(field.Tag.Get("json"), ",")[0]
				}
				if key == "" || key == "-" {
					key = field.Name
				}
				next := key
				if name != "" {
					next = name + "." + key
				}
				if e := visit(v.Field(i), next); e != nil {
					return e
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				if e := visit(v.Index(i), name); e != nil {
					return e
				}
			}
		case reflect.Map:
			for _, key := range v.MapKeys() {
				if e := visit(v.MapIndex(key), name); e != nil {
					return e
				}
			}
		case reflect.String:
			path := v.String()
			if path == "" {
				return nil
			}
			role, known := projectionReferenceRoles[name]
			if !known {
				// Unknown path-looking fields have no authority to open a file. Ordinary
				// opaque strings (including base64 keys beginning with '/') are not paths.
				leaf := name[strings.LastIndex(name, ".")+1:]
				if strings.HasSuffix(leaf, "_file") || strings.HasSuffix(leaf, "_directory") || strings.HasSuffix(leaf, "_root") || strings.HasSuffix(leaf, "_executable") || strings.HasSuffix(leaf, "_assets") {
					return fail("REFERENCE_ROLE_UNIMPLEMENTED")
				}
				return nil
			}
			if role == "store" || role == "temporary" {
				return nil
			}
			if role == "external_state" {
				return fail("STATE_OWNER_BACKUP_UNIMPLEMENTED")
			}
			if role == "nested_unimplemented" {
				return fail("NESTED_OWNER_CLOSURE_UNIMPLEMENTED")
			}
			if imageAllowed(name, path) {
				return nil
			}
			if !canonical(path) || !withinRoot(r.ConfigurationRoot, path) {
				return fail("CONFIGURATION_REFERENCE_OUTSIDE_CLOSURE")
			}
			return projectionTypedMaterial(path, role)

		}
		return nil
	}
	return visit(reflect.ValueOf(value), prefix)
}
func projectionReadback(ctx context.Context, c config.Config, r OperatorRequest, p SnapshotPolicy) (ConfigurationProjection, error) {
	var out ConfigurationProjection
	current, e := loadOperatorApplication(r)
	if e != nil || nativePlatformConfigurationSHA256(current) != nativePlatformConfigurationSHA256(c) {
		return out, fail("APPLICATION_SNAPSHOT_CHANGED")
	}
	if c.Validate() != nil || c.Server.Profile() != "platform" || c.DatabaseEngine() != config.DamengDatabaseEngine {
		return out, fail("CONFIGURATION_CLOSURE_REQUIRED")
	}
	if e = projectionReferences(c, r); e != nil {
		return out, e
	}
	if !withinRoot(r.ConfigurationRoot, r.ApplicationConfigurationFile) || r.ApplicationEnvironmentFile != "" && !withinRoot(r.ConfigurationRoot, r.ApplicationEnvironmentFile) {
		return out, fail("APPLICATION_SNAPSHOT_OUTSIDE_PROJECTION")
	}
	// Read the real owner formats, not a key-name heuristic or an unchecked digest.
	h, e := readPrivate(c.Security.AuthHMACKeyFile, security.MaximumHMACKeyBytes)
	if e != nil || len(h) < security.MinimumHMACKeyBytes {
		return out, fail("AUTH_KEY_CLOSURE_INVALID")
	}
	clear(h)
	if _, e = mail.LoadKeyring(c.Security.MailKeyringFile); e != nil {
		return out, fail("MAIL_KEYRING_CLOSURE_INVALID")
	}
	if c.SSO.KeyringFile != "" {
		if _, e = sso.LoadKeyring(c.SSO.KeyringFile); e != nil {
			return out, fail("SSO_KEYRING_CLOSURE_INVALID")
		}
	}
	if _, e = security.NewPasswords(c.Security.PasswordBlocklistFile, c.Policy().Identity); e != nil {
		return out, fail("PASSWORD_POLICY_CLOSURE_INVALID")
	}
	if _, e = localization.New(c.Localization, c.Policy().Localization); e != nil {
		return out, fail("LOCALE_CLOSURE_INVALID")
	}
	if _, e = c.TLSConfig(); e != nil {
		return out, fail("CERTIFICATE_CLOSURE_INVALID")
	}
	if _, e = c.MachineTLSConfig(); e != nil {
		return out, fail("MACHINE_CERTIFICATE_CLOSURE_INVALID")
	}
	// The runtime connector is a captured, protected configuration authority too.
	if e = projectionOptionalOwners(c, r); e != nil {
		return out, e
	}
	if c.Database.Dameng == nil {
		return out, fail("RUNTIME_CONNECTOR_CLOSURE_REQUIRED")
	}
	runtime, e := dameng.LoadConnectionConfiguration(c.Database.Dameng.RuntimeConfigurationFile, "")
	if e != nil {
		return out, fail("RUNTIME_CONNECTOR_CLOSURE_INVALID")
	}
	for _, path := range []string{runtime.PasswordFile, runtime.NativeManifestFile, runtime.NativeConfigurationDirectory, c.Database.Dameng.SchemaConfigurationFile} {
		if !withinRoot(r.ConfigurationRoot, path) {
			return out, fail("RUNTIME_CONNECTOR_REFERENCE_OUTSIDE_CLOSURE")
		}
	}
	tree, e := postgresbackup.TreeDigest(ctx, r.ConfigurationRoot, p.MaximumTreeBytes, p.MaximumTreeFiles, p.MaximumTreeMetadataBytes)
	if e != nil {
		return out, fail("CONFIGURATION_TREE_INVALID")
	}
	for _, image := range r.ImageRoots {
		actual, e := imageTreeDigest(ctx, image.Path, p, r.ImageRoots)
		if e != nil || actual != image.SHA256 {
			return out, fail("IMAGE_ASSET_BINDING_INVALID")
		}
	}
	out = ConfigurationProjection{r.ConfigurationRoot, tree, nativePlatformConfigurationSHA256(c), r.ImageRoots}
	return out, nil
}

// Image symlinks bind their exact link text and final target. Targets must be
// contained in an independently hashed declared root; arbitrary host fallback
// (including the usual unpinned interpreter fallback) is refused.
func imageTreeDigest(ctx context.Context, path string, p SnapshotPolicy, pins []ImageRootBinding) (string, error) {
	if !canonical(path) || (path != "/opt/projectone" && path != "/usr/local/bin/python3.12") {
		return "", fail("IMAGE_ROOT_UNSUPPORTED")
	}
	return digestImageAssets(ctx, path, p, func(target string) bool {
		for _, pin := range pins {
			if withinRoot(pin.Path, target) {
				return true
			}
		}
		return false
	})
}
func digestImageAssets(ctx context.Context, path string, p SnapshotPolicy, allowed func(string) bool) (string, error) {
	if noSymlinks(filepath.Dir(path)) != nil {
		return "", fail("IMAGE_ASSET_PATH_INVALID")
	}
	h := newHash()
	var bytes int64
	files := 0
	metadata := p.MaximumTreeMetadataBytes
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if ctx.Err() != nil || depth > 64 {
			return fail("IMAGE_ASSET_READ_INTERRUPTED")
		}
		info, e := os.Lstat(path)
		if e != nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 || info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSymlink == 0 {
			return fail("IMAGE_ASSET_PATH_INVALID")
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
			return fail("IMAGE_ASSET_OWNER_INVALID")
		}
		metadata -= int64(len(path) + 128)
		if metadata < 0 {
			return fail("IMAGE_ASSET_METADATA_LIMIT")
		}
		kind := "file"
		size := info.Size()
		link := ""
		if info.IsDir() {
			kind = "directory"
			size = 0
		} else if info.Mode()&os.ModeSymlink != 0 {
			kind = "symlink"
			link, e = os.Readlink(path)
			if e != nil {
				return fail("IMAGE_ASSET_PATH_INVALID")
			}
			metadata -= int64(len(link))
			resolved, e := filepath.EvalSymlinks(path)
			if e != nil || metadata < 0 || !allowed(resolved) {
				return fail("IMAGE_ASSET_SYMLINK_OUTSIDE_CLOSURE")
			}
			again, e := os.Lstat(path)
			current, ce := os.Readlink(path)
			if e != nil || ce != nil || !os.SameFile(info, again) || current != link || !again.ModTime().Equal(info.ModTime()) {
				return fail("IMAGE_ASSET_CHANGED")
			}
		} else if !info.Mode().IsRegular() {
			return fail("IMAGE_ASSET_PATH_INVALID")
		}
		_, _ = h.Write(jsonBytes(struct {
			Path string
			Kind string
			Mode uint32
			Size int64
			Link string
		}{path, kind, uint32(info.Mode().Perm()), size, link}))
		if kind == "symlink" {
			return nil
		}
		if info.IsDir() {
			entries, e := os.ReadDir(path)
			if e != nil {
				return fail("IMAGE_ASSET_READ_FAILED")
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, entry := range entries {
				if e = walk(filepath.Join(path, entry.Name()), depth+1); e != nil {
					return e
				}
			}
			after, e := os.Lstat(path)
			if e != nil || !os.SameFile(info, after) || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) {
				return fail("IMAGE_ASSET_CHANGED")
			}
			return nil
		}
		if st.Nlink != 1 || info.Size() < 0 || info.Size() > p.MaximumTreeBytes-bytes {
			return fail("IMAGE_ASSET_LIMIT")
		}
		files++
		if files > p.MaximumTreeFiles {
			return fail("IMAGE_ASSET_LIMIT")
		}
		f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return fail("IMAGE_ASSET_READ_FAILED")
		}
		opened, e := f.Stat()
		if e != nil || !os.SameFile(info, opened) {
			f.Close()
			return fail("IMAGE_ASSET_CHANGED")
		}
		n, e := io.Copy(h, io.LimitReader(contextReader{ctx, f}, info.Size()+1))
		after, ae := f.Stat()
		current, ce := os.Lstat(path)
		closeError := f.Close()
		if e != nil || ae != nil || ce != nil || closeError != nil || n != info.Size() || !os.SameFile(info, current) || after.Mode() != info.Mode() || current.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) || !current.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() || current.Size() != info.Size() {
			return fail("IMAGE_ASSET_CHANGED")
		}
		bytes += n
		return nil
	}
	if e := walk(path, 0); e != nil {
		return "", e
	}
	return hashString(h), nil
}

// Validate the selected owners without starting any provider or service. Nested
// protected owner references must remain in the same copied projection.
func projectionOptionalOwners(c config.Config, r OperatorRequest) error {
	materialOwners := []any{c}
	check := func(value any, name string) error { return projectionOwnerReferences(value, name, r) }
	if c.PrivateClusterConfigFile != "" {
		v, e := privateclusters.Load(c.PrivateClusterConfigFile)
		if e != nil {
			return fail("PRIVATE_CLUSTER_CLOSURE_INVALID")
		}
		if e = check(v, "private_cluster"); e != nil {
			return e
		}
		materialOwners = append(materialOwners, v)
	}
	if c.MonitoringConfigFile != "" {
		v, e := monitoring.Load(c.MonitoringConfigFile)
		if e != nil {
			return fail("MONITORING_CLOSURE_INVALID")
		}
		if e = check(v, "monitoring"); e != nil {
			return e
		}
		materialOwners = append(materialOwners, v)
	}
	if c.MiddlewareConfigFile != "" {
		v, e := middleware.LoadFiles(c.MiddlewareConfigFile, c.MiddlewareEnvironmentFile)
		if e != nil {
			return fail("MIDDLEWARE_CLOSURE_INVALID")
		}
		if e = check(v, "middleware"); e != nil {
			return e
		}
		for _, profile := range v.Profiles {
			if profile.MySQLConfigurationFile != "" {
				if _, e := projectionNestedMySQL(profile.MySQLConfigurationFile, profile.MySQLEnvironmentFile, r); e != nil {
					return e
				}
			}
		}
		materialOwners = append(materialOwners, v)
	}
	if c.ReleaseViewConfigFile != "" {
		v, e := releases.Load(c.ReleaseViewConfigFile)
		if e != nil {
			return fail("RELEASE_VIEW_CLOSURE_INVALID")
		}
		if e = check(v, "release_view"); e != nil {
			return e
		}
		materialOwners = append(materialOwners, v)
	}
	if c.Licensing != nil {
		if _, e := c.LicenseTrust(); e != nil {
			return fail("LICENSE_TRUST_CLOSURE_INVALID")
		}
	}
	// All configured trust bundles and client pairs are parsed before SQL. No
	// network connection is made, and no verification bypass is introduced.
	var visit func(reflect.Value, string) error
	visit = func(v reflect.Value, name string) error {
		if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return nil
			}
			return visit(v.Elem(), name)
		}
		switch v.Kind() {
		case reflect.Struct:
			cert, key := "", ""
			for j := 0; j < v.NumField(); j++ {
				f := v.Type().Field(j)
				k := strings.Split(f.Tag.Get("yaml"), ",")[0]
				if k == "" || k == "-" {
					k = strings.Split(f.Tag.Get("json"), ",")[0]
				}
				if k == "" || k == "-" {
					k = f.Name
				}
				field := v.Field(j)
				if field.Kind() == reflect.String {
					path := field.String()
					if path != "" && k == "ca_file" {
						raw, e := readPrivate(path, 1<<20)
						if e != nil || !x509.NewCertPool().AppendCertsFromPEM(raw) {
							return fail("TRUST_BUNDLE_CLOSURE_INVALID")
						}
					}
					if k == "client_certificate_file" {
						cert = path
					}
					if k == "client_key_file" {
						key = path
					}
					if path != "" && (k == "password_file" || k == "uri_file" || k == "credentials_file" || k == "credential_file" || k == "access_key_file" || k == "secret_key_file" || k == "refusal_access_key_file" || k == "refusal_secret_key_file" || k == "publisher_seed_file" || k == "consumer_seed_file") {
						raw, e := readPrivate(path, 65536)
						if e != nil || len(raw) == 0 {
							return fail("SECRET_CLOSURE_INVALID")
						}
						clear(raw)
					}
				}
				if e := visit(field, name+"."+k); e != nil {
					return e
				}
			}
			if cert != "" || key != "" {
				a, e := readPrivate(cert, 1<<20)
				if e != nil {
					return fail("CLIENT_CERTIFICATE_CLOSURE_INVALID")
				}
				b, e := readPrivate(key, 1<<20)
				if e != nil {
					return fail("CLIENT_CERTIFICATE_CLOSURE_INVALID")
				}
				_, e = tls.X509KeyPair(a, b)
				clear(b)
				if e != nil {
					return fail("CLIENT_CERTIFICATE_CLOSURE_INVALID")
				}
			}
		case reflect.Slice, reflect.Array:
			for j := 0; j < v.Len(); j++ {
				if e := visit(v.Index(j), name); e != nil {
					return e
				}
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				if e := visit(v.MapIndex(k), name); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for _, owner := range materialOwners {
		if e := visit(reflect.ValueOf(owner), ""); e != nil {
			return e
		}
	}
	return nil
}

// The typed decoder resolves three separately owned files. Admit their original
// references and explicit override file before that decoder can read them.
func admitApplicationResolution(raw []byte, r OperatorRequest) error {
	var document struct {
		Registry  string                         `yaml:"image_registry_config_file"`
		Mongo     string                         `yaml:"mongodb_config_file"`
		Licensing *config.LicensingConfiguration `yaml:"licensing"`
	}
	if yaml.Unmarshal(raw, &document) != nil {
		return fail("APPLICATION_CONFIGURATION_INVALID")
	}
	for _, path := range []string{document.Registry, document.Mongo} {
		if path != "" && (!canonical(path) || !withinRoot(r.ConfigurationRoot, path)) {
			return fail("APPLICATION_RESOLVER_OUTSIDE_CLOSURE")
		}
	}
	if document.Licensing != nil && document.Licensing.EnvironmentFile != "" && !withinRoot(r.ConfigurationRoot, document.Licensing.EnvironmentFile) {
		return fail("APPLICATION_RESOLVER_OUTSIDE_CLOSURE")
	}
	if r.ApplicationEnvironmentFile != "" {
		raw, e := readPrivate(r.ApplicationEnvironmentFile, 256<<10)
		if e != nil {
			return fail("APPLICATION_ENVIRONMENT_INVALID")
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				return fail("APPLICATION_ENVIRONMENT_INVALID")
			}
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if strings.HasPrefix(value, "\"") {
				if json.Unmarshal([]byte(value), &value) != nil {
					return fail("APPLICATION_ENVIRONMENT_INVALID")
				}
			} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
				value = value[1 : len(value)-1]
			}
			if key == "PROJECT_ONE_REGISTRY_CONFIG_FILE" || key == "PROJECT_ONE_MONGODB_CONFIG_FILE" {
				if !canonical(value) || !withinRoot(r.ConfigurationRoot, value) {
					return fail("APPLICATION_RESOLVER_OUTSIDE_CLOSURE")
				}
			}
		}
	}
	return nil
}

func copyConfigurationProjection(ctx context.Context, source, destination string, p SnapshotPolicy, minimum uint64) (postgresbackup.TreeReadback, error) {
	before, e := postgresbackup.TreeDigest(ctx, source, p.MaximumTreeBytes, p.MaximumTreeFiles, p.MaximumTreeMetadataBytes)
	if e != nil {
		return before, fail("CONFIGURATION_SOURCE_INVALID")
	}
	if e = requireSpace(filepath.Dir(destination), before.Bytes+p.MaximumTreeMetadataBytes, minimum); e != nil {
		return before, e
	}
	input, e := openPrivateRoot(source)
	if e != nil {
		return before, e
	}
	defer input.Close()
	output, e := newRoot(destination)
	if e != nil {
		return before, e
	}
	defer output.Close()
	var walk func(string) error
	walk = func(name string) error {
		if ctx.Err() != nil {
			return fail("CONFIGURATION_COPY_INTERRUPTED")
		}
		info, e := input.Lstat(name)
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return fail("CONFIGURATION_SOURCE_CHANGED")
		}
		if info.IsDir() {
			if name != "." {
				if info.Mode().Perm() != 0700 {
					return fail("CONFIGURATION_DIRECTORY_PRIVATE_REQUIRED")
				}
				if e = output.Mkdir(name, 0700); e != nil {
					return fail("CONFIGURATION_COPY_FAILED")
				}
			}
			f, e := input.Open(name)
			if e != nil {
				return fail("CONFIGURATION_COPY_FAILED")
			}
			entries, e := f.ReadDir(-1)
			ce := f.Close()
			if e != nil || ce != nil {
				return fail("CONFIGURATION_COPY_FAILED")
			}
			for _, entry := range entries {
				if e = walk(filepath.Join(name, entry.Name())); e != nil {
					return e
				}
			}
			f, e = output.Open(name)
			if e != nil {
				return fail("CONFIGURATION_COPY_FAILED")
			}
			se := f.Sync()
			ce = f.Close()
			if se != nil || ce != nil {
				return fail("CONFIGURATION_COPY_UNCERTAIN")
			}
			return nil
		}
		if !owned(info, false, false) {
			return fail("CONFIGURATION_SOURCE_CHANGED")
		}
		in, e := input.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return fail("CONFIGURATION_COPY_FAILED")
		}
		out, e := output.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			in.Close()
			return fail("CONFIGURATION_COPY_FAILED")
		}
		n, e := io.Copy(projectionWriter{ctx, out, filepath.Dir(destination), minimum}, io.LimitReader(contextReader{ctx, in}, info.Size()+1))
		after, ae := in.Stat()
		current, ce := input.Lstat(name)
		modeError := out.Chmod(info.Mode().Perm())
		syncError := out.Sync()
		ic, oc := in.Close(), out.Close()
		if e != nil || ae != nil || ce != nil || modeError != nil || syncError != nil || ic != nil || oc != nil || n != info.Size() || !os.SameFile(info, after) || !os.SameFile(info, current) || after.ModTime() != info.ModTime() || after.Size() != info.Size() {
			return fail("CONFIGURATION_COPY_UNCERTAIN")
		}
		return nil
	}
	if e = walk("."); e != nil {
		return before, e
	}
	after, e := postgresbackup.TreeDigest(ctx, source, p.MaximumTreeBytes, p.MaximumTreeFiles, p.MaximumTreeMetadataBytes)
	if e != nil || after != before {
		return before, fail("CONFIGURATION_SOURCE_CHANGED")
	}
	copied, e := postgresbackup.TreeDigest(ctx, destination, p.MaximumTreeBytes, p.MaximumTreeFiles, p.MaximumTreeMetadataBytes)
	if e != nil || copied != before {
		return before, fail("CONFIGURATION_COPY_CHANGED")
	}
	return copied, nil
}

type projectionWriter struct {
	ctx     context.Context
	file    *os.File
	parent  string
	minimum uint64
}

func (w projectionWriter) Write(p []byte) (int, error) {
	if w.ctx.Err() != nil {
		return 0, fail("CONFIGURATION_COPY_INTERRUPTED")
	}
	if e := requireSpace(w.parent, int64(len(p)), w.minimum); e != nil {
		return 0, e
	}
	return w.file.Write(p)
}

// This closed role inventory follows the selected current typed owners. It is
// deliberately not derived from arbitrary document keys or filename suffixes.
// Other state owners and not-yet-composed nested authorities refuse explicitly.
var projectionReferenceRoles = map[string]string{
	"models.artifact_root": "store", "datasets.artifact_root": "store", "training.artifact_root": "store", "model_preparation.artifact_root": "store",
	"knowledge.scratch_root": "temporary", "object_storage.staging_root": "temporary",
	"release_view_config_file": "config", "private_cluster_config_file": "config", "monitoring_config_file": "config", "middleware_config_file": "config", "middleware_environment_file": "config", "image_registry_config_file": "config", "mongodb_config_file": "config",
	"database.dameng.runtime_configuration_file": "config", "database.dameng.schema_configuration_file": "config", "database.dsn_file": "secret",
	"security.auth_hmac_key_file": "secret", "security.mail_keyring_file": "secret", "security.password_blocklist_file": "asset",
	"smtp.ca_file": "ca", "smtp.password_file": "secret", "server.tls_certificate_file": "certificate", "server.tls_private_key_file": "secret", "server.portal_assets": "directory", "server.admin_assets": "directory",
	"machine_server.tls_certificate_file": "certificate", "machine_server.tls_private_key_file": "secret",
	"localization.package_directory": "directory", "licensing.trust_file": "config", "licensing.environment_file": "config",
	"sso.keyring_file": "secret", "sso.providers.ca_file": "ca", "knowledge.gateways.ca_file": "ca", "knowledge.python_executable": "executable", "knowledge.worker_file": "asset", "knowledge.model_directory": "directory",
	"datasets.python_executable": "executable", "datasets.processor_file": "asset", "components.node_exporter_archive_file": "asset", "components.node_exporter_catalog_directory": "directory",
	"model_cache.ca_file": "ca", "model_cache.password_file": "secret", "aws.credentials_file": "secret",
	"task_events.ca_file": "ca", "task_events.client_certificate_file": "certificate", "task_events.client_key_file": "secret", "task_events.publisher_seed_file": "secret", "task_events.consumer_seed_file": "secret",
	"object_storage.refusal_access_key_file": "secret", "object_storage.refusal_secret_key_file": "secret", "object_storage.access_key_file": "secret", "object_storage.secret_key_file": "secret", "object_storage.ca_file": "ca",
	"object_storage.management.ca_file": "ca", "object_storage.management.client_certificate_file": "certificate", "object_storage.management.client_key_file": "secret",
	"MongoDB.sources.uri_file": "secret", "MongoDB.sources.ca_file": "ca",
	"ImageRegistry.ca_file": "ca", "ImageRegistry.credential_file": "secret", "ImageRegistry.verification.trust_file": "config", "ImageRegistry.store_root": "external_state", "ImageRegistry.prepared_release_root": "external_state", "ImageRegistry.verification.mount_root": "external_state",
	"private_cluster.profiles.ca_file": "ca", "private_cluster.profiles.token_file": "secret", "private_cluster.profiles.image_proof_file": "config", "private_cluster.verification.trust_file": "config", "private_cluster.verification.mount_root": "external_state",
	"middleware.profiles.mysql_configuration_file": "config", "middleware.profiles.mysql_environment_file": "config", "middleware.profiles.password_file": "secret", "middleware.profiles.ca_file": "ca", "middleware.profiles.artifact_root": "external_state",
	"middleware.profiles.operator_configuration_file": "nested_unimplemented", "middleware.profiles.backup_request_file": "nested_unimplemented", "middleware.profiles.restore_targets.operator_configuration_file": "nested_unimplemented",
	"release_view.directory": "external_state", "nested_mysql.password_file": "secret", "nested_mysql.ca_file": "ca",
}

func projectionTypedMaterial(path, role string) error {
	if role == "directory" {
		root, e := openPrivateRoot(path)
		if e != nil {
			return fail("CONFIGURATION_DIRECTORY_INVALID")
		}
		return root.Close()
	}
	if role == "secret" || role == "config" || role == "ca" {
		raw, e := readPrivate(path, maximumDocumentBytes)
		if e != nil {
			return fail("CONFIGURATION_PRIVATE_MATERIAL_INVALID")
		}
		defer clear(raw)
		if role == "ca" && !x509.NewCertPool().AppendCertsFromPEM(raw) {
			return fail("TRUST_BUNDLE_CLOSURE_INVALID")
		}
		return nil
	}
	if noSymlinks(path) != nil {
		return fail("CONFIGURATION_ASSET_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil || !owned(info, false, false) || info.Size() < 1 || role == "executable" && info.Mode().Perm()&0111 == 0 {
		return fail("CONFIGURATION_ASSET_INVALID")
	}
	return nil
}

// Decode is the existing selected MySQL config/env authority. It resolves no
// socket, pool or provider; only its named protected material references follow.
func projectionNestedMySQL(path, environment string, r OperatorRequest) (mysqlstore.Configuration, error) {
	empty := mysqlstore.Configuration{}
	for _, ref := range []string{path, environment} {
		if ref == "" {
			continue
		}
		if !canonical(ref) || !withinRoot(r.ConfigurationRoot, ref) {
			return empty, fail("NESTED_CONFIGURATION_OUTSIDE_CLOSURE")
		}
	}
	raw, e := readPrivate(path, mysqlstore.MaximumConfigurationBytes)
	if e != nil {
		return empty, fail("NESTED_CONFIGURATION_INVALID")
	}
	var overrides []byte
	if environment != "" {
		overrides, e = readPrivate(environment, mysqlstore.MaximumConfigurationBytes)
		if e != nil {
			return empty, fail("NESTED_ENVIRONMENT_INVALID")
		}
	}
	selected, e := mysqlstore.Decode(raw, overrides)
	if e != nil {
		return empty, fail("NESTED_CONFIGURATION_INVALID")
	}
	if e = projectionOwnerReferences(selected, "nested_mysql", r); e != nil {
		return empty, e
	}
	return selected, nil
}
