package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"testing"
)

func TestStoppedControlConfigurationReadbackVerifiesExactReadonlyGeneration(t *testing.T) {
	testControlConfigurationReadback(t, "created", true, false)
}

func TestCurrentControlConfigurationReadbackVerifiesExactReadonlyGeneration(t *testing.T) {
	for _, state := range []string{"created", "running", "exited"} {
		t.Run(state, func(t *testing.T) { testControlConfigurationReadback(t, state, false, false) })
	}
}

func TestFailedControlConfigurationReadbackKeepsCleanReaderStrict(t *testing.T) {
	testControlConfigurationReadback(t, "exited", false, true)
}

func testControlConfigurationReadback(t *testing.T, state string, legacy, failed bool) {
	t.Helper()
	for _, fault := range []string{"none", "owner", "consumer", "wrong-ID", "started", "writable", "source", "actual-mount", "payload", "archive-limit", "changed-after", "privileged", "paused", "OOM", "PID", "timestamp", "exit-code"} {
		t.Run(fault, func(t *testing.T) {
			scope, object := stoppedControlFixture()
			op := strings.Repeat("d", 32)
			o := Ownership{scope, op, TLSConfigurationRole + "-" + op}
			target := "/run/projectone/control"
			data := []ConfigurationFile{{Name: "runtime.yaml", Contents: []byte("explicit synthetic private runtime")}, {Name: "key.pem", Contents: []byte("explicit synthetic private key")}}
			encoded, _ := json.Marshal(object)
			var values map[string]any
			json.Unmarshal(encoded, &values)
			values["HostConfig"].(map[string]any)["Mounts"] = []any{map[string]any{"Type": "volume", "Source": o.VolumeName(), "Target": target, "ReadOnly": true, "VolumeOptions": map[string]any{"NoCopy": true}}}
			values["Mounts"] = []any{map[string]any{"Type": "volume", "Name": o.VolumeName(), "Destination": target, "Driver": "local", "RW": false}}
			encoded, _ = json.Marshal(values)
			json.Unmarshal(encoded, &object)
			object.State.Status = state
			if failed {
				object.State.ExitCode = 1
			}
			if state != "created" {
				object.State.StartedAt = "2026-10-18T00:00:00Z"
			}
			if state == "running" {
				object.State.Running, object.State.PID = true, 42
			}
			switch fault {
			case "wrong-ID":
				object.ID = strings.Repeat("f", 64)
			case "started":
				object.State.Status = "running"
				object.State.PID = 0
				object.State.Running = true
			case "privileged":
				object.HostConfig.Privileged = true
			case "paused":
				object.State.Paused = true
			case "OOM":
				object.State.OOMKilled = true
			case "PID":
				object.State.PID = -1
			case "timestamp":
				object.State.StartedAt = "invalid"
			case "exit-code":
				object.State.ExitCode = 1
				if failed {
					object.State.ExitCode = 0
				}
			case "writable":
				object.HostConfig.Mounts[0].ReadOnly = false
			case "source":
				object.HostConfig.Mounts[0].Source = "foreign"
			case "actual-mount":
				object.Mounts[0].Name = "foreign"
			}
			reads, archiveReads := 0, 0
			var client *Client
			client, _ = fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Fatal("readback submitted a mutation")
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/"+RuntimeContainerName(scope, "control")+"/json"):
					reads++
					reply := object
					if fault == "changed-after" && reads > 1 {
						reply.Config.Cmd = []string{"changed"}
					}
					json.NewEncoder(w).Encode(reply)
				case strings.Contains(r.URL.Path, "/volumes/"):
					labels := o.labels()
					if fault == "owner" {
						labels["io.projectone.operation"] = strings.Repeat("f", 32)
					}
					json.NewEncoder(w).Encode(Volume{Name: o.VolumeName(), Driver: "local", Scope: "local", Labels: labels})
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					ids := []RuntimeObject{{ID: strings.Repeat("c", 64)}}
					if fault == "consumer" {
						ids = append(ids, RuntimeObject{ID: strings.Repeat("f", 64)})
					}
					json.NewEncoder(w).Encode(ids)
				case strings.HasSuffix(r.URL.Path, "/archive"):
					archiveReads++
					if fault == "archive-limit" {
						w.Header().Set("Content-Length", "1048577")
						w.WriteHeader(http.StatusOK)
						return
					}
					if r.URL.Query().Get("path") != target {
						t.Fatal("arbitrary path read")
					}
					payload := configurationReadbackTar(t, data, target, fault == "payload")
					w.Write(payload)
				default:
					t.Error("unexpected request")
				}
			})
			client.config.ResponseBytes = 1 << 20
			read := client.InspectCurrentControlConfiguration
			if failed {
				read = client.InspectFailedControlConfiguration
			}
			if legacy {
				read = client.InspectStoppedControlConfiguration
			}
			observed, e := read(context.Background(), o, strings.Repeat("c", 64), target, 10033, 10034, data)
			if fault == "none" {
				if e != nil || observed.Status != ConfigurationPayloadVerified || archiveReads != 1 {
					t.Fatal("verified bound readback", observed, e)
				}
			} else if e == nil {
				t.Fatal("changed/foreign/partial payload accepted")
			}
			if fault == "archive-limit" && DiagnosticCode(e) != "OFFLINE_ENGINE_RESPONSE_LIMIT" {
				t.Fatal("safe bounded archive diagnostic lost", DiagnosticCode(e))
			}
			if !legacy && state != "created" && fault == "none" {
				if _, e = client.InspectStoppedControlConfiguration(context.Background(), o, strings.Repeat("c", 64), target, 10033, 10034, data); e == nil {
					t.Fatal("legacy never-started contract weakened")
				}
			}
			if failed && fault == "none" {
				if _, e = client.InspectCurrentControlConfiguration(context.Background(), o, strings.Repeat("c", 64), target, 10033, 10034, data); e == nil {
					t.Fatal("clean reader admitted a failed control")
				}
			}
		})
	}
}

// configurationReadbackTar is an explicitly synthetic Docker archive response.
func configurationReadbackTar(t *testing.T, files []ConfigurationFile, target string, corrupt bool) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	root := path.Base(target)
	headers := []*tar.Header{
		{Name: root, Typeflag: tar.TypeDir, Mode: 0755},
		{Name: root + "/private", Typeflag: tar.TypeDir, Mode: 0700, Uid: 10033, Gid: 10034},
	}
	for _, header := range headers {
		if e := writer.WriteHeader(header); e != nil {
			t.Fatal(e)
		}
	}
	for index, file := range files {
		data := append([]byte(nil), file.Contents...)
		if corrupt && index == 0 {
			data[0] ^= 1
		}
		header := &tar.Header{Name: root + "/private/" + file.Name, Typeflag: tar.TypeReg, Mode: 0600, Uid: 10033, Gid: 10034, Size: int64(len(data))}
		if e := writer.WriteHeader(header); e != nil {
			t.Fatal(e)
		}
		if _, e := writer.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	return output.Bytes()
}

func TestStoppedControlConfigurationRejectsInvalidInputBeforeRequest(t *testing.T) {
	for _, fault := range []string{"owner", "role", "ID", "path", "traversal", "separator", "user", "files", "file-name"} {
		t.Run(fault, func(t *testing.T) {
			scope, object := stoppedControlFixture()
			op := strings.Repeat("d", 32)
			owner := Ownership{scope, op, TLSConfigurationRole + "-" + op}
			target, uid := "/run/projectone/control", 10033
			files := []ConfigurationFile{{Name: "key.pem", Contents: []byte("synthetic")}}
			switch fault {
			case "owner":
				owner.ScopeID = "foreign"
			case "role":
				owner.Role = "control-config"
			case "ID":
				object.ID = "invalid"
			case "path":
				target = "/etc"
			case "traversal":
				target = "/run/projectone/control/../foreign"
			case "separator":
				target = "/run/projectone/control\\foreign"
			case "user":
				uid = 0
			case "files":
				files = nil
			case "file-name":
				files[0].Name = "../key.pem"
			}
			client, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
			client.config.ResponseBytes = 1 << 20
			if _, e := client.InspectStoppedControlConfiguration(context.Background(), owner, object.ID, target, uid, 10034, files); e == nil || calls.Load() != 0 {
				t.Fatal("invalid input submitted", e)
			}
		})
	}
}
