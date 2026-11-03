//go:build knowledge_encoder_integration

package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const crashHelperConfiguration = "PROJECT_ONE_KNOWLEDGE_CRASH_HELPER_CONFIG"

type crashConfiguration struct {
	Knowledge config.KnowledgeConfiguration
	Policy    config.KnowledgePolicy
}

func TestMain(main *testing.M) {
	if selected := os.Getenv(crashHelperConfiguration); selected != "" {
		raw, e := os.ReadFile(selected)
		var configuration crashConfiguration
		if e != nil || json.Unmarshal(raw, &configuration) != nil {
			os.Exit(2)
		}
		processor, e := NewProcessor(configuration.Knowledge, configuration.Policy)
		if e != nil {
			os.Exit(2)
		}
		_, _ = processor.Execute(context.Background(), "Explicit long-running crash worker double.", false, configuration.Policy.ChunkTokens, configuration.Policy.OverlapTokens)
		processor.Close()
		os.Exit(2)
	}
	os.Exit(main.Run())
}
func guardedFaultWorker(t *testing.T, c config.KnowledgeConfiguration) string {
	t.Helper()
	directory := t.TempDir()
	sourceRoot, _ := json.Marshal(filepath.Dir(c.WorkerFile))
	worker := filepath.Join(directory, "guarded_fault_double.py")
	program := "import os,sys,ctypes\nfrom pathlib import Path\nsys.path.insert(0," + string(sourceRoot) + ")\nfrom projectone_knowledge.process_guard import arm\narm()\nPath(__file__).with_suffix('.pid').write_text(str(os.getpid()))\nctypes.PyDLL(None).sleep(300)\n"
	if os.WriteFile(worker, []byte(program), 0600) != nil {
		t.Fatal("guarded fault program")
	}
	return worker
}
func processStopped(pid int) bool {
	if e := syscall.Kill(pid, 0); errors.Is(e, syscall.ESRCH) {
		return true
	}
	status, e := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return e == nil && strings.HasPrefix(strings.TrimSpace(string(status)), "Z")
}
func TestM12ActualParentSIGKILLStopsGuardedChildAndReclaimsOnlyOwnedScratch(t *testing.T) {
	original, c := actualProcessor(t)
	original.Close()
	worker := guardedFaultWorker(t, c)
	c.WorkerFile = worker
	configuration := crashConfiguration{Knowledge: c, Policy: config.DefaultKnowledgePolicy()}
	raw, _ := json.Marshal(configuration)
	file := filepath.Join(t.TempDir(), "helper.json")
	if os.WriteFile(file, raw, 0600) != nil {
		t.Fatal("helper configuration")
	}
	command := exec.Command(os.Args[0])
	command.Env = append(os.Environ(), crashHelperConfiguration+"="+file)
	var log bytes.Buffer
	command.Stdout = &log
	command.Stderr = &log
	if e := command.Start(); e != nil {
		t.Fatal(e)
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	pidFile := strings.TrimSuffix(worker, filepath.Ext(worker)) + ".pid"
	deadline := time.Now().Add(5 * time.Second)
	var pid int
	for time.Now().Before(deadline) {
		if bytes, e := os.ReadFile(pidFile); e == nil {
			pid, _ = strconv.Atoi(string(bytes))
			if pid > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid < 1 {
		t.Fatal("actual guarded child did not start")
	}
	entries, e := os.ReadDir(c.ScratchRoot)
	if e != nil || len(entries) != 1 {
		t.Fatal("owned operation marker missing", e)
	}
	if e = command.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = command.Wait()
	waited = true
	deadline = time.Now().Add(5 * time.Second)
	for !processStopped(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !processStopped(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("orphan child survived its expected parent")
	}
	recovered, e := NewProcessor(c, configuration.Policy)
	if e != nil {
		t.Fatal("marked orphan recovery", e)
	}
	defer recovered.Close()
	entries, e = os.ReadDir(c.ScratchRoot)
	if e != nil || len(entries) != 0 {
		t.Fatal("marked orphan scratch not reclaimed", e)
	}
	unrelated := filepath.Join(c.ScratchRoot, "unrelated-design.txt")
	if os.WriteFile(unrelated, []byte("SYNTHETIC_UNRELATED_ASSET"), 0600) != nil {
		t.Fatal("fixture asset")
	}
	candidate, e := NewProcessor(c, configuration.Policy)
	if candidate != nil {
		candidate.Close()
	}
	if e == nil || e.Error() != "KNOWLEDGE_SCRATCH_ROOT_INVALID" {
		t.Fatal("unrecognized scratch was adopted", e)
	}
	preserved, e := os.ReadFile(unrelated)
	if e != nil || string(preserved) != "SYNTHETIC_UNRELATED_ASSET" {
		t.Fatal("unrelated asset changed", e)
	}
	if os.Remove(unrelated) != nil {
		t.Fatal("remove only the explicitly created fixture")
	}
	live, e := recovered.createScratch()
	if e != nil {
		t.Fatal(e)
	}
	defer recovered.removeScratch(live)
	if e = recovered.CleanupScratch(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(live); e != nil {
		t.Fatal("live owner scratch was removed", e)
	}
}
func TestM12ActualIndependentChildWallWatchdog(t *testing.T) {
	p, c := actualProcessor(t)
	worker := guardedFaultWorker(t, c)
	command := exec.Command(c.PythonExecutable, "-I", "-B", worker)
	command.Env = []string{"PATH=/usr/bin:/bin", p.contract.ProcessControl.ParentPIDEnvironment + "=" + strconv.Itoa(os.Getpid()), p.contract.ProcessControl.WallSecondsEnvironment + "=1", p.contract.ProcessControl.PollMillisecondsEnvironment + "=100"}
	var log bytes.Buffer
	command.Stdout = &log
	command.Stderr = &log
	started := time.Now()
	e := command.Run()
	var exited *exec.ExitError
	status, signaled := exitedStatus(e)
	if !errors.As(e, &exited) || !signaled || !status.Signaled() || status.Signal() != syscall.SIGKILL || time.Since(started) > 4*time.Second {
		t.Fatal("independent child deadline did not terminate the real process")
	}
}

func exitedStatus(err error) (syscall.WaitStatus, bool) {
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		return 0, false
	}
	status, ok := exited.Sys().(syscall.WaitStatus)
	return status, ok
}
