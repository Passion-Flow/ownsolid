package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func stoppedControlFixture() (string, RuntimeContainer) {
	scope := strings.Repeat("a", 32)
	var c RuntimeContainer
	c.ID = strings.Repeat("c", 64)
	c.Name = "/" + RuntimeContainerName(scope, "control")
	c.Image = "sha256:" + strings.Repeat("b", 64)
	c.Platform = "linux"
	c.Config.Image = c.Image
	c.Config.User = "10033:10034"
	c.Config.Labels = map[string]string{"io.projectone.isolated-scope": scope, "io.projectone.purpose": "isolated-validation", "com.docker.compose.project": RuntimeProject(scope), "com.docker.compose.service": "control", "com.docker.compose.container-number": "1", "com.docker.compose.oneoff": "False"}
	c.State.Status = "created"
	c.State.StartedAt = neverStarted
	c.HostConfig.ReadonlyRootfs = true
	c.HostConfig.NetworkMode = RuntimeNetworkName(scope)
	return scope, c
}
func TestStoppedControlRemovalUsesExactMetadataAndNeverForceOrVolumes(t *testing.T) {
	for _, fault := range []string{"none", "running", "PID", "paused", "oom", "restarting", "dead", "exit-failure", "foreign-label", "changed-image", "changed-command", "changed-user", "missing", "lost-delete", "replacement-appeared"} {
		t.Run(fault, func(t *testing.T) {
			scope, expected := stoppedControlFixture()
			raw, _ := json.Marshal(expected)
			var actual RuntimeContainer
			json.Unmarshal(raw, &actual)
			found := fault != "missing"
			deletes := 0
			switch fault {
			case "running":
				actual.State.Status = "running"
				actual.State.Running = true
				actual.State.PID = 42
			case "PID":
				actual.State.PID = 42
			case "paused":
				actual.State.Paused = true
			case "oom":
				actual.State.OOMKilled = true
			case "restarting":
				actual.State.Restarting = true
			case "dead":
				actual.State.Dead = true
			case "exit-failure":
				actual.State.Status = "exited"
				actual.State.StartedAt = "2026-10-18T00:00:01Z"
				actual.State.ExitCode = 1
			case "foreign-label":
				actual.Config.Labels["io.projectone.isolated-scope"] = strings.Repeat("f", 32)
			case "changed-image":
				actual.Image = "sha256:" + strings.Repeat("f", 64)
			case "changed-command":
				actual.Config.Cmd = []string{"changed"}
			case "changed-user":
				actual.Config.User = "0:0"
			}
			client, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					if !strings.HasSuffix(r.URL.Path, "/json") {
						t.Error("unexpected read")
					}
					if !found {
						w.WriteHeader(404)
						return
					}
					json.NewEncoder(w).Encode(actual)
				case http.MethodDelete:
					deletes++
					if r.URL.Path != "/v1.49/containers/"+expected.ID || r.URL.Query().Get("force") != "false" || r.URL.Query().Get("v") != "false" {
						t.Error("unsafe removal request")
					}
					found = false
					if fault == "replacement-appeared" {
						found = true
						actual.ID = strings.Repeat("e", 64)
					}
					if fault == "lost-delete" {
						w.WriteHeader(500)
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				default:
					t.Error("unexpected effect")
				}
			})
			e := client.RemoveStoppedControl(context.Background(), scope, expected)
			if fault == "none" {
				if e != nil || found || deletes != 1 {
					t.Fatal("exact stopped removal", e)
				}
			} else if e == nil {
				t.Fatal("unsafe/unknown removal accepted")
			}
			if fault != "none" && fault != "lost-delete" && fault != "replacement-appeared" && deletes != 0 {
				t.Fatal("changed/foreign container deleted")
			}
			if fault == "lost-delete" {
				if deletes != 1 || found {
					t.Fatal("expected real effect then lost result")
				}
				if _, present, e := client.InspectRuntime(context.Background(), scope, "control"); e != nil || present {
					t.Fatal("independent absent observation", e)
				}
				if e := client.RemoveStoppedControl(context.Background(), scope, expected); e == nil || deletes != 1 {
					t.Fatal("normal missing delete replayed")
				}
			}
		})
	}
}
func TestStoppedControlRemovalRejectsUnsafeExpectedMetadataBeforeRequest(t *testing.T) {
	for _, fault := range []string{"scope", "name", "image", "root-user", "readonly", "privileged", "auto-remove", "purpose", "service", "oneoff", "network", "time"} {
		t.Run(fault, func(t *testing.T) {
			scope, c := stoppedControlFixture()
			switch fault {
			case "scope":
				scope = "invalid"
			case "name":
				c.Name = "/foreign"
			case "image":
				c.Image = ""
				c.Config.Image = ""
			case "root-user":
				c.Config.User = "0:0"
			case "readonly":
				c.HostConfig.ReadonlyRootfs = false
			case "privileged":
				c.HostConfig.Privileged = true
			case "auto-remove":
				c.HostConfig.AutoRemove = true
			case "purpose":
				c.Config.Labels["io.projectone.purpose"] = "foreign"
			case "service":
				c.Config.Labels["com.docker.compose.service"] = "postgres"
			case "oneoff":
				c.Config.Labels["com.docker.compose.oneoff"] = "True"
			case "network":
				c.HostConfig.NetworkMode = "host"
			case "time":
				c.State.StartedAt = "invalid"
			}
			calls := 0
			client, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
			if e := client.RemoveStoppedControl(context.Background(), scope, c); e == nil || calls != 0 {
				t.Fatal("unsafe expected metadata submitted", e)
			}
		})
	}
}

func TestRuntimeContainerFingerprintRefusesInvalidMetadataAndBindsChanges(t *testing.T) {
	_, c := stoppedControlFixture()
	first, e := RuntimeContainerDigest(c)
	if e != nil || len(first) != 64 {
		t.Fatal(e)
	}
	c.Config.Cmd = []string{"changed"}
	changed, e := RuntimeContainerDigest(c)
	if e != nil || changed == first {
		t.Fatal("command omitted from binding", e)
	}
	c.Config.Volumes = map[string]json.RawMessage{"/unsafe": json.RawMessage("invalid")}
	if hash, e := RuntimeContainerDigest(c); e == nil || hash != "" {
		t.Fatal("invalid metadata became a valid digest")
	}
}
