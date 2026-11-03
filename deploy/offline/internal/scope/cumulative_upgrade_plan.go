package scope

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

const cumulativeUpgradePlanFile = "cumulative-upgrade.json"

type CumulativeUpgradePlan struct {
	RegistryReleaseProfile        *engine.PreparedReleaseProfile         `json:"registry_release_profile,omitempty"`
	RegistryReleaseMetadataSHA256 string                                 `json:"registry_release_metadata_sha256,omitempty"`
	RegistryReleaseMountTarget    string                                 `json:"registry_release_mount_target,omitempty"`
	RegistryMountTarget           string                                 `json:"registry_mount_target,omitempty"`
	MVPConfigurationSHA256        string                                 `json:"mvp_configuration_sha256,omitempty"`
	PostgresPID                   int                                    `json:"postgres_pid"`
	PostgresStartedAt             string                                 `json:"postgres_started_at"`
	FormatVersion                 int                                    `json:"format_version"`
	ScopeID                       string                                 `json:"scope_id"`
	GenerationID                  string                                 `json:"generation_id"`
	Request                       configuration.CumulativeUpgradeRequest `json:"request"`
	Target                        wire.Manifest                          `json:"target"`
	ControlImage                  ImageRecord                            `json:"control_image"`
	RuntimeSHA256                 string                                 `json:"runtime_sha256"`
	ModelSHA256                   string                                 `json:"model_sha256"`
	BackupProofSHA256             string                                 `json:"backup_proof_sha256"`
	PreviousControlJSON           string                                 `json:"previous_control_json"`
	Before                        compose.RuntimeStatus                  `json:"before"`
	ControlFiles                  []InputFile                            `json:"control_files"`
	OperatorFiles                 []InputFile                            `json:"operator_files"`
	UID                           int                                    `json:"uid"`
	GID                           int                                    `json:"gid"`
}

func cumulativeUpgradePrefix(events []Event) []Event {
	for i, e := range events {
		if e.CumulativeUpgrade != nil {
			return events[:i]
		}
	}
	return events
}
func (l *locked) cumulativeOriginal() *locked {
	v := *l
	v.events = cumulativeUpgradePrefix(l.events)
	return &v
}
func cumulativeIndex(files []engine.ConfigurationFile) []InputFile {
	out := make([]InputFile, 0, len(files))
	for _, f := range files {
		out = append(out, InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func cumulativeProfile(raw []byte, policyReference string) ([]byte, error) {
	var values map[string]json.RawMessage
	if wire.StrictJSON(raw, &values) != nil {
		return nil, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_JSON_CONFIGURATION_REQUIRED")
	}
	var server map[string]json.RawMessage
	var old string
	if wire.StrictJSON(values["server"], &server) != nil || json.Unmarshal(server["application_profile"], &old) != nil || old != "localization" {
		return nil, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_M16_CONFIGURATION_REQUIRED")
	}
	if v, ok := values["release_view_config_file"]; ok {
		var s string
		if json.Unmarshal(v, &s) != nil || s != "" {
			return nil, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_POLICY_CONFLICT")
		}
	}
	server["application_profile"] = json.RawMessage(`"releases"`)
	values["server"], _ = json.Marshal(server)
	values["release_view_config_file"], _ = json.Marshal(policyReference)
	return encode(values), nil
}

// The source M17 view binding remains the original scope's authority. Only the
// application selection changes; DSNs, origins, key references, false switches
// and all other values are preserved through the existing protected payload.
func cumulativeMVPProfile(raw []byte, policyReference string) ([]byte, error) {
	bad := failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_M17_CONFIGURATION_REQUIRED")
	var values map[string]json.RawMessage
	var server map[string]json.RawMessage
	var application, reference string
	if wire.StrictJSON(raw, &values) != nil || wire.StrictJSON(values["server"], &server) != nil || json.Unmarshal(server["application_profile"], &application) != nil || application != "releases" || json.Unmarshal(values["release_view_config_file"], &reference) != nil || reference == "" || reference != policyReference {
		return nil, bad
	}
	server["application_profile"], _ = json.Marshal(wire.MVPPostgreSQLCPUApplicationProfile)
	values["server"], _ = json.Marshal(server)
	return encode(values), nil
}

func cumulativeMVPPolicyName(files []engine.ConfigurationFile, role string, expected view.Configuration) (string, error) {
	names := []string{"runtime.yaml"}
	if role == "operator-config" {
		names = append(names, "control.yaml")
	}
	selected := ""
	for _, config := range names {
		name, policy, err := releasePolicyPayloadForProfile(files, config, wire.CumulativeCPUProfile)
		if err != nil || selected != "" && name != selected || policy.ScopeID != expected.ScopeID || policy.ManifestSHA256 != expected.ManifestSHA256 || policy.ConfigurationSHA256 != expected.ConfigurationSHA256 || policy.Directory != expected.Directory || policy.PublisherUID != expected.PublisherUID || policy.PageSize != expected.PageSize || policy.OperationSeconds != expected.OperationSeconds {
			return "", failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_POLICY_CONFLICT")
		}
		selected = name
	}
	return selected, nil
}
func (l *locked) cumulativePayloads(ctx context.Context, p CumulativeUpgradePlan) (map[string][]engine.ConfigurationFile, compose.Model, error) {
	original := l.cumulativeOriginal()
	input, outcome, _ := inputsState(original.events)
	if input == nil || outcome != "completed" {
		return nil, compose.Model{}, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_INPUT_REQUIRED")
	}
	source, e := original.readInputs(ctx, *input)
	if e != nil {
		return nil, compose.Model{}, e
	}
	base, e := compose.DecodeModel(source.model, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return nil, base, e
	}
	next, e := compose.CumulativeControlModel(base, p.GenerationID, p.ControlImage.ManifestDigest, l.config.ScopeRoot)
	if p.Target.Profile == wire.MVPPostgreSQLCPUProfile {
		next, e = compose.MVPControlModel(base, p.GenerationID, p.ControlImage.ManifestDigest, l.config.ScopeRoot)
	}
	if e != nil {
		return nil, next, e
	}
	target := ""
	for _, m := range base.Services["control"].Mounts {
		if m.Source == "control-config" {
			target = m.Target
		}
	}
	if target == "" {
		return nil, next, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CONTROL_MOUNT_REQUIRED")
	}
	if !reflect.DeepEqual(base.Services["control"].Command, []string{"serve", "--config", filepath.ToSlash(filepath.Join(target, "private", "runtime.yaml"))}) {
		return nil, next, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_FIXED_RUNTIME_COMMAND_REQUIRED")
	}
	policy := view.Configuration{Directory: view.ContainerDirectory, ScopeID: l.marker.ScopeID, ManifestSHA256: l.marker.ManifestSHA256, ConfigurationSHA256: l.marker.ConfigurationSHA256, PublisherUID: l.marker.OwnerUID, PageSize: p.Request.PageSize, OperationSeconds: p.Request.OperationSeconds}
	policyRaw := encode(policy)
	if _, e = view.ParseConfiguration(policyRaw); e != nil {
		return nil, next, e
	}
	if p.Target.Profile == wire.MVPPostgreSQLCPUProfile {
		next, e = compose.MVPRegistryModel(next, p.RegistryMountTarget)
		if e != nil {
			return nil, next, e
		}
		next, e = compose.MVPRegistryReleaseModel(next, p.RegistryReleaseMountTarget)
		if e != nil {
			return nil, next, e
		}
	}
	extras, e := l.cumulativeMVPFiles(p)
	if e != nil {
		return nil, next, e
	}
	if !cumulativeMVPSecretIsolation(extras, source.files) {
		return nil, next, failure("OFFLINE_SCOPE_MVP_OPERATOR_SECRET_EXPOSURE_REFUSED")
	}
	files := map[string][]engine.ConfigurationFile{}
	for _, role := range []string{"control-config", "operator-config"} {
		root := target
		if role == "operator-config" {
			root = engine.DatabaseTaskDestination
		}
		policyName := "admin-view.json"
		mvp := p.Target.Profile == wire.MVPPostgreSQLCPUProfile
		if mvp {
			policyName, e = cumulativeMVPPolicyName(source.files[role], role, policy)
			if e != nil {
				return nil, next, e
			}
		}
		policyFound := false
		for _, f := range source.files[role] {
			if mvp && role == "operator-config" && f.Name == configuration.NativePostgreSQLRecoveryDescriptorFile {
				replacement := false
				for _, extra := range extras[role] {
					if extra.Name == f.Name {
						replacement = true
					}
				}
				if replacement {
					continue
				}
			}
			v := engine.ConfigurationFile{Name: f.Name, Contents: append([]byte{}, f.Contents...)}
			if f.Name == policyName {
				if !mvp || policyFound {
					return nil, next, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_POLICY_FILENAME_CONFLICT")
				}
				policyFound = true // retain the actual M17 policy bytes
			}
			if f.Name == "runtime.yaml" || role == "operator-config" && f.Name == "control.yaml" {
				reference := filepath.ToSlash(filepath.Join(root, "private", policyName))
				if mvp {
					v.Contents, e = cumulativeMVPProfile(f.Contents, reference)
				} else {
					v.Contents, e = cumulativeProfile(f.Contents, reference)
				}
				if e != nil {
					return nil, next, e
				}
			}
			if mvp && (f.Name == "runtime.yaml" || role == "operator-config" && f.Name == "control.yaml") {
				v.Contents, e = cumulativeMVPCapabilities(v.Contents, extras[role], source.files[role], root, next)
				if e != nil {
					return nil, next, e
				}
			}
			files[role] = append(files[role], v)
		}
		if mvp {
			for _, extra := range extras[role] {
				for _, original := range source.files[role] {
					if extra.Name == original.Name && !(role == "operator-config" && extra.Name == configuration.NativePostgreSQLRecoveryDescriptorFile) {
						return nil, next, failure("OFFLINE_SCOPE_MVP_CONFIGURATION_COLLISION")
					}
				}
				files[role] = append(files[role], extra)
			}
			if !policyFound {
				return nil, next, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ORIGINAL_POLICY_CONFLICT")
			}
		} else {
			files[role] = append(files[role], engine.ConfigurationFile{Name: "admin-view.json", Contents: policyRaw})
		}
	}
	if _, e = restoreNamedFile(files["control-config"], "runtime.yaml"); e != nil {
		return nil, next, e
	}
	for _, name := range []string{"control.yaml", "runtime.yaml"} {
		if _, e = restoreNamedFile(files["operator-config"], name); e != nil {
			return nil, next, e
		}
	}
	if p.Target.Profile == wire.MVPPostgreSQLCPUProfile {
		if e := validateMVPRegistryReleaseBinding(files, next, p.Request.ArchiveSHA256, p.Request.ManifestSHA256); e != nil {
			return nil, next, e
		}
		sha, e := l.registryReleaseMetadata(ctx, p.Request.PreparedRoot, p.Request.ArchiveSHA256, p.Request.ManifestSHA256)
		if e != nil || sha != p.RegistryReleaseMetadataSHA256 || p.RegistryReleaseProfile == nil || wire.Digest(encode(*p.RegistryReleaseProfile)) != sha {
			return nil, next, failure("OFFLINE_SCOPE_MVP_REGISTRY_RELEASE_CHANGED")
		}
	}
	runtimeRaw, err := restoreNamedFile(files["control-config"], "runtime.yaml")
	var serving struct {
		Server struct {
			ListenAddress string `json:"listen_address"`
			Transport     string `json:"transport_mode"`
		} `json:"server"`
	}
	if err != nil || wire.UniqueJSON(runtimeRaw, &serving) != nil {
		return nil, next, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_READINESS_CONFIGURATION_INVALID")
	}
	_, port, err := net.SplitHostPort(serving.Server.ListenAddress)
	dial, de := netip.ParseAddrPort(p.Request.ReadinessDialAddress)
	if err != nil || de != nil || port != strconv.Itoa(int(dial.Port())) || serving.Server.Transport != "" && serving.Server.Transport != "http" && serving.Server.Transport != "direct_https" || (serving.Server.Transport == "http") != (p.Request.ReadinessCAName == "") {
		return nil, next, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_READINESS_CONFIGURATION_INVALID")
	}
	if p.Request.ReadinessCAName != "" {
		if _, e = restoreNamedFile(files["control-config"], p.Request.ReadinessCAName); e != nil {
			return nil, next, e
		}
	}
	return files, next, nil
}
func (l *locked) validateCumulativeUpgradePlan(ctx context.Context, p CumulativeUpgradePlan) error {
	bad := func() error { return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_PLAN_CHANGED") }
	original := l.cumulativeOriginal()
	b := backupState(original.events)
	runtime := runtimeState(original.events)
	started, err := time.Parse(time.RFC3339Nano, p.PostgresStartedAt)
	if err != nil || started.IsZero() || p.PostgresPID < 1 {
		return bad()
	}
	v := l.config.Verification
	if s := cumulativeUpgradeState(l.events); s.record != nil && s.record.TargetProfile != cumulativeRecordProfile(p.Target.Profile) {
		return bad()
	}
	if p.FormatVersion != 1 || p.ScopeID != l.marker.ScopeID || !identityPattern.MatchString(p.GenerationID) || p.Request.Validate() != nil || !cumulativeProfileTransition(l.marker.Profile, p.Target.Profile) || p.Target.Validate(v.MaximumAssets, v.MaximumAssetBytes, v.MaximumTotalBytes, v.AllowTestFixture) != nil || p.Target.ReleaseID == l.marker.ReleaseID || p.Request.ManifestSHA256 == l.marker.ManifestSHA256 || !validImage(&p.ControlImage) || p.ControlImage.AssetID != "control-image" || p.UID < 1 || p.GID < 1 || p.UID == l.marker.OwnerUID || b.record == nil || !b.terminal || b.aborted || b.record.Phase != "complete" || p.BackupProofSHA256 != wire.Digest(encode(*b.record)) || p.Request.BackupSource.ManifestSHA256 != b.record.ArtifactSHA256 || p.Request.BackupSource.BackupRoot != b.record.DestinationRoot || runtime.record == nil || !runtime.completed || p.RuntimeSHA256 != wire.Digest(encode(*runtime.record)) || p.Before.Status != "observed_complete_compose_resources" || !runtimeIDsMatch(*runtime.record, p.Before) || controlReplacementState(original.events).record != nil || restoreState(original.events).reserved || tlsProjectionState(original.events).record != nil {
		return bad()
	}
	foundControl := false
	for _, a := range p.Target.Assets {
		if a.ID == "control-image" {
			foundControl = a.Kind == "image_archive" && a.Path == p.ControlImage.Path && a.SHA256 == p.ControlImage.SourceSHA256 && a.Size == p.ControlImage.SourceBytes && a.ImageID == p.ControlImage.ManifestDigest && a.ImageConfigDigest == p.ControlImage.ConfigDigest
		}
	}
	if !foundControl || p.Request.PreparedRoot == p.Request.BackupSource.BackupRoot || contained(p.Request.PreparedRoot, p.Request.BackupSource.BackupRoot) || contained(p.Request.BackupSource.BackupRoot, p.Request.PreparedRoot) {
		return bad()
	}
	for role, s := range p.Before.Services {
		if s.ExitCode != 0 || role == "postgres" && s.State != "running" || role != "postgres" && s.State != "created" && s.State != "exited" {
			return bad()
		}
	}
	var previous engine.RuntimeContainer
	if wire.UniqueJSON([]byte(p.PreviousControlJSON), &previous) != nil || previous.ID != p.Before.Services["control"].ID || previous.Config.User != strings.Join([]string{fmtInt(p.UID), fmtInt(p.GID)}, ":") || previous.State.Running || previous.State.ExitCode != 0 {
		return bad()
	}
	raw, _ := json.Marshal(previous)
	if string(raw) != p.PreviousControlJSON {
		return bad()
	}
	files, model, e := l.cumulativePayloads(ctx, p)
	if e != nil || wire.Digest(encode(model)) != p.ModelSHA256 || !reflect.DeepEqual(cumulativeIndex(files["control-config"]), p.ControlFiles) || !reflect.DeepEqual(cumulativeIndex(files["operator-config"]), p.OperatorFiles) {
		return bad()
	}
	return nil
}
func fmtInt(n int) string { return strconv.Itoa(n) }
func (l *locked) readCumulativeUpgradePlan() (CumulativeUpgradePlan, error) {
	var p CumulativeUpgradePlan
	s := cumulativeUpgradeState(l.events)
	if s.record == nil {
		return p, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_NOT_RECORDED")
	}
	raw, e := regular(l.root, cumulativeUpgradePlanFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != s.record.PlanSHA256 || wire.StrictJSON(raw, &p) != nil || p.GenerationID != s.record.GenerationID {
		return p, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_PLAN_UNAVAILABLE")
	}
	return p, l.validateCumulativeUpgradePlan(context.Background(), p)
}
func (l *locked) cumulativeTarget(ctx context.Context, r configuration.CumulativeUpgradeRequest) (bundle.Result, error) {
	if !contained(l.config.Verification.MountRoot, r.PreparedRoot) || !privateDirectory(r.PreparedRoot) || r.PreparedRoot == l.config.ScopeRoot || contained(l.config.ScopeRoot, r.PreparedRoot) || contained(r.PreparedRoot, l.config.ScopeRoot) {
		return bundle.Result{}, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_SEPARATE_PREPARED_ROOT_REQUIRED")
	}
	target, e := bundle.VerifyPrepared(ctx, l.config.Verification, r.PreparedRoot, r.ArchiveSHA256, r.ManifestSHA256)
	if e != nil {
		return target, e
	}
	if !cumulativeProfileTransition(l.marker.Profile, target.Profile) || target.Manifest.Cumulative == nil {
		if l.marker.Profile == wire.CumulativeCPUProfile {
			return target, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_MVP_TARGET_REQUIRED")
		}
		return target, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_M17_TARGET_REQUIRED")
	}
	return target, nil
}
func (l *locked) cumulativeImage(ctx context.Context, target wire.Manifest, root string) (ImageRecord, error) {
	for _, a := range target.Assets {
		if a.ID != "control-image" {
			continue
		}
		source, e := bundle.OpenImageSource(ctx, l.config.Verification, filepath.Join(root, filepath.FromSlash(a.Path)), a, l.config.Engine.MaximumExpandedImageBytes)
		if e != nil {
			return ImageRecord{}, e
		}
		inventory := source.Inventory()
		receipt, e := source.WriteTagless(ctx, io.Discard)
		ce := source.Close()
		if e != nil || ce != nil {
			return ImageRecord{}, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_IMAGE_UNAVAILABLE")
		}
		r := ImageRecord{AssetID: a.ID, Path: a.Path, SourceSHA256: a.SHA256, SourceBytes: a.Size, ManifestDigest: inventory.ManifestDigest, ConfigDigest: inventory.ConfigDigest, DiffIDs: append([]string{}, inventory.DiffIDs...), ExpandedBytes: inventory.ExpandedBytes, TransportSHA256: receipt.TransportSHA256, TransportBytes: receipt.TransportBytes, Mode: "load"}
		if !validImage(&r) {
			return r, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_IMAGE_INVALID")
		}
		return r, nil
	}
	return ImageRecord{}, failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_CONTROL_IMAGE_REQUIRED")
}

func (l *locked) verifyCumulativeUpgradePlan() error {
	s := cumulativeUpgradeState(l.events)
	if s.record == nil {
		return nil
	}
	p, e := l.readCumulativeUpgradePlan()
	if e != nil {
		if len(s.completed) == 0 && s.record.Step == 0 {
			if _, err := l.root.Lstat(cumulativeUpgradePlanFile); os.IsNotExist(err) {
				return nil
			}
		}
		return e
	}
	if s.activated {
		i := s.record.Installed
		if i == nil || i.ReleaseID != p.Target.ReleaseID || i.Profile != p.Target.Profile || i.ArchiveSHA256 != p.Request.ArchiveSHA256 || i.ManifestSHA256 != p.Request.ManifestSHA256 || i.SourceCommit != p.Target.Cumulative.SourceCommit || i.SchemaVersion != p.Target.SchemaVersion || i.ActivationSequence != s.activationSequence {
			return failure("OFFLINE_SCOPE_CUMULATIVE_UPGRADE_ACTIVATION_CHANGED")
		}
	}
	return nil
}
