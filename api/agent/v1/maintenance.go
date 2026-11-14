package v1

import (
	_ "embed"
	"encoding/json"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"
)

//go:embed catalog.json
var approvedCatalog []byte

// Recipe exposes only immutable runtime integrity metadata, never a download URL.
type Recipe struct {
 ListenAddress string `json:"listen_address"`
	Version       string `json:"version"`
	ArchiveSHA256 string `json:"archive_sha256"`
	ArchiveBytes  int64  `json:"archive_size_bytes"`
	UpgradeOrder  int    `json:"upgrade_order"`
}

func RecipeForVersion(version string) (Recipe, bool) {
	var catalog struct {
		DefaultVersion string   `json:"default_version"`
		Recipes        []Recipe `json:"recipes"`
	}
	if json.Unmarshal(approvedCatalog, &catalog) != nil || catalog.DefaultVersion != NodeExporterVersion {
		return Recipe{}, false
	}
	for _, recipe := range catalog.Recipes {
		if recipe.Version == version {
			return recipe, true
		}
	}
	return Recipe{}, false
}
func IsForwardUpgrade(current, target string) bool {
	from, a := RecipeForVersion(current)
	to, b := RecipeForVersion(target)
	return a && b && to.UpgradeOrder > from.UpgradeOrder
}

type ComponentConfig struct {
	LogLevel          string `json:"log_level"`
	MaxScrapeRequests int    `json:"max_scrape_requests"`
}

// MaxComponentScrapeRequests is the approved v1 component recipe resource ceiling.
const MaxComponentScrapeRequests = 40

func DefaultComponentConfig() ComponentConfig {
	return ComponentConfig{LogLevel: "info", MaxScrapeRequests: MaxComponentScrapeRequests}
}
func (c ComponentConfig) Validate() error {
	if (c.LogLevel != "debug" && c.LogLevel != "info" && c.LogLevel != "warn" && c.LogLevel != "error") || c.MaxScrapeRequests < 1 || c.MaxScrapeRequests > MaxComponentScrapeRequests {
		return errors.New("COMPONENT_CONFIG_INVALID")
	}
	return nil
}

const SystemdMaintenanceProfile = "systemd_maintenance"
const MaintenanceVersion = "1.2"

var maintenanceActions = map[string]bool{"install": true, "start": true, "stop": true, "remove": true, "observe": true, "configure": true, "restart": true, "upgrade": true, "rollback": true, "logs": true}
var maintenanceErrors = map[string]bool{"COMPONENT_INTERRUPTED": true, "COMPONENT_CONFIG_INVALID": true, "COMPONENT_VERSION_UNSUPPORTED": true, "COMPONENT_VERSION_CONFLICT": true, "COMPONENT_UPGRADE_NOT_FORWARD": true, "COMPONENT_ROLLBACK_UNAVAILABLE": true, "COMPONENT_ROLLBACK_FAILED": true, "COMPONENT_UPDATE_ROLLED_BACK": true, "COMPONENT_LOGS_UNAVAILABLE": true, "COMPONENT_LOGS_TOO_LARGE": true, "COMPONENT_LOGS_INVALID": true, "COMPONENT_MAINTENANCE_REQUIRED": true}

func KnownMaintenanceError(value string) bool {
	return ComponentErrors[value] || maintenanceErrors[value]
}
func (c ComponentCommand) ValidateMaintenance(operation string) error {
	r, ok := RecipeForVersion(c.Version)
	if !ok || c.Component != NodeExporter || r.ArchiveSHA256 != c.ArtifactSHA256 || r.ArchiveBytes != c.ArchiveBytes {
		return errors.New("COMPONENT_VERSION_UNSUPPORTED")
	}
	if !maintenanceActions[c.Action] || ComponentOperation(c.Action) != operation {
		return errors.New("AGENT_OPERATION_UNSUPPORTED")
	}
	if c.Config != nil {
		if e := c.Config.Validate(); e != nil {
			return e
		}
	}
	if c.ExpectedVersion != "" {
		if _, ok = RecipeForVersion(c.ExpectedVersion); !ok {
			return errors.New("COMPONENT_VERSION_UNSUPPORTED")
		}
	}
	if c.Action == "configure" && (c.Config == nil || c.ExpectedVersion == "" || c.ExpectedVersion != c.Version) {
		return errors.New("COMPONENT_CONFIG_INVALID")
	}
	if c.Action == "upgrade" && !IsForwardUpgrade(c.ExpectedVersion, c.Version) {
		return errors.New("COMPONENT_UPGRADE_NOT_FORWARD")
	}
	if c.Action == "rollback" && c.ExpectedVersion == "" {
		return errors.New("COMPONENT_ROLLBACK_UNAVAILABLE")
	}
	return nil
}
func MaintenanceCommand(action, version, expected string, config *ComponentConfig) (ComponentCommand, error) {
	r, ok := RecipeForVersion(version)
	if !ok {
		return ComponentCommand{}, errors.New("COMPONENT_VERSION_UNSUPPORTED")
	}
	c := ComponentCommand{Component: NodeExporter, Version: version, Action: action, ArtifactSHA256: r.ArchiveSHA256, ArchiveBytes: r.ArchiveBytes, Config: config, ExpectedVersion: expected}
	return c, c.ValidateMaintenance(ComponentOperation(action))
}
func (o ComponentObservation) ValidateMaintenance() error {
	if _, ok := RecipeForVersion(o.Version); !ok || o.Component != NodeExporter || o.ObservedAt.IsZero() || o.ObservedAt.Year() < 2020 || o.ObservedAt.Year() > 2200 || !KnownMaintenanceError(o.ErrorCode) || !KnownMaintenanceError(o.MaintenanceError) || !KnownMaintenanceError(o.MaintenanceCause) {
		return errors.New("VALIDATION_FAILED")
	}
	if o.Managed && (o.State == "running" || o.State == "stopped") && o.Version != NodeExporterVersion && o.Config == nil {
		return errors.New("VALIDATION_FAILED")
	}
	if o.Config != nil {
		if e := o.Config.Validate(); e != nil {
			return e
		}
	}
	if o.PreviousVersion != "" {
		if _, ok := RecipeForVersion(o.PreviousVersion); !ok {
			return errors.New("VALIDATION_FAILED")
		}
	}
	return o.validateState()
}

type ComponentLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Priority  int       `json:"priority"`
	Message   string    `json:"message"`
	Sanitized bool      `json:"sanitized"`
}
type ComponentLogs struct {
	Source     string              `json:"source"`
	Available  bool                `json:"available"`
	ObservedAt time.Time           `json:"observed_at"`
	Since      time.Time           `json:"since"`
	Truncated  bool                `json:"truncated"`
	Entries    []ComponentLogEntry `json:"entries"`
}

func (l ComponentLogs) Validate() error {
	validTime := func(t time.Time) bool { return !t.IsZero() && t.Year() >= 2020 && t.Year() <= 2200 }
	if l.Source != "systemd_journal" || !l.Available || !validTime(l.ObservedAt) || !validTime(l.Since) || l.Entries == nil || len(l.Entries) > 8 {
		return errors.New("VALIDATION_FAILED")
	}
	for _, entry := range l.Entries {
		for _, r := range entry.Message {
			if unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
				return errors.New("VALIDATION_FAILED")
			}
		}
		if !validTime(entry.Timestamp) || entry.Priority < 0 || entry.Priority > 7 || !utf8.ValidString(entry.Message) || len(entry.Message) > 512 {
			return errors.New("VALIDATION_FAILED")
		}
	}
	if encoded, e := json.Marshal(l); e != nil || len(encoded) > 12288 {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
