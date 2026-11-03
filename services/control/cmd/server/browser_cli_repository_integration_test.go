//go:build repository_integration && fullserve_browser_integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/healthprobe"
)

type liveCLIProbeOutput struct{ bytes.Buffer }

func (w *liveCLIProbeOutput) Write(raw []byte) (int, error) {
	if w.Len()+len(raw) > 1024 {
		return 0, io.ErrShortWrite
	}
	return w.Buffer.Write(raw)
}

type liveCLIPhase struct {
	Phase                  string   `json:"phase"`
	PID                    int      `json:"pid"`
	BinarySHA256           string   `json:"binary_sha256"`
	ConfigurationSHA256    string   `json:"configuration_sha256"`
	Status                 string   `json:"status"`
	HealthAudiences        []string `json:"health_audiences"`
	RuntimeRoleConnections int      `json:"runtime_role_connections"`
	ExitCode               *int     `json:"exit_code"`
	Forced                 bool     `json:"forced"`
	Joined                 bool     `json:"joined"`
}

func liveCLIEnvironment() []string {
	var values []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "PG") || strings.HasPrefix(key, "MYSQL") || strings.HasPrefix(key, "DOCKER_") || strings.HasPrefix(key, "PROJECT_ONE_REPOSITORY_") || strings.HasPrefix(key, "PROJECT_ONE_TEST_") || key == "PROJECT_ONE_FULLSERVE_BROWSER_INPUT" {
			continue
		}
		values = append(values, value)
	}
	return values
}

func liveWriteBrowserEvidence(t *testing.T, file string, raw []byte) {
	t.Helper()
	pending, e := os.CreateTemp(filepath.Dir(file), ".native-evidence-")
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_PHASE_WRITE_FAILED")
	}
	_, writeError := pending.Write(raw)
	syncError, closeError := pending.Sync(), pending.Close()
	if writeError != nil || syncError != nil || closeError != nil {
		// Retain only this private diagnostic temporary file; never promote it.
		t.Fatal("LIVE_BROWSER_NATIVE_PHASE_WRITE_FAILED")
	}
	if os.Rename(pending.Name(), file) != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_PHASE_REPLACE_FAILED")
	}
	dir, e := os.Open(filepath.Dir(file))
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_PHASE_SYNC_FAILED")
	}
	syncError, closeError = dir.Sync(), dir.Close()
	if syncError != nil || closeError != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_PHASE_SYNC_FAILED")
	}
}

func liveCLIWritePhase(t *testing.T, file string, phase liveCLIPhase) {
	t.Helper()
	raw, e := json.Marshal(phase)
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_PHASE_WRITE_FAILED")
	}
	liveWriteBrowserEvidence(t, file, raw)
}

func liveCLIRoleConnections(t *testing.T, x *serverCompositionStory, role string) int {
	t.Helper()
	var count int
	if e := x.f.PostgreSQL.QueryRow(x.ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename=$1", role).Scan(&count); e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_ROLE_OBSERVATION_FAILED")
	}
	return count
}

func liveCLIHTTPStart(t *testing.T, x *serverCompositionStory, input liveAccountBrowserInput, configuration, role, phaseName string) (func(), int) {
	t.Helper()
	if phaseName != "initial" && phaseName != "restarted" {
		t.Fatal("LIVE_BROWSER_NATIVE_PHASE_INVALID")
	}
	if x.d.postgres != nil || x.d.pgLease != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_GO_POOL_MUST_BE_CLOSED")
	}
	if liveBrowserFileSHA(t, input.ControlFile) != input.ControlSHA256 {
		t.Fatal("LIVE_BROWSER_NATIVE_BINARY_CHANGED")
	}
	deadline := time.Now().Add(5 * time.Second)
	for liveCLIRoleConnections(t, x, role) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("LIVE_BROWSER_PRIOR_RUNTIME_CONNECTIONS_REMAIN")
		}
		time.Sleep(25 * time.Millisecond)
	}
	logFile := filepath.Join(input.ResultDirectory, "native-"+phaseName+".log")
	log, e := os.OpenFile(logFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("LIVE_BROWSER_NATIVE_PRIVATE_LOG_FAILED")
	}
	writer := &liveBrowserBoundedLog{file: log, remaining: input.MaximumLogBytes}
	command := exec.Command(input.ControlFile, "serve", "--config", configuration)
	if input.OfflineSandboxFile != "" {
		if liveBrowserFileSHA(t, input.OfflineSandboxFile) != input.OfflineSandboxSHA256 {
			t.Fatal("LIVE_BROWSER_OFFLINE_POLICY_CHANGED")
		}
		command = exec.Command("/usr/bin/sandbox-exec", "-f", input.OfflineSandboxFile, input.ControlFile, "serve", "--config", configuration)
	}
	command.Env = liveCLIEnvironment()
	command.Stdout, command.Stderr = writer, writer
	command.WaitDelay = 2 * time.Second
	if e = command.Start(); e != nil {
		log.Close()
		t.Fatal("LIVE_BROWSER_NATIVE_START_FAILED")
	}
	phase := liveCLIPhase{Phase: phaseName, PID: command.Process.Pid, BinarySHA256: input.ControlSHA256, ConfigurationSHA256: liveBrowserFileSHA(t, configuration), Status: "started_not_accepted"}
	phaseFile := filepath.Join(input.ResultDirectory, "native-"+phaseName+".json")
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stopped := false
	finish := func(result error) {
		phase.Joined = true
		code := command.ProcessState.ExitCode()
		phase.ExitCode = &code
		phase.Status = "stopped_failed"
		if result == nil && code == 0 && !phase.Forced {
			phase.Status = "stopped_clean"
		} else {
			t.Error("LIVE_BROWSER_NATIVE_EXIT_FAILED")
		}
		syncError, closeError := log.Sync(), log.Close()
		if syncError != nil || closeError != nil {
			t.Error("LIVE_BROWSER_NATIVE_LOG_SYNC_FAILED")
		}
		liveCLIWritePhase(t, phaseFile, phase)
	}
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if e := command.Process.Signal(syscall.SIGTERM); e != nil && !errors.Is(e, os.ErrProcessDone) {
			t.Error("LIVE_BROWSER_NATIVE_SIGNAL_FAILED")
		}
		select {
		case e := <-done:
			finish(e)
		case <-time.After(20 * time.Second):
			phase.Forced = true
			t.Error("LIVE_BROWSER_NATIVE_SHUTDOWN_TIMEOUT")
			_ = command.Process.Kill()
			select {
			case e := <-done:
				finish(e)
			case <-time.After(5 * time.Second):
				t.Error("LIVE_BROWSER_NATIVE_JOIN_TIMEOUT")
			}
		}
	}
	t.Cleanup(stop)
	liveCLIWritePhase(t, phaseFile, phase)
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	portal, _ := url.Parse(x.c.Server.PortalOrigin)
	ready := false
	for !ready {
		select {
		case <-timer.C:
			t.Fatal("LIVE_BROWSER_NATIVE_STARTUP_TIMEOUT")
		case e := <-done:
			stopped = true
			finish(e)
			t.Fatal("LIVE_BROWSER_NATIVE_STARTUP_REFUSED")
		case <-ticker.C:
			request, _ := http.NewRequestWithContext(x.ctx, "GET", "http://"+x.c.Server.ListenAddress+"/health/ready", nil)
			request.Host = portal.Host
			response, e := client.Do(request)
			if e != nil {
				continue
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			ready = response.StatusCode == http.StatusOK
		}
	}
	for _, audience := range []string{"portal", "admin"} {
		ctx, cancel := context.WithTimeout(x.ctx, 5*time.Second)
		probe := exec.CommandContext(ctx, input.ControlFile, healthprobe.Command, "--config", configuration, "--audience", audience, "--kind", "ready", "--dial-address", x.c.Server.ListenAddress, "--timeout-seconds", "2")
		probe.Env = liveCLIEnvironment()
		var output liveCLIProbeOutput
		probe.Stdout, probe.Stderr = &output, &output
		e := probe.Run()
		cancel()
		if e != nil || output.Len() > 1024 || strings.TrimSpace(output.String()) != healthprobe.Success {
			t.Fatal("LIVE_BROWSER_NATIVE_HEALTH_PROBE_FAILED")
		}
		phase.HealthAudiences = append(phase.HealthAudiences, audience)
	}
	phase.RuntimeRoleConnections = liveCLIRoleConnections(t, x, role)
	if phase.RuntimeRoleConnections < 1 {
		t.Fatal("LIVE_BROWSER_NATIVE_RESTRICTED_CONNECTIONS_MISSING")
	}
	phase.Status = "ready_not_accepted"
	liveCLIWritePhase(t, phaseFile, phase)
	readyFile := filepath.Join(input.ResultDirectory, "native-"+phaseName+"-ready.json")
	if _, e := os.Lstat(readyFile); !os.IsNotExist(e) {
		t.Fatal("LIVE_BROWSER_NATIVE_READY_RECEIPT_EXISTS")
	}
	liveCLIWritePhase(t, readyFile, phase)
	return stop, command.Process.Pid
}
