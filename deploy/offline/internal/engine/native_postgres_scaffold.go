package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
)

const nativePostgreSQLScaffoldQualification = "empty_directory_namespace_only_not_durability_prepared_release_CAS_SQL_or_target"

type nativePostgreSQLScaffoldPlan struct {
	owner           Ownership
	directories     []string
	maximumEntries  int
	maximumMetadata int64
}

// The caller cannot supply a directory list. Every leaf and ancestor comes from
// an actual mount of the already frozen, original fixed task profile.
func nativePostgreSQLScaffoldContext(t DatabaseTask) (DatabaseTask, nativePostgreSQLScaffoldPlan, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_CONTEXT_INVALID")
	var p nativePostgreSQLScaffoldPlan
	var scaffold *NativePostgreSQLVerifierPlaceholder
	var mounts []databaseMountProfile
	switch {
	case t.NativePostgreSQLRecovery != nil:
		t = t.freezeNativeRecovery()
		if !t.validNativePostgreSQLRecovery() || t.NativePostgreSQLRecovery.Scaffold == nil {
			return t, p, bad
		}
		scaffold = t.NativePostgreSQLRecovery.Scaffold
		mounts = t.nativeRecoveryExtraMounts()
		policy := t.nativeRecoveryPolicy()
		p.maximumEntries, p.maximumMetadata = int(policy.MaximumManifestBytes/tarBlockBytes), policy.MaximumManifestBytes
	case t.NativePostgreSQLBackupVerifier != nil:
		t = t.freezeNativeVerifier()
		if !t.validNativePostgreSQLVerifier() || t.NativePostgreSQLBackupVerifier.Placeholder == nil {
			return t, p, bad
		}
		scaffold = t.NativePostgreSQLBackupVerifier.Placeholder
		mounts = t.nativeVerifierExtraMounts()
		policy := t.NativePostgreSQLBackupVerifier.Request.Policy
		p.maximumEntries, p.maximumMetadata = int(policy.MaximumManifestBytes/tarBlockBytes), policy.MaximumManifestBytes
	default:
		return t, p, bad
	}
	p.owner = scaffold.Owner
	directories := map[string]bool{UtilityPrivateDirectory: true}
	for _, mount := range mounts {
		if mount.owner == scaffold.Owner || !strings.HasPrefix(mount.target, scaffold.Target+"/") {
			continue
		}
		relative := strings.TrimPrefix(mount.target, scaffold.Target+"/")
		if relative == "" || relative == "." || path.Clean(relative) != relative || strings.HasPrefix(relative, "/") || strings.ContainsAny(relative, "\\\x00\r\n") {
			return t, p, bad
		}
		for _, part := range strings.Split(relative, "/") {
			if part == "" || part == "." || part == ".." {
				return t, p, bad
			}
		}
		for name := path.Join(UtilityPrivateDirectory, relative); name != "."; name = path.Dir(name) {
			directories[name] = true
		}
	}
	// Keep native metadata/entry bounds: each accepted header consumes at
	// least one tar block from MaximumManifestBytes. No store payload is added.
	var metadata int64
	for name := range directories {
		metadata += int64(len(name)) + tarBlockBytes
		if len(directories)+1 > p.maximumEntries || metadata > p.maximumMetadata {
			return t, p, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_ARCHIVE_LIMIT")
		}
		p.directories = append(p.directories, name)
	}
	sort.Strings(p.directories)
	return t, p, nil
}

func (c *Client) nativePostgreSQLScaffoldArchive(t DatabaseTask, p nativePostgreSQLScaffoldPlan, initializer string) ([]byte, error) {
	if c == nil || !utilityIdentity(p.owner, initializer, t.UID, t.GID) {
		return nil, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_CONTEXT_INVALID")
	}
	maximum := c.config.ResponseBytes - archiveReadbackReserveBytes
	if native := p.maximumMetadata - tarBlockBytes; native < maximum {
		maximum = native // the GET adds one daemon-owned wrapper directory
	}
	if maximum < tarEndBytes || int64(len(p.directories)) > (maximum-tarEndBytes)/tarBlockBytes {
		return nil, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_ARCHIVE_LIMIT")
	}
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	for _, name := range p.directories {
		if e := writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0700, Uid: t.UID, Gid: t.GID, Format: tar.FormatUSTAR}); e != nil {
			return nil, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_ARCHIVE_INVALID")
		}
	}
	if writer.Close() != nil || int64(raw.Len()) > maximum {
		return nil, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_ARCHIVE_LIMIT")
	}
	return raw.Bytes(), nil
}

func inspectNativePostgreSQLScaffoldArchive(raw []byte, t DatabaseTask, p nativePostgreSQLScaffoldPlan) (ConfigurationObservation, error) {
	bad := failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_INVENTORY_MISMATCH")
	if int64(len(raw)) > p.maximumMetadata || len(raw) < 3*tarBlockBytes || len(raw)%tarBlockBytes != 0 || len(bytes.Trim(raw[len(raw)-tarEndBytes:], "\x00")) != 0 {
		return ConfigurationObservation{}, bad
	}
	root := path.Base(UtilityDestination)
	expected := map[string]bool{root: true}
	for _, name := range p.directories {
		expected[root+"/"+name] = true
	}
	seen := map[string]bool{}
	source := bytes.NewReader(raw)
	reader := tar.NewReader(source)
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return ConfigurationObservation{}, bad
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !expected[name] || seen[name] || len(seen) >= p.maximumEntries || h.Typeflag != tar.TypeDir || h.Size != 0 || h.Linkname != "" || len(h.Xattrs) != 0 {
			return ConfigurationObservation{}, bad
		}
		seen[name] = true
		if name == root {
			// This nonsecret wrapper belongs to the daemon, as in the original
			// private-configuration archive protocol.
			if h.Mode != 0755 || h.Uid != 0 || h.Gid != 0 {
				return ConfigurationObservation{}, bad
			}
		} else if h.Mode != 0700 || h.Uid != t.UID || h.Gid != t.GID {
			return ConfigurationObservation{}, bad
		}
	}
	if !seen[root] || len(bytes.Trim(raw[len(raw)-source.Len():], "\x00")) != 0 {
		return ConfigurationObservation{}, bad
	}
	if len(seen) == 1 {
		return ConfigurationObservation{Status: "observed_empty_owned_volume", Qualification: nativePostgreSQLScaffoldQualification}, nil
	}
	if len(seen) != len(expected) {
		return ConfigurationObservation{}, bad
	}
	return ConfigurationObservation{Status: ConfigurationPayloadVerified, Qualification: nativePostgreSQLScaffoldQualification}, nil
}

// InspectNativePostgreSQLScaffold only GETs the full original volume archive.
// It starts no container and never repairs a missing/partial namespace.
func (c *Client) InspectNativePostgreSQLScaffold(ctx context.Context, t DatabaseTask, initializer string) (ConfigurationObservation, error) {
	if c == nil || ctx == nil {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_CONTEXT_INVALID")
	}
	if e := ctx.Err(); e != nil {
		return ConfigurationObservation{}, e
	}
	frozen, p, e := nativePostgreSQLScaffoldContext(t)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	if _, e = c.nativePostgreSQLScaffoldArchive(frozen, p, initializer); e != nil {
		return ConfigurationObservation{}, e
	}
	if e = c.utilityPolicy(); e != nil {
		return ConfigurationObservation{}, e
	}
	return c.inspectNativePostgreSQLScaffold(ctx, frozen, p, initializer)
}

func (c *Client) inspectNativePostgreSQLScaffold(ctx context.Context, t DatabaseTask, p nativePostgreSQLScaffoldPlan, initializer string) (ConfigurationObservation, error) {
	utility, e := c.configurationUtility(ctx, p.owner, initializer, t.UID, t.GID)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+utility.ID+"/archive", url.Values{"path": {UtilityDestination}}, nil, c.config.RequestSeconds)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	if status != http.StatusOK {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_READBACK_FAILED")
	}
	result, e := inspectNativePostgreSQLScaffoldArchive(raw, t, p)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	after, e := c.configurationUtility(ctx, p.owner, initializer, t.UID, t.GID)
	if e != nil || after.ID != utility.ID {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_UTILITY_CHANGED")
	}
	return result, nil
}

// PutNativePostgreSQLScaffold submits at most one directory-only PUT, exclusively
// into an observed empty owned volume. Scope must persist its original intent
// before calling; an uncertain result permits Inspect only, never automatic PUT
// replay. This proves neither fsync, st_dev, prepared inventory nor CAS content.
func (c *Client) PutNativePostgreSQLScaffold(ctx context.Context, t DatabaseTask, initializer string) (ConfigurationObservation, error) {
	if c == nil || ctx == nil {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_CONTEXT_INVALID")
	}
	if e := ctx.Err(); e != nil {
		return ConfigurationObservation{}, e
	}
	frozen, p, e := nativePostgreSQLScaffoldContext(t)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	body, e := c.nativePostgreSQLScaffoldArchive(frozen, p, initializer)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	if e = c.utilityPolicy(); e != nil {
		return ConfigurationObservation{}, e
	}
	state, e := c.inspectNativePostgreSQLScaffold(ctx, frozen, p, initializer)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	if state.Status != "observed_empty_owned_volume" {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_NONEMPTY_PRESERVED")
	}
	utility, e := c.configurationUtility(ctx, p.owner, initializer, frozen.UID, frozen.GID)
	if e != nil {
		return ConfigurationObservation{}, e
	}
	_, status, e := c.request(ctx, http.MethodPut, "/v"+c.policy.APIVersion+"/containers/"+utility.ID+"/archive", url.Values{"path": {UtilityDestination}, "copyUIDGID": {"true"}, "noOverwriteDirNonDir": {"true"}}, bytes.NewReader(body), c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_WRITE_UNCERTAIN")
	}
	result, e := c.inspectNativePostgreSQLScaffold(ctx, frozen, p, initializer)
	if e != nil || result.Status != ConfigurationPayloadVerified {
		return ConfigurationObservation{}, failure("OFFLINE_ENGINE_NATIVE_SCAFFOLD_WRITE_UNCERTAIN")
	}
	return result, nil
}
