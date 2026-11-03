package scope

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const generationPayloadDescriptorName = "descriptor.json"
const generationPayloadTrustName = "trust.pem"
const generationPayloadConfiguration = "configuration"

// Only metadata is serialized. Raw credentials remain private regular files.
type generationPayloadDescriptor struct {
	FormatVersion      int         `json:"format_version"`
	ScopeID            string      `json:"scope_id"`
	GenerationID       string      `json:"generation_id"`
	PlanSHA256         string      `json:"plan_sha256"`
	ConfigurationFiles []InputFile `json:"configuration_files"`
	TrustBundle        InputFile   `json:"trust_bundle"`
}

func generationPayloadDirectory(generation string) string {
	return "tls-generation-" + generation + ".payload"
}

func (l *locked) generationPayloadBound(p RestoredTLSPlan) bool {
	if l.root == nil || filepath.Clean(l.root.Name()) != l.config.ScopeRoot || p.FormatVersion != 2 || p.Request.FormatVersion != 2 || p.Request.Validate() != nil || p.ScopeID != l.marker.ScopeID || !identityPattern.MatchString(p.GenerationID) || len(p.ConfigurationFiles) == 0 || len(p.ConfigurationFiles) > l.config.Verification.MaximumAssets || !wire.ValidSHA256(p.Identity.TrustBundleSHA256) || wire.Digest(encode(p.ConfigurationFiles)) != p.PayloadSHA256 {
		return false
	}
	previous := ""
	for _, f := range p.ConfigurationFiles {
		if !wire.SafePath(f.Name) || strings.ContainsAny(f.Name, "/\\") || f.Name <= previous || f.Bytes < 1 || f.Bytes > l.config.Verification.MaximumMetadataBytes || !wire.ValidSHA256(f.SHA256) {
			return false
		}
		previous = f.Name
	}
	hash, found := wire.Digest(encode(p)), false
	for _, event := range l.events {
		if r := event.TLSProjection; r != nil && r.GenerationID == p.GenerationID {
			if !r.PayloadCustody || r.PlanSHA256 != hash {
				return false
			}
			found = true
		}
	}
	return found
}

func (l *locked) generationDescriptorMatches(p RestoredTLSPlan, d generationPayloadDescriptor) bool {
	return d.FormatVersion == 1 && d.ScopeID == p.ScopeID && d.GenerationID == p.GenerationID && d.PlanSHA256 == wire.Digest(encode(p)) && reflect.DeepEqual(d.ConfigurationFiles, p.ConfigurationFiles) && d.TrustBundle.Name == generationPayloadTrustName && d.TrustBundle.Bytes > 0 && d.TrustBundle.Bytes <= l.config.Verification.MaximumMetadataBytes && d.TrustBundle.SHA256 == p.Identity.TrustBundleSHA256
}

func syncPrivateRoot(root *os.Root) error {
	info, e := root.Stat(".")
	named, ne := os.Lstat(filepath.Clean(root.Name()))
	if e != nil || ne != nil || !canonical(filepath.Clean(root.Name())) || !samePrivateDirectory(info, named) {
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CHANGED")
	}
	f, e := root.Open(".")
	if e != nil {
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_DURABILITY_FAILED")
	}
	se, ce := f.Sync(), f.Close()
	if se != nil || ce != nil {
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_DURABILITY_FAILED")
	}
	return nil
}

// All enumeration is relative to a pinned directory descriptor. Opening "."
// cannot block on a named replacement FIFO. No recursive traversal is used.
func privatePayloadNames(root *os.Root, maximum int) ([]string, error) {
	if maximum < 1 {
		return nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_INVENTORY_INVALID")
	}
	f, e := root.Open(".")
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_INVENTORY_INVALID")
	}
	entries, re := f.ReadDir(maximum + 1)
	ce := f.Close()
	if re != nil && re != io.EOF || ce != nil || len(entries) > maximum {
		return nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_INVENTORY_INVALID")
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	slices.Sort(names)
	return names, nil
}

func (l *locked) openGenerationPayload(p RestoredTLSPlan) (*os.Root, error) {
	if !l.generationPayloadBound(p) {
		return nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CONTEXT_INVALID")
	}
	return openPrivateDirectory(filepath.Join(l.config.ScopeRoot, generationPayloadDirectory(p.GenerationID)))
}

func (l *locked) readGenerationPayload(ctx context.Context, p RestoredTLSPlan) (files []engine.ConfigurationFile, trust []byte, err error) {
	if ctx.Err() != nil {
		return nil, nil, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	root, e := l.openGenerationPayload(p)
	if e != nil {
		return nil, nil, e
	}
	defer func() {
		if e := root.Close(); e != nil && err == nil {
			files, trust, err = nil, nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CHANGED")
		}
	}()
	names, e := privatePayloadNames(root, 3)
	if e != nil || !slices.Equal(names, []string{generationPayloadConfiguration, generationPayloadDescriptorName, generationPayloadTrustName}) {
		return nil, nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_INVENTORY_INVALID")
	}
	raw, e := regular(root, generationPayloadDescriptorName, l.config.Verification.MaximumMetadataBytes)
	var d generationPayloadDescriptor
	if e != nil || wire.StrictJSON(raw, &d) != nil || !l.generationDescriptorMatches(p, d) || !bytes.Equal(raw, encode(d)) {
		return nil, nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_DESCRIPTOR_INVALID")
	}
	trust, e = regular(root, generationPayloadTrustName, l.config.Verification.MaximumMetadataBytes)
	if e != nil || int64(len(trust)) != d.TrustBundle.Bytes || wire.Digest(trust) != d.TrustBundle.SHA256 {
		return nil, nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_TRUST_CHANGED")
	}
	configuration, e := openPrivateDirectory(filepath.Join(filepath.Clean(root.Name()), generationPayloadConfiguration))
	if e != nil {
		return nil, nil, e
	}
	defer func() {
		if e := configuration.Close(); e != nil && err == nil {
			files, trust, err = nil, nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CHANGED")
		}
	}()
	names, e = privatePayloadNames(configuration, len(p.ConfigurationFiles))
	if e != nil || len(names) != len(p.ConfigurationFiles) {
		return nil, nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_INVENTORY_INVALID")
	}
	for i, index := range p.ConfigurationFiles {
		if names[i] != index.Name || ctx.Err() != nil {
			return nil, nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_INVENTORY_INVALID")
		}
		raw, e := regular(configuration, index.Name, l.config.Verification.MaximumMetadataBytes)
		if e != nil || int64(len(raw)) != index.Bytes || wire.Digest(raw) != index.SHA256 {
			return nil, nil, failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CONTENT_CHANGED")
		}
		files = append(files, engine.ConfigurationFile{Name: index.Name, Contents: raw})
	}
	return files, trust, nil
}

func (l *locked) syncGenerationPayload(ctx context.Context, p RestoredTLSPlan) error {
	if _, _, e := l.readGenerationPayload(ctx, p); e != nil {
		return e
	}
	root, e := l.openGenerationPayload(p)
	if e != nil {
		return e
	}
	defer root.Close()
	configuration, e := openPrivateDirectory(filepath.Join(filepath.Clean(root.Name()), generationPayloadConfiguration))
	if e != nil {
		return e
	}
	defer configuration.Close()
	for _, index := range p.ConfigurationFiles {
		if e := syncGenerationFile(configuration, index.Name, l.config.Verification.MaximumMetadataBytes); e != nil {
			return e
		}
	}
	for _, name := range []string{generationPayloadDescriptorName, generationPayloadTrustName} {
		if e := syncGenerationFile(root, name, l.config.Verification.MaximumMetadataBytes); e != nil {
			return e
		}
	}
	for _, directory := range []*os.Root{configuration, root, l.root} {
		if e := syncPrivateRoot(directory); e != nil {
			return e
		}
	}
	_, _, e = l.readGenerationPayload(ctx, p)
	return e
}

func syncGenerationFile(root *os.Root, name string, maximum int64) error {
	parent, parents, e := pinPrivateParent(root, name)
	if e != nil {
		return e
	}
	defer parent.Close()
	before, e := parent.Lstat(name)
	if e != nil {
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_DURABILITY_FAILED")
	}
	f, e := openPrivateSnapshot(root, parent, parents, name, before, maximum)
	if e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_DURABILITY_FAILED")
	}
	_, e = readPrivateSnapshot(root, parent, parents, name, before, f, maximum)
	return e
}

func (l *locked) writeGenerationPayload(ctx context.Context, p RestoredTLSPlan, files []engine.ConfigurationFile, trust []byte) error {
	if ctx.Err() != nil {
		return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	state := tlsProjectionState(l.events)
	if state.record == nil || state.record.GenerationID != p.GenerationID || state.record.Step != 0 || state.pending == "" || state.aborting || state.aborted || state.complete {
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_PUBLICATION_INTENT_REQUIRED")
	}
	if !l.generationPayloadBound(p) || len(files) != len(p.ConfigurationFiles) || restoreConfigurationDigest(files) != p.PayloadSHA256 || len(trust) == 0 || int64(len(trust)) > l.config.Verification.MaximumMetadataBytes || wire.Digest(trust) != p.Identity.TrustBundleSHA256 {
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CONTEXT_INVALID")
	}
	total := int64(len(trust))
	for i, index := range p.ConfigurationFiles {
		f := files[i]
		if f.Name != index.Name || int64(len(f.Contents)) != index.Bytes || wire.Digest(f.Contents) != index.SHA256 || index.Bytes > math.MaxInt64-total {
			return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CONTEXT_INVALID")
		}
		total += index.Bytes
	}
	d := generationPayloadDescriptor{FormatVersion: 1, ScopeID: p.ScopeID, GenerationID: p.GenerationID, PlanSHA256: wire.Digest(encode(p)), ConfigurationFiles: p.ConfigurationFiles, TrustBundle: InputFile{Name: generationPayloadTrustName, Bytes: int64(len(trust)), SHA256: p.Identity.TrustBundleSHA256}}
	descriptor := encode(d)
	if int64(len(descriptor)) > l.config.Verification.MaximumMetadataBytes || total > math.MaxInt64/2-int64(len(descriptor)) {
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CONTEXT_INVALID")
	}
	if e := backupDiskAdmission(l.config.ScopeRoot, 2*(total+int64(len(descriptor)))); e != nil {
		return e
	}
	directory := generationPayloadDirectory(p.GenerationID)
	if e := l.root.Mkdir(directory, 0700); e != nil {
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_OCCUPIED")
	}
	root, e := l.openGenerationPayload(p)
	if e != nil {
		return e
	}
	defer root.Close()
	if e = writeNew(root, generationPayloadDescriptorName, descriptor); e != nil {
		return e
	}
	if e = writeNew(root, generationPayloadTrustName, trust); e != nil {
		return e
	}
	if e = root.Mkdir(generationPayloadConfiguration, 0700); e != nil {
		return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_OCCUPIED")
	}
	configuration, e := openPrivateDirectory(filepath.Join(filepath.Clean(root.Name()), generationPayloadConfiguration))
	if e != nil {
		return e
	}
	defer configuration.Close()
	for _, f := range files {
		if ctx.Err() != nil {
			return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		if e = writeNew(configuration, f.Name, f.Contents); e != nil {
			return e
		}
	}
	return l.syncGenerationPayload(ctx, p)
}

type generationPayloadRemoval struct {
	root *os.Root
	name string
	info os.FileInfo
}

// Abort owns an exact immutable generation, never a wildcard directory. Verify
// every present object before removing any; changed/unknown content is retained.
func (l *locked) removeGenerationPayload(ctx context.Context, p RestoredTLSPlan) error {
	bad := func() error { return failure("OFFLINE_SCOPE_GENERATION_PAYLOAD_CLEANUP_UNRESOLVED") }
	if !l.generationPayloadBound(p) || ctx.Err() != nil {
		return bad()
	}
	state := tlsProjectionState(l.events)
	if state.record == nil || state.record.GenerationID != p.GenerationID || !state.aborting {
		return bad()
	}
	for _, event := range l.events {
		if r := event.ControlReplacement; r != nil && r.GenerationID == p.GenerationID {
			return bad()
		}
	}
	directory := generationPayloadDirectory(p.GenerationID)
	if _, e := l.root.Lstat(directory); os.IsNotExist(e) {
		return syncPrivateRoot(l.root)
	}
	root, e := l.openGenerationPayload(p)
	if e != nil {
		return bad()
	}
	defer root.Close()
	parentBefore, e := root.Stat(".")
	if e != nil {
		return bad()
	}
	names, e := privatePayloadNames(root, 3)
	if e != nil {
		return bad()
	}
	present := map[string]bool{}
	for _, name := range names {
		if name != generationPayloadDescriptorName && name != generationPayloadTrustName && name != generationPayloadConfiguration {
			return bad()
		}
		present[name] = true
	}
	removals := []generationPayloadRemoval{}
	verify := func(parent *os.Root, name string, index InputFile) error {
		before, e := parent.Lstat(name)
		if e != nil {
			return bad()
		}
		raw, e := regular(parent, name, l.config.Verification.MaximumMetadataBytes)
		after, ae := parent.Lstat(name)
		if e != nil || ae != nil || !samePrivateSnapshot(before, after, l.config.Verification.MaximumMetadataBytes) || index.Bytes > 0 && int64(len(raw)) != index.Bytes || wire.Digest(raw) != index.SHA256 {
			return bad()
		}
		removals = append(removals, generationPayloadRemoval{parent, name, after})
		return nil
	}
	if present[generationPayloadDescriptorName] {
		raw, e := regular(root, generationPayloadDescriptorName, l.config.Verification.MaximumMetadataBytes)
		var descriptor generationPayloadDescriptor
		if e != nil || wire.StrictJSON(raw, &descriptor) != nil || !bytes.Equal(raw, encode(descriptor)) || !l.generationDescriptorMatches(p, descriptor) {
			return bad()
		}
		if e = verify(root, generationPayloadDescriptorName, InputFile{Bytes: int64(len(raw)), SHA256: wire.Digest(raw)}); e != nil {
			return e
		}
	}
	if present[generationPayloadTrustName] {
		if e = verify(root, generationPayloadTrustName, InputFile{SHA256: p.Identity.TrustBundleSHA256}); e != nil {
			return e
		}
	}
	var configuration *os.Root
	var configurationBefore os.FileInfo
	if present[generationPayloadConfiguration] {
		configuration, e = openPrivateDirectory(filepath.Join(filepath.Clean(root.Name()), generationPayloadConfiguration))
		if e != nil {
			return bad()
		}
		defer configuration.Close()
		configurationBefore, e = configuration.Stat(".")
		if e != nil {
			return bad()
		}
		names, e = privatePayloadNames(configuration, len(p.ConfigurationFiles))
		if e != nil {
			return bad()
		}
		index := map[string]InputFile{}
		for _, file := range p.ConfigurationFiles {
			index[file.Name] = file
		}
		for _, name := range names {
			file, found := index[name]
			if !found {
				return bad()
			}
			if e = verify(configuration, name, file); e != nil {
				return e
			}
		}
	}
	for _, item := range removals {
		if ctx.Err() != nil {
			return bad()
		}
		current, e := item.root.Lstat(item.name)
		if e != nil || !samePrivateSnapshot(item.info, current, l.config.Verification.MaximumMetadataBytes) || syncPrivateRoot(item.root) != nil {
			return bad()
		}
		if e = item.root.Remove(item.name); e != nil {
			return bad()
		}
	}
	if configuration != nil {
		if syncPrivateRoot(configuration) != nil {
			return bad()
		}
		named, e := root.Lstat(generationPayloadConfiguration)
		if e != nil || !samePrivateDirectory(configurationBefore, named) {
			return bad()
		}
		if e = root.Remove(generationPayloadConfiguration); e != nil {
			return bad()
		}
	}
	if syncPrivateRoot(root) != nil {
		return bad()
	}
	named, e := l.root.Lstat(directory)
	if e != nil || !samePrivateDirectory(parentBefore, named) {
		return bad()
	}
	if e = l.root.Remove(directory); e != nil {
		return bad()
	}
	if syncPrivateRoot(l.root) != nil {
		return bad()
	}
	if _, e = l.root.Lstat(directory); !os.IsNotExist(e) {
		return bad()
	}
	return nil
}
