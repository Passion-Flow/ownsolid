package e2e

import (
	"crypto/sha256"
	"crypto/x509"
	"debug/buildinfo"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTemporaryCombinedInstalledAcceptance(t *testing.T) {
	e := loadEnvironment(t)
	directory := "/evidence/install-inputs"
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	historySetting := ""
	switch os.Getenv("ACCEPTANCE_WITH_HISTORY") {
	case "true":
		history, err := os.ReadFile("/evidence/history-ready.json")
		if err != nil {
			t.Fatal(err)
		}
		var ready struct {
			Status         string `json:"status"`
			MonitoringFile string `json:"monitoring_file"`
		}
		start := strings.LastIndex(string(history), "{\"format_version\"")
		if start < 0 || json.Unmarshal(history[start:], &ready) != nil || ready.Status != "FLEET_HISTORY_READY" {
			t.Fatal("History deployment did not publish ready output")
		}
		historySetting = ", fleet_history_control_directory: " + filepath.Dir(ready.MonitoringFile)
	case "false":
	default:
		t.Fatal("Select whether the admitted window includes history")
	}
	extra := fmt.Sprintf(`project: lane-del-installed
network: {bind_address: 127.0.0.1, https_port: 8443, state_subnet: 172.31.254.0/26}
telemetry: {metrics_retention: 2d, logs_retention: 2d%s}
resources:
  control_memory: 1g
  control_cpus: "2"
  postgres_memory: 768m
  postgres_cpus: "2"
  postgres_max_connections: 50
  metrics_memory: 256m
  metrics_cpus: "1"
  logs_memory: 256m
  logs_cpus: "1"
`, historySetting)
	config := e.configurationWith(t, directory, "single-node", e.controlImage, extra)
	e.apply(t, config)
	container, err := e.compose(t, "ps", "--quiet", "control")
	if err != nil || len(strings.Fields(container)) != 1 {
		t.Fatal("Installed Control container identity unavailable")
	}
	container = strings.TrimSpace(container)
	actualImage, err := run(t, "docker", "inspect", "--format", "{{.Image}}", container)
	if err != nil || strings.TrimSpace(actualImage) != e.controlImage {
		t.Fatal("Installed Control image differs from the pinned input")
	}
	label, err := run(t, "docker", "image", "inspect", "--format", `{{index .Config.Labels "io.ownsolid.acceptance.source"}}`, e.controlImage)
	if err != nil || strings.TrimSpace(label) != os.Getenv("COMBINED_SOURCE_SHA") {
		t.Fatal("Installed image source label differs from the frozen source")
	}
	if output, err := run(t, "docker", "cp", container+":/usr/local/bin/control", "/evidence/installed-control.bin"); err != nil {
		t.Fatalf("Cannot read installed Control binary: %v %s", err, output)
	}
	installed, err := os.ReadFile("/evidence/installed-control.bin")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("/bundle/control")
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(installed) != sha256.Sum256(expected) {
		t.Fatal("Installed Control binary differs from the native build")
	}
	build, err := buildinfo.ReadFile("/evidence/installed-control.bin")
	if err != nil {
		t.Fatal("Installed ELF build information unavailable")
	}
	settings := map[string]string{}
	for _, setting := range build.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["vcs.revision"] != os.Getenv("COMBINED_SOURCE_SHA") || settings["vcs.modified"] != "false" {
		t.Fatal("Installed Control build is not bound to the clean frozen source", settings["vcs.revision"], settings["vcs.modified"])
	}
	profile, err := os.ReadFile(filepath.Join(e.root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	shippedProfile, err := os.ReadFile("/bundle/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(profile) != sha256.Sum256(shippedProfile) {
		t.Fatal("Installed Compose profile differs from the frozen source")
	}
	provenance, err := json.Marshal(map[string]any{"target_source_sha": os.Getenv("COMBINED_SOURCE_SHA"), "actual_image": strings.TrimSpace(actualImage), "actual_source_label": strings.TrimSpace(label), "installed_control_sha256": fmt.Sprintf("%x", sha256.Sum256(installed)), "build_go_version": build.GoVersion, "build_revision": settings["vcs.revision"], "build_modified": settings["vcs.modified"], "installed_compose_sha256": fmt.Sprintf("%x", sha256.Sum256(profile)), "release_mode": "direct Apply with pinned qualification images; no production signed release artifact is claimed"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/evidence/installed-provenance.json", provenance, 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("INSTALLED_CONTROL_IMAGE_BINARY_BUILDINFO_AND_COMPOSE_BOUND_TO_FROZEN_SOURCE")
	e.assertWebApplication(t, adminOrigin)
	e.assertWebApplication(t, portalOrigin)
	password := e.password(t)
	admin, csrf := e.adminSession(t, password)
	e.installLicense(t, admin, csrf)
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join("/evidence", name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	credential, err := os.ReadFile(filepath.Join(e.root, "operator/admin-credential"))
	if err != nil {
		t.Fatal(err)
	}
	write("admin-credential", credential)
	write("admin-password", []byte(password))
	ca, err := os.ReadFile(filepath.Join(e.root, "config/ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	write("installation-ca.pem", ca)
	cert, err := os.ReadFile(filepath.Join(e.root, "config/certificate.pem"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(cert)
	if block == nil {
		t.Fatal("Missing installed leaf")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	write("spki", []byte(base64.StdEncoding.EncodeToString(pin[:])))
	write("install-config-path", []byte(config))
	summary, _ := json.Marshal(map[string]any{"candidate": os.Getenv("COMBINED_SOURCE_SHA"), "control_image": e.controlImage, "admin_origin": adminOrigin, "portal_origin": portalOrigin, "installation_root": e.root, "history_root": "/srv/fleet-history", "schema": e.sql(t, "SELECT max(version) FROM schema_migrations"), "public_tables": e.sql(t, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'")})
	write("installed-ready.json", summary)
	output, err := run(t, "/bundle/scope.test", "-test.run=^TestTemporaryInstalledScopePreparation$", "-test.v", "-test.timeout=180s")
	if err != nil {
		t.Fatalf("Installed scope preparation failed: %v\n%s", err, output)
	}
	fmt.Println(output)
	var scope map[string]json.RawMessage
	scopeBytes, err := os.ReadFile("/evidence/scope-state.json")
	if err != nil || json.Unmarshal(scopeBytes, &scope) != nil {
		t.Fatal("Scope preparation did not publish complete metadata")
	}
	var workspace string
	if json.Unmarshal(scope["primary_workspace"], &workspace) != nil || workspace == "" {
		t.Fatal("The prepared primary workspace is absent")
	}
	database, err := os.ReadFile("/evidence/schema-table-set-proof.json")
	if err != nil || !json.Valid(database) {
		t.Fatal("The installed source catalog and migration proof is absent")
	}
	complete, err := json.Marshal(map[string]any{
		"candidate": os.Getenv("COMBINED_SOURCE_SHA"), "control_image": e.controlImage,
		"admin_origin": adminOrigin, "portal_origin": portalOrigin, "installation_root": e.root,
		"administrator_email": adminEmail,
		"scope_prepared":      true, "primary_workspace_id": workspace,
		"scope_projection_boundary": "Published empty projections; not runnable workflows",
		"database":                  json.RawMessage(database),
	})
	if err != nil {
		t.Fatal(err)
	}
	write("acceptance-ready.json.stage", complete)
	if err := os.Rename("/evidence/acceptance-ready.json.stage", "/evidence/acceptance-ready.json"); err != nil {
		t.Fatal(err)
	}
	fmt.Println("COMBINED_INSTALLED_CONTROL_ADMIN_PORTAL_READY")
	deadline := time.NewTimer(6 * time.Hour)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("Owner acceptance did not finish before the bounded environment deadline")
		case <-tick.C:
			if _, err := os.Stat("/evidence/owners-finished"); err == nil {
				if err := verifyNoEgress(e.egress.log); err != nil {
					t.Fatal(err)
				}
				fmt.Println("COMBINED_OWNER_ACCEPTANCE_FINISHED")
				return
			}
		}
	}
}

func TestTemporaryStrictEgressRecorderGuard(t *testing.T) {
	base := []map[string]string{
		{"time": "2026-11-12T00:00:00Z", "protocol": "tcp", "source": "198.18.0.1:40000", "destination": "192.0.2.10:9"},
		{"time": "2026-11-12T00:00:00Z", "protocol": "tcp", "source": "[2001:db8:e9e5::1]:40000", "destination": "[2001:db8::10]:9"},
		{"time": "2026-11-12T00:00:00Z", "protocol": "udp", "source": "198.18.0.1:40000", "destination": "192.0.2.53:53", "dns_question": "probe.invalid. A"},
		{"time": "2026-11-12T00:00:00Z", "protocol": "tcp", "source": "172.17.0.2:40000", "destination": "192.0.2.10:9"},
		{"time": "2026-11-12T00:00:00Z", "protocol": "udp", "source": "172.17.0.2:40000", "destination": "192.0.2.53:53", "dns_question": "probe.invalid. A"},
	}
	for _, fault := range []string{"valid", "empty", "missing-ipv6", "duplicate", "foreign-dns", "foreign-tcp"} {
		t.Run(fault, func(t *testing.T) {
			rows := make([]map[string]string, len(base))
			for i, row := range base {
				rows[i] = map[string]string{}
				for k, v := range row {
					rows[i][k] = v
				}
			}
			switch fault {
			case "empty":
				rows = nil
			case "missing-ipv6":
				rows = append(rows[:1], rows[2:]...)
			case "duplicate":
				rows = append(rows, rows[0])
			case "foreign-dns":
				rows[2]["dns_question"] = "runtime-download.invalid. A"
			case "foreign-tcp":
				rows[0]["destination"] = "203.0.113.7:443"
			}
			var capture strings.Builder
			for _, row := range rows {
				raw, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				capture.Write(raw)
				capture.WriteString("\n")
			}
			path := filepath.Join(t.TempDir(), "capture.jsonl")
			if err := os.WriteFile(path, []byte(capture.String()), 0600); err != nil {
				t.Fatal(err)
			}
			err := verifyNoEgress(path)
			if (err == nil) != (fault == "valid") {
				t.Fatalf("capture admission for %s: %v", fault, err)
			}
		})
	}
}
