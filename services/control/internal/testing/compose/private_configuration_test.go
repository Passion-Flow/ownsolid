package compose_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Uses the real static offline operator, not a fixture-only writer. The artifact
// identity and source ownership as presented by this Engine are explicit inputs.
func initializeSyntheticPrivateConfiguration(t *testing.T, source, volume, image, scopeID, operationID, fixtureID string) {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_COMPOSE_PRIVATE_INSTALLER")
	expectedBinary := os.Getenv("PROJECT_ONE_COMPOSE_PRIVATE_INSTALLER_SHA256")
	resolved, e := filepath.EvalSymlinks(binary)
	if e != nil || resolved != binary || !filepath.IsAbs(binary) || strings.ContainsAny(binary+source, ",\r\n") || len(expectedBinary) != 64 {
		t.Fatal("EXPLICIT_STATIC_PRIVATE_INSTALLER_REQUIRED")
	}
	readBinary := func() string {
		t.Helper()
		parent, e := os.Stat(filepath.Dir(binary))
		if e != nil || parent.Mode() != os.ModeDir|0700 {
			t.Fatal("PRIVATE_INSTALLER_PROTECTED_PARENT_REQUIRED")
		}
		ps, ok := parent.Sys().(*syscall.Stat_t)
		if !ok || int(ps.Uid) != os.Geteuid() {
			t.Fatal("PRIVATE_INSTALLER_PROTECTED_PARENT_REQUIRED")
		}
		f, e := os.OpenFile(binary, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			t.Fatal("PRIVATE_INSTALLER_UNAVAILABLE")
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || info.Mode() != 0555 || info.Size() < 1 || info.Size() > 64<<20 {
			t.Fatal("PRIVATE_INSTALLER_METADATA_REFUSED")
		}
		s, ok := info.Sys().(*syscall.Stat_t)
		if !ok || s.Nlink != 1 || int(s.Uid) != os.Geteuid() {
			t.Fatal("PRIVATE_INSTALLER_OWNER_REFUSED")
		}
		h := sha256.New()
		if _, e = io.Copy(h, io.LimitReader(f, (64<<20)+1)); e != nil {
			t.Fatal("PRIVATE_INSTALLER_READ_FAILED")
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	if readBinary() != expectedBinary {
		t.Fatal("PRIVATE_INSTALLER_HASH_MISMATCH")
	}
	var images []struct {
		ID           string `json:"Id"`
		Architecture string
		OS           string `json:"Os"`
	}
	raw, e := exec.Command("docker", "image", "inspect", image).Output()
	if e != nil || json.Unmarshal(raw, &images) != nil || len(images) != 1 || images[0].OS != "linux" {
		t.Fatal("PRIVATE_INSTALLER_EXACT_IMAGE_REQUIRED")
	}
	imageID := images[0].ID
	imageDigest, de := hex.DecodeString(strings.TrimPrefix(imageID, "sha256:"))
	if de != nil || len(imageDigest) != 32 || imageID != "sha256:"+hex.EncodeToString(imageDigest) {
		t.Fatal("PRIVATE_INSTALLER_EXACT_IMAGE_REQUIRED")
	}
	machine := elf.EM_NONE
	switch images[0].Architecture {
	case "amd64":
		machine = elf.EM_X86_64
	case "arm64":
		machine = elf.EM_AARCH64
	default:
		t.Fatal("PRIVATE_INSTALLER_ARCHITECTURE_UNSUPPORTED")
	}
	native, e := elf.Open(binary)
	if e != nil {
		t.Fatal("PRIVATE_INSTALLER_STATIC_ELF_REQUIRED")
	}
	if native.Class != elf.ELFCLASS64 || native.Data != elf.ELFDATA2LSB || native.Machine != machine || native.Type != elf.ET_EXEC {
		native.Close()
		t.Fatal("PRIVATE_INSTALLER_STATIC_ELF_REQUIRED")
	}
	for _, p := range native.Progs {
		if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
			native.Close()
			t.Fatal("PRIVATE_INSTALLER_STATIC_ELF_REQUIRED")
		}
	}
	native.Close()
	ownerLabels := map[string]string{"io.projectone.isolated-scope": scopeID, "io.projectone.operation": operationID, "io.projectone.role": "control-config", "io.projectone.purpose": "isolated-validation"}
	verifyVolume := func() {
		t.Helper()
		var volumes []struct {
			Name, Driver, Scope string
			Labels, Options     map[string]string
		}
		raw, e := exec.Command("docker", "volume", "inspect", volume).Output()
		if e != nil || json.Unmarshal(raw, &volumes) != nil || len(volumes) != 1 || volumes[0].Name != volume || volumes[0].Driver != "local" || volumes[0].Scope != "local" || len(volumes[0].Options) != 0 {
			t.Fatal("PRIVATE_CONFIGURATION_VOLUME_IDENTITY_REQUIRED")
		}
		for k, v := range ownerLabels {
			if volumes[0].Labels[k] != v {
				t.Fatal("PRIVATE_CONFIGURATION_VOLUME_OWNER_MISMATCH")
			}
		}
		raw, e = exec.Command("docker", "ps", "--all", "--quiet", "--filter", "volume="+volume).Output()
		if e != nil || len(bytes.TrimSpace(raw)) != 0 {
			t.Fatal("PRIVATE_CONFIGURATION_UNUSED_VOLUME_REQUIRED")
		}
	}
	parseSourceOwner := func(key string) int {
		value := os.Getenv(key)
		n, e := strconv.ParseInt(value, 10, 32)
		if e != nil || n < 0 || strconv.FormatInt(n, 10) != value {
			t.Fatal("EXPLICIT_PRIVATE_SOURCE_OWNER_REQUIRED")
		}
		return int(n)
	}
	files, e := os.ReadDir(source)
	if e != nil || len(files) == 0 {
		t.Fatal("PRIVATE_CONFIGURATION_SOURCE_REQUIRED")
	}
	inventory := []map[string]any{}
	for _, file := range files {
		info, e := file.Info()
		if e != nil || info.Mode() != 0600 {
			t.Fatal("PRIVATE_CONFIGURATION_SOURCE_METADATA_REFUSED")
		}
		raw, e := os.ReadFile(filepath.Join(source, file.Name()))
		if e != nil {
			t.Fatal("PRIVATE_CONFIGURATION_SOURCE_READ_FAILED")
		}
		digest := sha256.Sum256(raw)
		inventory = append(inventory, map[string]any{"name": file.Name(), "bytes": len(raw), "sha256": hex.EncodeToString(digest[:])})
	}
	sourceUID, sourceGID := parseSourceOwner("PROJECT_ONE_COMPOSE_PRIVATE_SOURCE_UID"), parseSourceOwner("PROJECT_ONE_COMPOSE_PRIVATE_SOURCE_GID")
	sourceMount := stagePrivateConfigurationSource(t, source, imageID, scopeID, operationID, fixtureID, images[0].Architecture, sourceUID, sourceGID, inventory)
	request := map[string]any{"format_version": 1, "scope_id": scopeID, "operation_id": operationID, "volume_name": volume, "source_root": "/source", "target_root": "/target", "source_uid": sourceUID, "source_gid": sourceGID, "uid": os.Getuid(), "gid": os.Getgid(), "maximum_file_bytes": 1048576, "maximum_total_bytes": 4194304, "timeout_seconds": 30, "files": inventory}
	body, e := json.Marshal(request)
	if e != nil {
		t.Fatal(e)
	}
	intent := filepath.Join(filepath.Dir(source), "private-configuration-intent.json")
	f, e := os.OpenFile(intent, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("PRIVATE_CONFIGURATION_NEW_INTENT_REQUIRED")
	}
	_, we := f.Write(append(body, '\n'))
	se := f.Sync()
	ce := f.Close()
	parent, e := os.Open(filepath.Dir(intent))
	if e != nil {
		t.Fatal("PRIVATE_CONFIGURATION_INTENT_DURABILITY_FAILED")
	}
	pe := parent.Sync()
	parent.Close()
	if we != nil || se != nil || ce != nil || pe != nil {
		t.Fatal("PRIVATE_CONFIGURATION_INTENT_DURABILITY_FAILED")
	}
	// Durable immutable intent precedes even the new volume creation. The caller
	// already inventoried this exact name for uncertain-effect recovery.
	if e = exec.Command("docker", "volume", "create", "--driver", "local", "--label", "projectone.owner=mvp-development", "--label", "projectone.scope="+fixtureID, "--label", "io.projectone.isolated-scope="+scopeID, "--label", "io.projectone.operation="+operationID, "--label", "io.projectone.role=control-config", "--label", "io.projectone.purpose=isolated-validation", volume).Run(); e != nil {
		t.Fatal("PRIVATE_CONFIGURATION_VOLUME_CREATE_UNCERTAIN")
	}
	verifyVolume()
	var fingerprint string
	for _, action := range []string{"install", "inspect"} {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		mount := "type=volume,src=" + volume + ",dst=/target,volume-nocopy"
		if action == "inspect" {
			mount += ",readonly"
		}
		args := []string{"run", "--rm", "--pull", "never", "--platform", "linux/" + images[0].Architecture, "--network", "none", "--user", "0:0", "--read-only", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges:true", "--memory", "128m", "--cpus", "1", "--pids-limit", "32", "--interactive", "--label", "io.projectone.isolated-scope=" + scopeID, "--label", "io.projectone.operation=" + operationID, "--mount", sourceMount, "--mount", mount, "--mount", "type=bind,src=" + binary + ",dst=/installer/projectone-operator,readonly", "--entrypoint", "/installer/projectone-operator", imageID, "compose-private-configuration", "--action", action}
		command := exec.CommandContext(ctx, "docker", args...)
		command.Stdin = bytes.NewReader(body)
		output, e := command.Output()
		cancel()
		var result struct {
			Status        string `json:"status"`
			ScopeID       string `json:"scope_id"`
			OperationID   string `json:"operation_id"`
			VolumeName    string `json:"volume_name"`
			RequestSHA256 string `json:"request_sha256"`
			Files         int    `json:"files"`
		}
		if e != nil || json.Unmarshal(output, &result) != nil {
			if exit, ok := e.(*exec.ExitError); ok {
				code := strings.TrimSpace(string(exit.Stderr))
				if len(code) <= 96 && strings.HasPrefix(code, "OFFLINE_") && !strings.ContainsAny(code, "\r\n /:") {
					t.Logf("Native private installer %s: %s", action, code)
				}
			}
			t.Fatal("NATIVE_PRIVATE_CONFIGURATION_FAILED_RETAIN_VOLUME_AND_INTENT")
		}
		expectedStatus := "verified_private_configuration"
		if action == "inspect" {
			expectedStatus = "observed_private_configuration"
		}
		if result.Status != expectedStatus || result.ScopeID != scopeID || result.OperationID != operationID || result.VolumeName != volume || len(result.RequestSHA256) != 64 || result.Files != len(inventory) {
			t.Fatal("NATIVE_PRIVATE_CONFIGURATION_READBACK_REFUSED")
		}
		if action == "install" {
			fingerprint = result.RequestSHA256
		} else if result.RequestSHA256 != fingerprint {
			t.Fatal("NATIVE_PRIVATE_CONFIGURATION_IDENTITY_CHANGED")
		}
		verifyVolume()
		t.Logf("Native private configuration %s verified: request=%s files=%d", action, result.RequestSHA256, result.Files)
	}
	if readBinary() != expectedBinary {
		t.Fatal("PRIVATE_INSTALLER_CHANGED")
	}
}
