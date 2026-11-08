// Package apps reads versioned application definitions from local catalog bundles.
package apps

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
)

const (
	documentBytes = 64 << 10
	maxImages     = 128
	maxFields     = 32
	maxChoices    = 32
)

var (
	// ErrManifest identifies an invalid or unsupported application manifest.
	ErrManifest = errors.New("CATALOG_MANIFEST_INVALID")
	// ErrLocale identifies incomplete or invalid local translations.
	ErrLocale = errors.New("CATALOG_LOCALE_INVALID")
	// ErrValues identifies invalid configuration supplied for a release.
	ErrValues = errors.New("CATALOG_VALUES_INVALID")
	// ErrPlatformVersion identifies an invalid installed platform version.
	ErrPlatformVersion = errors.New("CATALOG_PLATFORM_VERSION_INVALID")

	identifier = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
	digest     = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	triplet    = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)
)

// Artifact identifies the local engine input and the digest of its bytes.
type Artifact struct {
	Engine string `json:"engine"`
	SHA256 string `json:"sha256"`
}

// Filename returns the bundle's fixed artifact name, or an empty string for an unknown engine.
func (a Artifact) Filename() string {
	switch a.Engine {
	case "helm":
		return "chart.tgz"
	case "compose":
		return "compose.yaml"
	default:
		return ""
	}
}

// PlatformRange describes an inclusive lower and exclusive upper stable release bound.
type PlatformRange struct {
	Minimum string `json:"minimum"`
	Before  string `json:"before"`
}

// Field describes one configuration value. Its identifier also keys the locale documents.
type Field struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Required *bool    `json:"required"`
	Choices  []string `json:"choices,omitempty"`
}

// Manifest describes one app version independently of the workload engine.
type Manifest struct {
	SchemaVersion int           `json:"schema_version"`
	ID            string        `json:"id"`
	Version       string        `json:"version"`
	Platform      PlatformRange `json:"platform"`
	Artifact      Artifact      `json:"artifact"`
	Images        []string      `json:"images"`
	Form          []Field       `json:"form"`
}

// Release holds a validated manifest and its two local message catalogs.
type Release struct {
	Manifest          Manifest
	English           Messages
	SimplifiedChinese Messages
}

// Load reads one release from a local, immutable bundle directory. Callers must
// verify the bundle's provenance and confine their root to that release directory.
// The workload artifact and images are verified and admitted separately.
func Load(root *os.Root) (Release, error) {
	if root == nil {
		return Release{}, fmt.Errorf("%w: open a release directory", ErrManifest)
	}
	var release Release
	if err := readDocument(root, "manifest.json", &release.Manifest, ErrManifest); err != nil {
		return Release{}, err
	}
	if err := release.Manifest.validate(); err != nil {
		return Release{}, err
	}
	for _, locale := range []struct {
		file     string
		messages *Messages
	}{
		{"locales/en.json", &release.English},
		{"locales/zh-cn.json", &release.SimplifiedChinese},
	} {
		if err := readDocument(root, locale.file, locale.messages, ErrLocale); err != nil {
			return Release{}, err
		}
		if !locale.messages.valid(release.Manifest.Form) {
			return Release{}, fmt.Errorf("%w: fill the title, description and exact form keys in %s", ErrLocale, locale.file)
		}
	}
	return release, nil
}

func readDocument(root *os.Root, name string, dest any, invalid error) error {
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > documentBytes {
		return fmt.Errorf("%w: provide a regular JSON file of at most %d bytes at %s", invalid, documentBytes, name)
	}
	file, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("%w: cannot open %s", invalid, name)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, documentBytes+1))
	if err != nil || len(raw) > documentBytes || json.Unmarshal(raw, dest, json.RejectUnknownMembers(true)) != nil {
		return fmt.Errorf("%w: provide one valid JSON object at %s", invalid, name)
	}
	return nil
}

func validID(s string) bool { return len(s) <= 63 && identifier.MatchString(s) }

func (m Manifest) validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("%w: use schema_version 1", ErrManifest)
	}
	if !validID(m.ID) || !triplet.MatchString(m.Version) {
		return fmt.Errorf("%w: use a lowercase app identifier and a stable major.minor.patch version", ErrManifest)
	}
	if _, _, err := m.Platform.bounds(); err != nil {
		return err
	}
	if m.Artifact.Filename() == "" || !digest.MatchString(m.Artifact.SHA256) {
		return fmt.Errorf("%w: identify a Helm chart or Compose file by its SHA-256 digest", ErrManifest)
	}
	if len(m.Images) == 0 || len(m.Images) > maxImages || !distinct(m.Images, digest.MatchString) {
		return fmt.Errorf("%w: list 1 to %d distinct image SHA-256 digests", ErrManifest, maxImages)
	}
	if m.Form == nil || len(m.Form) > maxFields {
		return fmt.Errorf("%w: provide a form array with at most %d fields", ErrManifest, maxFields)
	}
	seen := make(map[string]bool, len(m.Form))
	for _, field := range m.Form {
		if !validID(field.ID) || seen[field.ID] {
			return fmt.Errorf("%w: use distinct lowercase form field identifiers", ErrManifest)
		}
		seen[field.ID] = true
		if !field.valid() {
			return fmt.Errorf("%w: specify a field type, required flag and any permitted choices for %s", ErrManifest, field.ID)
		}
	}
	return nil
}

func distinct(values []string, valid func(string) bool) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !valid(value) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func (f Field) valid() bool {
	if f.Required == nil {
		return false
	}
	switch f.Type {
	case "text", "integer", "boolean":
		return f.Choices == nil
	case "choice":
		return len(f.Choices) > 0 && len(f.Choices) <= maxChoices && distinct(f.Choices, validID)
	default:
		return false
	}
}

type version [3]int

func parseVersion(s string) (version, bool) {
	parts := triplet.FindStringSubmatch(s)
	if parts == nil {
		return version{}, false
	}
	var result version
	for i := range result {
		// The matched components fit in nine decimal digits.
		result[i], _ = strconv.Atoi(parts[i+1])
	}
	return result, true
}

func (r PlatformRange) bounds() (version, version, error) {
	minimum, minOK := parseVersion(r.Minimum)
	before, maxOK := parseVersion(r.Before)
	if !minOK || !maxOK || slices.Compare(minimum[:], before[:]) >= 0 {
		return version{}, version{}, fmt.Errorf("%w: set platform minimum below before using stable versions", ErrManifest)
	}
	return minimum, before, nil
}

// Supports reports whether platformVersion is within the declared stable release range.
func (r PlatformRange) Supports(platformVersion string) (bool, error) {
	minimum, before, err := r.bounds()
	if err != nil {
		return false, err
	}
	platform, ok := parseVersion(platformVersion)
	if !ok {
		return false, fmt.Errorf("%w: use a stable major.minor.patch version", ErrPlatformVersion)
	}
	return slices.Compare(minimum[:], platform[:]) <= 0 && slices.Compare(platform[:], before[:]) < 0, nil
}
