package localization

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/text/language"
	"io"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"regexp"
	"sort"
	"sync"
	"syscall"
)

//go:embed locales/seed_resources.json
var seedResources []byte

//go:embed font_assets.json
var fontAssets []byte
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type blob struct {
	bytes     []byte
	mediaType string
}
type Registry struct {
	source   string
	policy   config.LocalizationPolicy
	locales  map[string]*LocalePackage
	aliases  map[string]string
	packages map[string]string
	blobs    map[string]blob
}

var defaultOnce sync.Once
var defaultRegistry *Registry
var defaultError error

func unavailable() error     { return errors.New("LOCALIZATION_PACKAGE_INVALID") }
func hash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func CanonicalLocale(value string) bool {
	if len(value) < 2 || len(value) > 35 {
		return false
	}
	tag, e := language.Parse(value)
	return e == nil && tag.String() == value
}
func Default() (*Registry, error) {
	defaultOnce.Do(func() {
		defaultRegistry, defaultError = New(config.LocalizationConfiguration{}, config.DefaultLocalizationPolicy())
	})
	return defaultRegistry, defaultError
}
func New(configuration config.LocalizationConfiguration, policy config.LocalizationPolicy) (*Registry, error) {
	if configuration.Validate() != nil || policy.Validate() != nil {
		return nil, unavailable()
	}
	var seeds struct {
		SchemaVersion int                      `json:"schema_version"`
		SourceSHA256  string                   `json:"source_sha256"`
		Locales       map[string]LocalePackage `json:"locales"`
	}
	if strict.DecodeStrict(seedResources, &seeds) != nil || seeds.SchemaVersion != 1 || !digestPattern.MatchString(seeds.SourceSHA256) || len(seeds.Locales) != 2 {
		return nil, unavailable()
	}
	r := &Registry{source: seeds.SourceSHA256, policy: policy, locales: map[string]*LocalePackage{}, aliases: map[string]string{}, packages: map[string]string{}, blobs: map[string]blob{}}
	for _, locale := range []string{"en", "zh-CN"} {
		value, ok := seeds.Locales[locale]
		if !ok {
			return nil, unavailable()
		}
		raw, e := json.Marshal(value)
		if e != nil {
			return nil, unavailable()
		}
		if e = r.add(value, raw, true); e != nil {
			return nil, e
		}
	}
	if configuration.PackageDirectory != "" {
		if e := r.load(configuration.PackageDirectory); e != nil {
			return nil, e
		}
	}
	for _, locale := range configuration.RequiredLocales {
		if _, ok := r.aliases[locale]; !ok {
			return nil, errors.New("LOCALIZATION_REQUIRED_LOCALE_UNAVAILABLE")
		}
	}
	return r, nil
}
func (r *Registry) SourceSHA256() string { return r.source }
func (r *Registry) Resolve(locale string) (string, bool) {
	value, ok := r.aliases[locale]
	return value, ok
}

// Catalog returns detached metadata; callers cannot mutate the shared registry.
func (r *Registry) Catalog() []Choice {
	values := make([]Choice, 0, len(r.locales))
	for locale, p := range r.locales {
		value := Choice{Locale: locale, DisplayName: p.DisplayName, EnglishName: p.EnglishName, Aliases: append([]string{}, p.Aliases...), Direction: p.Direction, PackageSHA256: r.packages[locale], PackageURL: "/api/v1/localization/packages/" + r.packages[locale], TranslationState: p.Translation.State, ReviewState: p.Translation.Review["state"], GoogleTranslatedComplete: p.GoogleTranslatedComplete, Fonts: append([]Font{}, p.Fonts...), Formatting: p.Formatting}
		value.Formatting.PluralCategories = append([]string{}, p.Formatting.PluralCategories...)
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Locale < values[j].Locale })
	return values
}
func (r *Registry) MailTemplates() map[string]MailTemplate {
	out := map[string]MailTemplate{}
	for locale, p := range r.locales {
		for purpose, t := range p.Mail {
			out[locale+"/"+purpose] = t
		}
	}
	return out
}

// MailPresentation exposes detached, verified display resources for offline MIME.
// Missing stored languages recover through the canonical English template.
type MailPresentation struct {
	Locale                   string
	Direction                string
	TranslationLabel         string
	TranslationLink          string
	TranslationDocumentation string
	TranslationBadge         []byte
}

func (r *Registry) MailPresentation(locale string) (MailPresentation, error) {
	canonical, ok := r.Resolve(locale)
	if !ok {
		canonical = "en"
	}
	p := r.locales[canonical]
	if p == nil {
		return MailPresentation{}, unavailable()
	}
	result := MailPresentation{Locale: p.Locale, Direction: p.Direction}
	if !p.GoogleTranslatedComplete {
		return result, nil
	}
	attribution := p.Translation.Attribution
	if attribution == nil {
		return MailPresentation{}, unavailable()
	}
	result.TranslationLabel = p.Messages["localization_google_reviewed"]
	result.TranslationLink = attribution.Link
	result.TranslationDocumentation = attribution.DocumentationURL
	for _, badge := range attribution.Badges {
		if badge.Variant == "color-regular.png" {
			value := r.blobs[badge.SHA256]
			if value.mediaType != "image/png" {
				return MailPresentation{}, unavailable()
			}
			result.TranslationBadge = append([]byte{}, value.bytes...)
		}
	}
	if result.TranslationLabel == "" || len(result.TranslationBadge) == 0 {
		return MailPresentation{}, unavailable()
	}
	return result, nil
}
func requiredArtifactObject(raw []byte, kind string) bool {
	var values map[string]json.RawMessage
	required, ok := artifactRequiredFields[kind]
	if !ok || json.Unmarshal(raw, &values) != nil || values == nil {
		return false
	}
	for _, name := range required {
		if _, ok := values[name]; !ok {
			return false
		}
	}
	return true
}
func (r *Registry) add(p LocalePackage, raw []byte, seed bool) error {
	if !requiredArtifactObject(raw, p.Kind) {
		return unavailable()
	}
	if len(r.locales) >= r.policy.MaximumLocales && r.locales[p.Locale] == nil {
		return unavailable()
	}
	if e := r.validate(p, seed); e != nil {
		return e
	}
	for _, alias := range p.Aliases {
		if previous, ok := r.aliases[alias]; ok && previous != p.Locale {
			return unavailable()
		}
	}
	if len(p.Fonts) > 0 {
		for _, f := range p.Fonts {
			if r.blobs[f.SHA256].mediaType != "font/ttf" || r.blobs[f.LicenseSHA256].mediaType != "text/plain; charset=utf-8" {
				return unavailable()
			}
		}
	}
	sum := hash(raw)
	if previous := r.packages[p.Locale]; previous != "" && previous != sum {
		delete(r.blobs, previous)
	}
	r.locales[p.Locale] = &p
	r.packages[p.Locale] = sum
	r.blobs[sum] = blob{bytes: raw, mediaType: "application/json"}
	for _, alias := range p.Aliases {
		r.aliases[alias] = p.Locale
	}
	return nil
}
func privateInfo(path string, directory bool) (os.FileInfo, error) {
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, unavailable()
	}
	info, e := os.Lstat(path)
	if e != nil {
		return nil, unavailable()
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() || info.Mode().Perm()&0077 != 0 || directory && !info.IsDir() || !directory && (!info.Mode().IsRegular() || owner.Nlink != 1) {
		return nil, unavailable()
	}
	return info, nil
}
func readPrivate(root *os.Root, path, name string, maximum int) ([]byte, error) {
	info, e := privateInfo(filepath.Join(path, name), false)
	if e != nil || info.Size() < 1 || info.Size() > int64(maximum) {
		return nil, unavailable()
	}
	file, e := root.Open(name)
	if e != nil {
		return nil, unavailable()
	}
	defer file.Close()
	current, e := file.Stat()
	if e != nil || !os.SameFile(info, current) {
		return nil, unavailable()
	}
	raw, e := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if e != nil || int64(len(raw)) != info.Size() {
		return nil, unavailable()
	}
	return raw, nil
}
func (r *Registry) load(path string) error {
	if _, e := privateInfo(path, true); e != nil {
		return e
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return unavailable()
	}
	defer root.Close()
	raw, e := readPrivate(root, path, "inventory.json", r.policy.PackageBytes)
	if e != nil {
		return e
	}
	var inventory Inventory
	if strict.DecodeStrict(raw, &inventory) != nil || !requiredArtifactObject(raw, inventory.Kind) || inventory.SchemaVersion != 1 || inventory.Kind != "projectone-offline-localization-inventory-v1" || inventory.SourceSHA256 != r.source || !digestPattern.MatchString(inventory.CatalogSHA256) || len(inventory.Locales) < 1 || len(inventory.Locales) > r.policy.MaximumLocales || len(inventory.Assets) < 1 || len(inventory.Assets) > r.policy.MaximumLocales+3*r.policy.MaximumFonts+2 {
		return unavailable()
	}
	seen := map[string]bool{"inventory.json": true}
	total := 0
	for _, value := range r.blobs {
		total += len(value.bytes)
	}
	fontCount := 0
	for _, asset := range inventory.Assets {
		if !digestPattern.MatchString(asset.SHA256) || seen[asset.File] || asset.SizeBytes < 1 {
			return unavailable()
		}
		seen[asset.File] = true
		maximum := r.policy.PackageBytes
		extension := ""
		switch asset.MediaType {
		case "application/json":
			extension = ".json"
		case "font/ttf":
			extension = ".ttf"
			maximum = r.policy.FontBytes
			fontCount++
		case "text/plain; charset=utf-8":
			extension = ".txt"
		case "image/png":
			extension = ".png"
		default:
			return unavailable()
		}
		if asset.File != asset.SHA256+extension || asset.SizeBytes > maximum || total > r.policy.AggregateBytes-asset.SizeBytes {
			return unavailable()
		}
		total += asset.SizeBytes
		bytes, e := readPrivate(root, path, asset.File, maximum)
		if e != nil || len(bytes) != asset.SizeBytes || hash(bytes) != asset.SHA256 {
			return unavailable()
		}
		if asset.MediaType == "font/ttf" && len(bytes) < 12 {
			return unavailable()
		}
		if asset.MediaType == "image/png" && (len(bytes) < 8 || string(bytes[:8]) != "\x89PNG\r\n\x1a\n") {
			return unavailable()
		}
		r.blobs[asset.SHA256] = blob{bytes: bytes, mediaType: asset.MediaType}
	}
	if fontCount > r.policy.MaximumFonts {
		return unavailable()
	}
	directory, e := root.Open(".")
	if e != nil {
		return unavailable()
	}
	entries, e := directory.ReadDir(len(seen) + 1)
	directory.Close()
	if e != nil || len(entries) != len(seen) {
		return unavailable()
	}
	for _, entry := range entries {
		if !seen[entry.Name()] || entry.IsDir() {
			return unavailable()
		}
	}
	installed := map[string]bool{}
	for _, item := range inventory.Locales {
		if installed[item.Locale] || !digestPattern.MatchString(item.SHA256) || item.File != item.SHA256+".json" {
			return unavailable()
		}
		installed[item.Locale] = true
		asset, ok := r.blobs[item.SHA256]
		if !ok || asset.mediaType != "application/json" {
			return unavailable()
		}
		var p LocalePackage
		if strict.DecodeStrict(asset.bytes, &p) != nil || p.Locale != item.Locale || p.CatalogSHA256 != "" && p.CatalogSHA256 != inventory.CatalogSHA256 {
			return unavailable()
		}
		legacy := r.locales[item.Locale]
		seed := legacy != nil && !staticMachine(p)
		if seed {
			if !sameSeedText(*legacy, p) {
				return unavailable()
			}
		} else if !staticMachine(p) && p.Translation.Attribution == nil {
			return unavailable()
		}
		if e := r.add(p, asset.bytes, seed); e != nil {
			return e
		}
	}
	return nil
}

// The runtime admits only original hash-pinned font families inspected by the
// manufacturer. New variants and actual weights require a new verified lock.
func knownFont(font Font) bool {
	var lock struct {
		Fonts []struct {
			Family string `json:"family"`
			Format string `json:"format"`
			Weight string `json:"weight"`
			Style  string `json:"style"`
			Font   struct {
				SHA256 string `json:"sha256"`
			} `json:"font"`
			License struct {
				SHA256 string `json:"sha256"`
			} `json:"license"`
		} `json:"fonts"`
	}
	if json.Unmarshal(fontAssets, &lock) != nil {
		return false
	}
	for _, item := range lock.Fonts {
		if item.Font.SHA256 == font.SHA256 {
			return item.License.SHA256 == font.LicenseSHA256 && item.Family == font.Family && item.Format == font.Format && item.Weight == font.Weight && item.Style == font.Style
		}
	}
	return false
}
