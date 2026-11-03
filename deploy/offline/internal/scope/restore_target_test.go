package scope

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func restoreTargetInputFixture() (preparedInputs, compose.Model, BackupPlan, map[string][]engine.ConfigurationFile) {
	root := engine.DatabaseTaskDestination
	envelope := func(dsn string) []byte {
		return encode(map[string]any{"database": map[string]string{"dsn_file": root + "/private/" + dsn}, "security": map[string]string{"auth_hmac_key_file": root + "/private/auth-hmac", "mail_keyring_file": root + "/private/mail-keyring.json"}})
	}
	credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	dsn := func(role, password string) []byte {
		return []byte((&url.URL{Scheme: "postgresql", User: url.UserPassword(role, password), Host: "postgres:5432", Path: "/synthetic_restore", RawQuery: "sslmode=disable"}).String())
	}
	opPassword := "explicit-synthetic-operator-only"
	runtime := dsn("synthetic_new_runtime", credential)
	p := preparedInputs{files: map[string][]engine.ConfigurationFile{
		"operator-config": {{Name: "control.yaml", Contents: envelope("operator-dsn")}, {Name: "runtime.yaml", Contents: envelope("runtime-dsn")}, {Name: "operator-dsn", Contents: dsn("synthetic_operator", opPassword)}, {Name: "runtime-dsn", Contents: runtime}},
		"control-config":  {{Name: "runtime.yaml", Contents: envelope("runtime-dsn")}, {Name: "runtime-dsn", Contents: runtime}, {Name: "auth-hmac", Contents: []byte("explicit synthetic retained HMAC fixture")}, {Name: "mail-keyring.json", Contents: []byte("explicit synthetic retained encryption fixture")}, {Name: "future-key", Contents: []byte("explicit retained unknown key fixture")}},
		"postgres-config": {{Name: "postgres-password", Contents: []byte(opPassword)}},
	}}
	model := compose.Model{Services: map[string]compose.Service{
		"control":  {Mounts: []compose.Mount{{Type: "volume", Source: "control-config", Target: root, ReadOnly: true}}},
		"postgres": {Environment: map[string]string{"POSTGRES_DB": "synthetic_restore", "POSTGRES_USER": "synthetic_operator", "POSTGRES_PASSWORD_FILE": "/run/projectone/postgres/private/postgres-password"}, Mounts: []compose.Mount{{Type: "volume", Source: "postgres-config", Target: "/run/projectone/postgres", ReadOnly: true}}},
	}}
	old := map[string][]engine.ConfigurationFile{}
	var plan BackupPlan
	for role, files := range p.files {
		for _, f := range files {
			copy := engine.ConfigurationFile{Name: f.Name, Contents: append([]byte{}, f.Contents...)}
			if copy.Name == "runtime-dsn" {
				copy.Contents = dsn("synthetic_old_runtime", base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)))
			}
			old[role] = append(old[role], copy)
			plan.ConfigurationFiles = append(plan.ConfigurationFiles, BackupConfigurationFile{Role: role, File: InputFile{Name: copy.Name, Bytes: int64(len(copy.Contents)), SHA256: wire.Digest(copy.Contents)}})
		}
	}
	return p, model, plan, old
}

func replaceRestoreTestFile(p *preparedInputs, role, name string, raw []byte) {
	for i := range p.files[role] {
		if p.files[role][i].Name == name {
			p.files[role][i].Contents = raw
			return
		}
	}
}

func TestRestoreTargetPrivateConnectionsKeysAndNondefaultPort(t *testing.T) {
	p, model, source, files := restoreTargetInputFixture()
	connection, e := restoreConnections(p, model)
	if e != nil || connection.database != "synthetic_restore" || connection.port != 5432 {
		t.Fatal("synthetic fixed connection", e)
	}
	if e = restoreKeyInventory(p, model, source, files); e != nil {
		t.Fatal(e)
	}
	for role, inputs := range p.files {
		for i, f := range inputs {
			if strings.HasSuffix(f.Name, "-dsn") {
				p.files[role][i].Contents = bytes.ReplaceAll(f.Contents, []byte(":5432/"), []byte(":5544/"))
			}
		}
	}
	pg := model.Services["postgres"]
	pg.Command = []string{"postgres", "-p", "5544"}
	model.Services["postgres"] = pg
	if connection, e = restoreConnections(p, model); e != nil || connection.port != 5544 {
		t.Fatal("explicit nondefault private server port", e)
	}
	if e = restoreKeyInventory(p, model, source, files); e != nil {
		t.Fatal("credential generation should not alter retained keys", e)
	}
}

func TestRestoreTargetConnectionOverridesAndRetainedKeyLossRefused(t *testing.T) {
	for _, fault := range []string{"outside-host", "query-override", "encoded-path", "same-role", "bad-credential", "serving-dsn", "pg-user", "pg-password", "pg-command", "mutable-key", "missing-future-key", "renamed-key-reference", "unknown-secret-replaced", "old-runtime-reused", "missing-mount", "duplicate-input"} {
		t.Run(fault, func(t *testing.T) {
			p, model, source, files := restoreTargetInputFixture()
			raw, _ := restoreNamedFile(p.files["operator-config"], "operator-dsn")
			switch fault {
			case "outside-host":
				replaceRestoreTestFile(&p, "operator-config", "operator-dsn", bytes.ReplaceAll(raw, []byte("postgres:5432"), []byte("old-source.example.invalid:5432")))
			case "query-override":
				replaceRestoreTestFile(&p, "operator-config", "operator-dsn", append(raw, []byte("&host=outside")...))
			case "encoded-path":
				replaceRestoreTestFile(&p, "operator-config", "operator-dsn", bytes.ReplaceAll(raw, []byte("/synthetic_restore"), []byte("/synthetic%5frestore")))
			case "same-role":
				for _, role := range []string{"operator-config", "control-config"} {
					replaceRestoreTestFile(&p, role, "runtime-dsn", raw)
				}
			case "bad-credential":
				for _, role := range []string{"operator-config", "control-config"} {
					r, _ := restoreNamedFile(p.files[role], "runtime-dsn")
					u, _ := url.Parse(string(r))
					u.User = url.UserPassword("synthetic_new_runtime", strings.Repeat("a", 64))
					replaceRestoreTestFile(&p, role, "runtime-dsn", []byte(u.String()))
				}
			case "serving-dsn":
				replaceRestoreTestFile(&p, "control-config", "runtime-dsn", raw)
			case "pg-user":
				model.Services["postgres"].Environment["POSTGRES_USER"] = "outside"
			case "pg-password":
				replaceRestoreTestFile(&p, "postgres-config", "postgres-password", []byte("different private fixture"))
			case "pg-command":
				v := model.Services["postgres"]
				v.Command = []string{"postgres", "-c", "port=5544"}
				model.Services["postgres"] = v
			case "mutable-key":
				replaceRestoreTestFile(&p, "control-config", "auth-hmac", []byte("replaced key"))
			case "missing-future-key":
				p.files["control-config"] = p.files["control-config"][:4]
			case "renamed-key-reference":
				r, _ := restoreNamedFile(p.files["control-config"], "runtime.yaml")
				replaceRestoreTestFile(&p, "control-config", "runtime.yaml", bytes.ReplaceAll(r, []byte("auth-hmac"), []byte("future-key")))
			case "unknown-secret-replaced":
				replaceRestoreTestFile(&p, "control-config", "future-key", []byte("lost future decryption material"))
			case "old-runtime-reused":
				old, _ := restoreNamedFile(files["control-config"], "runtime-dsn")
				for _, role := range []string{"operator-config", "control-config"} {
					replaceRestoreTestFile(&p, role, "runtime-dsn", old)
				}
			case "missing-mount":
				v := model.Services["control"]
				v.Mounts = nil
				model.Services["control"] = v
			case "duplicate-input":
				p.files["operator-config"] = append(p.files["operator-config"], p.files["operator-config"][0])
			}
			_, connectionError := restoreConnections(p, model)
			keyError := restoreKeyInventory(p, model, source, files)
			if connectionError == nil && keyError == nil {
				t.Fatal("unsafe target admitted")
			}
			for _, e := range []error{connectionError, keyError} {
				if e != nil && (!strings.HasPrefix(e.Error(), "OFFLINE_SCOPE_RESTORE_") || strings.ContainsAny(e.Error(), " /:@")) {
					t.Fatal("unsafe diagnostic")
				}
			}
		})
	}
}

func TestRestoreTargetSourceURIEncodedOverrideAndAmbientRefusal(t *testing.T) {
	for _, raw := range []string{"postgresql://role:synthetic@postgres/db?sslmode=disable", "postgresql://role:synthetic@postgres:5432/db?sslmode=disable&sslmode=disable", "postgresql://role:synthetic@postgres:5432/db?sslmode=prefer", "postgresql://role:synthetic@127.0.0.1:5432/db?sslmode=disable", "postgresql://role:synthetic@postgres:05432/db?sslmode=disable", "postgresql://role:synthetic@postgres:5432/db/extra?sslmode=disable", "postgresql://role:synthetic@postgres:5432/db?sslmode=disable#override"} {
		if _, _, e := restoreURI([]byte(raw)); e == nil {
			t.Fatal("noncanonical or outside connection admitted")
		}
	}
}

func TestRestoreTargetFreshLineageRejectsSourceAndHistoricalApplication(t *testing.T) {
	c, d, w := databaseProcessFixture(t)
	ctx := context.Background()
	if _, e := processRuntimeSelected(ctx, c, d, w, "running", false, []string{"postgres"}); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	v, e := l.verifyRuntimeContext(ctx, d, w, false)
	if e != nil {
		t.Fatal(e)
	}
	source := RestoreSourceResult{SourceScopeID: strings.Repeat("a", 32), ReleaseID: l.marker.ReleaseID, Profile: l.marker.Profile}
	if e = restoreTargetLineage(l.marker, l.events, v, source); e != nil {
		t.Fatal(e)
	}
	modelEvents := append([]Event{}, l.events...)
	modelEvents = append(modelEvents, Event{Action: "volume_prepare", Outcome: "completed", Volume: &VolumeRecord{Key: "models", Phase: phasePrepared}})
	withModels := source
	withModels.Stores = []string{"models"}
	if e := restoreTargetLineage(l.marker, modelEvents, v, withModels); e != nil {
		t.Fatal("declared restored model store omitted", e)
	}
	if e := restoreTargetLineage(l.marker, modelEvents, v, source); e == nil {
		t.Fatal("target model store missing from source accepted")
	}
	for _, fault := range []string{"same-source", "release", "profile", "historical-sql", "running-application", "missing-store", "extra-store"} {
		t.Run(fault, func(t *testing.T) {
			m := l.marker
			s := source
			events := append([]Event{}, l.events...)
			copy := v
			copy.observed.Services = map[string]compose.RuntimeServiceStatus{}
			for k, status := range v.observed.Services {
				copy.observed.Services[k] = status
			}
			switch fault {
			case "same-source":
				s.SourceScopeID = m.ScopeID
			case "release":
				s.ReleaseID = "different"
			case "profile":
				s.Profile = "different"
			case "historical-sql":
				events = append(events, Event{Action: "database_task_create", Outcome: "completed", DatabaseTask: &DatabaseTaskRecord{}})
			case "running-application":
				x := copy.observed.Services["control"]
				x.State = "running"
				copy.observed.Services["control"] = x
			case "missing-store":
				s.Stores = []string{"datasets"}
			case "extra-store":
				s.Stores = []string{"unrecognized"}
			}
			if e := restoreTargetLineage(m, events, copy, s); e == nil {
				t.Fatal("unsafe lineage admitted")
			}
		})
	}
}
