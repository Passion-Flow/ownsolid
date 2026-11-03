package release

import (
	"errors"
	"regexp"
	"strings"
)

const CumulativeCPUProfile = "m17-postgresql-cpu-linux-amd64"
const CumulativePredecessorCPUProfile = "m16-postgresql-cpu-linux-amd64"

func IsCumulativeCPUProfile(profile string) bool {
	return profile == CumulativeCPUProfile || profile == CumulativePredecessorCPUProfile || profile == CumulativeKubernetesCPUProfile || profile == CumulativeCacheCPUProfile || profile == MVPPostgreSQLCPUProfile
}

type CumulativeSelection struct {
	ApplicationProfile string            `json:"application_profile"`
	SchemaLineage      string            `json:"schema_lineage"`
	SourceCommit       string            `json:"source_commit"`
	ComponentVersions  map[string]string `json:"component_versions"`
	LocaleSourceSHA256 string            `json:"locale_source_sha256"`
	InstalledLocales   []string          `json:"installed_static_locales"`
	LocaleTarget       int               `json:"locale_target"`
	PendingLocales     int               `json:"pending_current_locales"`
	HumanApprovals     int               `json:"native_human_approvals"`
	EmbeddedSeed       string            `json:"embedded_seed"`
	LocaleInventory    string            `json:"locale_inventory_asset_id"`
}

func cumulativeRequiredAssets() map[string]string {
	return map[string]string{
		"control-image": "image_archive", "gateway-image": "image_archive", "agent-image": "image_archive",
		"training-image": "image_archive", "preparation-image": "image_archive", "postgres-image": "image_archive",
		"compose": "compose", "operator": "operator", "factory": "operator",
		"runtime-config-schema": "config_schema", "configuration-example": "config_example", "locales": "locale",
		"base-model": "model", "embedding-model": "model", "dataset-inputs": "runtime_inputs",
		"training-inputs": "runtime_inputs", "preparation-inputs": "runtime_inputs", "builtin-plugin": "plugin",
		"offline-dependency-lock": "dependency_lock", "offline-dependencies": "dependency_lock",
		"source-evidence": "source_evidence", "operator-runbook": "runbook", "platform-license": "license",
		LicenseTrustAssetID: LicenseTrustAssetKind, LicenseValidationAssetID: "operator",
	}
}

func (m Manifest) validateCumulative() error {
	bad := errors.New("RELEASE_CUMULATIVE_SELECTION_INVALID")
	if !IsCumulativeCPUProfile(m.Profile) {
		if m.Cumulative != nil {
			return bad
		}
		return nil
	}
	c := m.Cumulative
	application, maximumCapability, capabilityCount := "releases", "M17", 17
	minimumPredecessor, maximumPredecessor, schemaVersion, lineage := 22, 22, 22, "account22"
	if m.Profile == CumulativePredecessorCPUProfile {
		application, maximumCapability, capabilityCount = "localization", "M16", 16
	}
	if m.Profile == CumulativeKubernetesCPUProfile {
		application, maximumCapability, capabilityCount = "localization", "M18", 18
		minimumPredecessor = 21
	}
	if m.Profile == CumulativeCacheCPUProfile {
		application, maximumCapability, capabilityCount = CumulativeCacheApplicationProfile, ModelCacheCapability, 18
	}
	if m.Profile == MVPPostgreSQLCPUProfile {
		application, maximumCapability, capabilityCount = MVPPostgreSQLCPUApplicationProfile, "M30", 30
		maximumPredecessor, schemaVersion, lineage = MVPPostgreSQLCPUV1SchemaVersion, MVPPostgreSQLCPUV1SchemaVersion, MVPPostgreSQLCPUApplicationProfile
	}
	if c == nil || c.ApplicationProfile != application || c.SchemaLineage != lineage || m.SchemaVersion != schemaVersion || m.MinimumPredecessor != minimumPredecessor || m.MaximumPredecessor != maximumPredecessor || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(c.SourceCommit) || !ValidSHA256(c.LocaleSourceSHA256) || c.EmbeddedSeed != "zh-CN" || c.LocaleInventory != "locales" {
		return bad
	}
	owners := []string{"control", "gateway", "agent", "python", "postgres", "pgvector", "training", "preparation", "knowledge", "compose", "operator"}
	if m.Profile == MVPPostgreSQLCPUProfile {
		owners = append(owners, "inference", "kubernetes", "native-postgres", "private-cluster-probe")
	}
	if len(c.ComponentVersions) != len(owners) {
		return bad
	}
	for _, owner := range owners {
		v := c.ComponentVersions[owner]
		if !identifier.MatchString(v) {
			return bad
		}
	}
	if c.ComponentVersions["control"] != c.SourceCommit || c.ComponentVersions["postgres"] != "18.6" || c.ComponentVersions["pgvector"] != "0.8.7" || c.ComponentVersions["compose"] != "5.5.1" || len(c.InstalledLocales) < 1 || c.InstalledLocales[0] == "" || c.LocaleTarget != 202 || c.PendingLocales != c.LocaleTarget-len(c.InstalledLocales) || c.PendingLocales < 0 || c.HumanApprovals < 0 || c.HumanApprovals > len(c.InstalledLocales) {
		return bad
	}
	seenEnglish := false
	for i, locale := range c.InstalledLocales {
		if !regexp.MustCompile(`^[a-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`).MatchString(locale) || i > 0 && c.InstalledLocales[i-1] >= locale {
			return bad
		}
		seenEnglish = seenEnglish || locale == "en"
	}
	if !seenEnglish || len(m.Capabilities) != capabilityCount {
		return bad
	}
	for _, capability := range m.Capabilities {
		if capability > maximumCapability || m.Profile == CumulativeCacheCPUProfile && capability > "M17" && capability != ModelCacheCapability {
			return bad
		}
	}
	for _, asset := range m.Assets {
		if m.Profile == MVPPostgreSQLCPUProfile && asset.ID == MVPPostgreSQLCPULineageAssetID && asset.SHA256 != MVPPostgreSQLCPULineageSHA256() {
			return bad
		}
		if asset.ID == "source-evidence" && asset.SHA256 != m.SourceSHA256 {
			return bad
		}
		if asset.Kind == "image_archive" && (m.Profile == MVPPostgreSQLCPUProfile && !validImageArchitecture(asset) || m.Profile != MVPPostgreSQLCPUProfile && asset.Architecture != "amd64") {
			return bad
		}
		if strings.Contains(strings.ToLower(asset.ID), "private-key") {
			return bad
		}
	}
	if m.Profile == MVPPostgreSQLCPUProfile && (c.ComponentVersions["inference"] != "0.31.0" || c.ComponentVersions["native-postgres"] != "18.6") {
		return bad
	}
	return nil
}
