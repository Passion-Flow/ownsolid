// Package mysqlmigrate owns explicit ordered MySQL DDL and its durable journal.
// Its initial account/Workspace catalog is not a full product storage profile.
package mysqlmigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const maximumCatalogBytes = 1 << 20
const maximumCatalogSteps = 4096
const maximumStatementBytes = 1 << 20
const referenceFormat = "projectone.mysql.schema-reference.v1"
const createTable = "create_table"
const alterTable = "alter_table"
const evolutionFormat = "projectone.mysql.schema-evolution.v1"
const installationSeed = "seed"
const seedDefinition = "EXISTS installation_state id=1"
const membershipAuthoritySeedObject = "membership_authority_seed"
const membershipAuthoritySeedDefinition = "NOT EXISTS workspace_memberships authority_id IS NULL"
const membershipAuthoritySeedSQL = "UPDATE workspace_memberships SET authority_id=UUID() WHERE authority_id IS NULL;"
const licenseStateSeedObject = "license_state_seed"
const licenseStateSeedDefinition = "EXISTS platform_license_state id=1"
const licenseStateSeedSQL = "INSERT INTO platform_license_state(id) VALUES(1);"
const userPreferencesSeedObject = "user_preferences_seed"
const userPreferencesSeedDefinition = "NOT EXISTS users without user_preferences"
const userPreferencesSeedSQL = "INSERT INTO user_preferences(user_id) SELECT id FROM users;"
const journalObject = "schema_migrations"

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var assetFile = regexp.MustCompile(`^[0-9]{6}_[a-z][a-z0-9_]+\.sql$`)
var checksum = regexp.MustCompile(`^[a-f0-9]{64}$`)

type reference struct {
	Step                     int    `json:"step"`
	File                     string `json:"file"`
	SourceSHA256             string `json:"source_sha256"`
	Object                   string `json:"object"`
	Kind                     string `json:"kind"`
	DefinitionSHA256         string `json:"definition_sha256"`
	Definition               string `json:"definition"`
	PreviousDefinitionSHA256 string `json:"previous_definition_sha256,omitempty"`
}
type evolutionDocument struct {
	Format              string      `json:"format"`
	BaseReferenceSHA256 string      `json:"base_reference_sha256"`
	Steps               []reference `json:"steps"`
}
type referenceDocument struct {
	Format  string      `json:"format"`
	Version string      `json:"version"`
	Scope   string      `json:"scope"`
	Journal reference   `json:"journal"`
	Steps   []reference `json:"steps"`
}
type entry struct {
	reference
	sql string
}

type Catalog struct {
	version string
	scope   string
	journal entry
	steps   []entry
}

func hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func invalidCatalog() error { return errors.New(CodeCatalogInvalid) }

func ownedAlter(statement, object string) bool {
	remainder, ok := strings.CutPrefix(statement, "ALTER TABLE "+object)
	return ok && len(remainder) > 0 && strings.ContainsRune(" \t\r\n", rune(remainder[0]))
}

func loadEvolution(base, evolution []byte, files fs.FS) (Catalog, error) {
	var document referenceDocument
	var extension evolutionDocument
	if len(base) == 0 || len(base) > maximumCatalogBytes || len(evolution) == 0 || len(evolution) > maximumCatalogBytes || strict.DecodeStrict(base, &document) != nil || strict.DecodeStrict(evolution, &extension) != nil || extension.Format != evolutionFormat || extension.BaseReferenceSHA256 != hash(string(base)) || len(extension.Steps) == 0 || len(extension.Steps) > maximumCatalogSteps-len(document.Steps) {
		return Catalog{}, invalidCatalog()
	}
	document.Steps = append(document.Steps, extension.Steps...)
	merged, e := json.Marshal(document)
	if e != nil {
		return Catalog{}, invalidCatalog()
	}
	return loadCatalog(merged, files)
}

func DefaultCatalog() (Catalog, error) {
	raw, e := assets.Files.ReadFile("reference.json")
	if e != nil {
		return Catalog{}, invalidCatalog()
	}
	evolution, e := assets.Files.ReadFile("evolution.json")
	if e != nil {
		return Catalog{}, invalidCatalog()
	}
	return loadEvolution(raw, evolution, assets.Files)
}
func loadCatalog(raw []byte, files fs.FS) (Catalog, error) {
	var d referenceDocument
	if len(raw) == 0 || len(raw) > maximumCatalogBytes || strict.DecodeStrict(raw, &d) != nil || d.Format != referenceFormat || !regexp.MustCompile(`^8\.4\.(0|[1-9][0-9]*)$`).MatchString(d.Version) || d.Scope != "account_workspace_schema_only_product_pending" || len(d.Steps) == 0 || len(d.Steps) > maximumCatalogSteps {
		return Catalog{}, invalidCatalog()
	}
	out := Catalog{version: d.Version, scope: d.Scope}
	seen := map[string]bool{}
	load := func(r reference, journal bool) (entry, error) {
		if !identifier.MatchString(r.Object) || !checksum.MatchString(r.SourceSHA256) || !checksum.MatchString(r.DefinitionSHA256) || r.DefinitionSHA256 != hash(r.Definition) || !utf8.ValidString(r.Definition) || len(r.Definition) > maximumStatementBytes || strings.TrimSpace(r.Definition) != r.Definition || seen[r.File] {
			return entry{}, invalidCatalog()
		}
		if journal {
			if r.Step != 0 || r.File != "journal.sql" || r.Object != journalObject || r.Kind != createTable {
				return entry{}, invalidCatalog()
			}
		} else if !assetFile.MatchString(r.File) || r.Step < 1 || r.Step > maximumCatalogSteps || !strings.HasPrefix(r.File, fmt.Sprintf("%06d_", r.Step)) || r.Object == journalObject {
			return entry{}, invalidCatalog()
		}
		seen[r.File] = true
		b, e := fs.ReadFile(files, r.File)
		if e != nil || len(b) == 0 || len(b) > maximumStatementBytes || !utf8.Valid(b) || hash(string(b)) != r.SourceSHA256 {
			return entry{}, invalidCatalog()
		}
		statement := strings.TrimSpace(string(b))
		if !strings.HasSuffix(statement, ";") || r.Kind != createTrigger && !singleStatementTerminator(statement) {
			return entry{}, invalidCatalog()
		}
		switch r.Kind {
		case createTable:
			if r.PreviousDefinitionSHA256 != "" || !strings.HasPrefix(statement, "CREATE TABLE "+r.Object+" (") || !strings.HasPrefix(r.Definition, "CREATE TABLE `"+r.Object+"` (") {
				return entry{}, invalidCatalog()
			}
		case alterTable:
			if journal || !checksum.MatchString(r.PreviousDefinitionSHA256) || r.PreviousDefinitionSHA256 == r.DefinitionSHA256 || !ownedAlter(statement, r.Object) || !strings.HasPrefix(r.Definition, "CREATE TABLE `"+r.Object+"` (") {
				return entry{}, invalidCatalog()
			}
		case createTrigger:
			definition, canonical, e := canonicalTrigger(r.Definition)
			if e != nil || journal || r.PreviousDefinitionSHA256 != "" || definition.Name != r.Object || canonical != r.Definition || statement != triggerSource(definition) {
				return entry{}, invalidCatalog()
			}
		case installationSeed:
			installation := r.Object == "installation_seed" && statement == "INSERT INTO installation_state(id) VALUES(1);" && r.Definition == seedDefinition
			membership := r.Object == membershipAuthoritySeedObject && statement == membershipAuthoritySeedSQL && r.Definition == membershipAuthoritySeedDefinition
			preferences := r.Object == userPreferencesSeedObject && statement == userPreferencesSeedSQL && r.Definition == userPreferencesSeedDefinition
			licenseState := r.Object == licenseStateSeedObject && statement == licenseStateSeedSQL && r.Definition == licenseStateSeedDefinition
			if r.PreviousDefinitionSHA256 != "" || journal || !installation && !membership && !licenseState && !preferences {
				return entry{}, invalidCatalog()
			}
		default:
			return entry{}, invalidCatalog()
		}
		return entry{r, statement}, nil
	}
	var e error
	if out.journal, e = load(d.Journal, true); e != nil {
		return Catalog{}, e
	}
	objects := map[string]string{}
	objectKinds := map[string]string{}
	definitions := map[string]entry{}
	for index, r := range d.Steps {
		if r.Step != index+1 {
			return Catalog{}, invalidCatalog()
		}
		item, e := load(r, false)
		if e != nil {
			return Catalog{}, e
		}
		if item.Kind == alterTable {
			if previous := objects[item.Object]; previous == "" || previous != item.PreviousDefinitionSHA256 || objectKinds[item.Object] != createTable {
				return Catalog{}, invalidCatalog()
			}
			if !validWorkflowCheckEvolution(definitions[item.Object], item) {
				return Catalog{}, invalidCatalog()
			}
		} else if objects[item.Object] != "" {
			return Catalog{}, invalidCatalog()
		}
		if item.Kind == installationSeed && item.Object == membershipAuthoritySeedObject {
			present := false
			for _, previous := range out.steps {
				if previous.Object == "workspace_memberships" && previous.Kind == alterTable && strings.Contains(previous.Definition, "`authority_id`") {
					present = true
				}
			}
			if !present {
				return Catalog{}, invalidCatalog()
			}
		}
		if item.Kind == installationSeed && item.Object == licenseStateSeedObject && objectKinds["platform_license_state"] != createTable {
			return Catalog{}, invalidCatalog()
		}
		if item.Kind == installationSeed && item.Object == userPreferencesSeedObject && (objectKinds["users"] != createTable || objectKinds["user_preferences"] != createTable) {
			return Catalog{}, invalidCatalog()
		}
		if item.Kind == createTrigger {
			definition, _, _ := canonicalTrigger(item.Definition)
			if !validWorkflowTriggerPredecessor(definition, definitions) {
				return Catalog{}, invalidCatalog()
			}
			if objectKinds[definition.Table] != createTable {
				return Catalog{}, invalidCatalog()
			}
		}
		objects[r.Object] = item.DefinitionSHA256
		definitions[r.Object] = item
		if item.Kind != alterTable {
			objectKinds[r.Object] = item.Kind
		}
		out.steps = append(out.steps, item)
	}
	listed, e := fs.Glob(files, "*.sql")
	if e != nil || len(listed) != len(seen) {
		return Catalog{}, invalidCatalog()
	}
	for _, name := range listed {
		if !seen[name] {
			return Catalog{}, invalidCatalog()
		}
	}
	return out, nil
}

func (c Catalog) Version() string { return c.version }
func (c Catalog) Scope() string   { return c.scope }
func (c Catalog) Steps() int      { return len(c.steps) }
