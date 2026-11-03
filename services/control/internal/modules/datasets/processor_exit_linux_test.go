//go:build dataset_processor_integration

package datasets

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These are real CPython children. Only delivery of the owned Cmd.Wait receipt
// is controlled; no production sampler or global mutable test hook is replaced.
func processorExitLinuxCommand(t *testing.T, body string) *exec.Cmd {
	t.Helper()
	p, _ := nativeProcessor(t)
	command := exec.Command(p.configuration.PythonExecutable, "-I", "-B", "-c", body)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	return command
}

func processorExitLinuxZombie(t *testing.T, command *exec.Cmd, budget time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(budget)
	for {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(command.Process.Pid) + "/status")
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "State:") && len(strings.Fields(line)) >= 2 && strings.Fields(line)[1] == "Z" {
				if strings.Contains(string(raw), "VmRSS:") {
					t.Fatal("actual zombie unexpectedly retained VmRSS")
				}
				t.Logf("actual CPython pid=%d State=Z; VmRSS absent", command.Process.Pid)
				return raw
			}
		}
		if !time.Now().Before(deadline) {
			t.Fatal("actual CPython did not reach zombie state within fixture budget")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLinuxProcessorExitReceiptActualRapidCPython(t *testing.T) {
	for _, state := range []string{"zombie", "reaped", "nonzero"} {
		t.Run(state, func(t *testing.T) {
			p, _ := nativeProcessor(t)
			body := "pass"
			if state == "nonzero" {
				body = "raise SystemExit(7)"
			}
			command := processorExitLinuxCommand(t, body)
			waited := false
			t.Cleanup(func() {
				if !waited {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})
			var actualExit error
			if state == "zombie" {
				processorExitLinuxZombie(t, command, time.Duration(p.policy.ChildStopSeconds)*time.Second)
			} else {
				actualExit = command.Wait()
				waited = true
				if _, err := os.Stat("/proc/" + strconv.Itoa(command.Process.Pid) + "/status"); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("reaped owned PID still present", err)
				}
				t.Logf("actual CPython pid=%d reaped; /proc status absent", command.Process.Pid)
			}
			_, sampleError := childResidentBytes(context.Background(), command.Process.Pid, time.Duration(p.policy.MemoryPollMilliseconds)*time.Millisecond)
			if sampleError == nil {
				t.Fatal("terminal child produced a false RSS sample")
			}
			done := make(chan error, 1)
			wall := time.NewTimer(time.Duration(p.policy.Resources.WallSeconds) * time.Second)
			defer wall.Stop()
			type outcome struct{ exit, monitor error }
			result := make(chan outcome, 1)
			go func() {
				x, err := processorMemoryExitReceipt(context.Background(), done, sampleError, wall.C, time.Duration(p.policy.ChildStopSeconds)*time.Second)
				result <- outcome{x, err}
			}()
			// Hold delivery across a complete configured probe interval. Old code
			// immediately reports MEMORY_MONITOR_FAILED despite the actual exit.
			hold := time.NewTimer(time.Duration(p.policy.MemoryPollMilliseconds) * time.Millisecond)
			defer hold.Stop()
			select {
			case got := <-result:
				t.Fatalf("terminal sample resolved before its owned Wait receipt: %v / %v", got.exit, got.monitor)
			case <-hold.C:
			}
			if !waited {
				actualExit = command.Wait()
				waited = true
			}
			if state != "nonzero" && actualExit != nil {
				t.Fatal("actual rapid CPython did not exit successfully", actualExit)
			}
			done <- actualExit
			select {
			case got := <-result:
				if got.monitor != nil || got.exit != actualExit {
					t.Fatalf("owned exit receipt changed or terminal sample rejected: %v / %v", got.exit, got.monitor)
				}
				if state == "nonzero" {
					var code *exec.ExitError
					if !errors.As(got.exit, &code) || code.ExitCode() != 7 {
						t.Fatal("nonzero CPython status became success", got.exit)
					}
				}
			case <-wall.C:
				t.Fatal("owned Wait receipt was not joined")
			}
		})
	}
}

func TestLinuxProcessorMemoryStateRemainsFailClosed(t *testing.T) {
	for _, value := range []struct {
		name, raw string
		resident  int64
		terminal  bool
		invalid   bool
	}{
		{"live", "State:\tS (sleeping)\nVmRSS:\t123 kB\n", 123 * 1024, false, false},
		{"zombie", "State:\tZ (zombie)\n", 0, true, false},
		{"dead", "State:\tX (dead)\n", 0, true, false},
		{"live_missing_rss", "State:\tS (sleeping)\n", 0, false, true},
		{"running_missing_rss", "State:\tR (running)\n", 0, false, true},
		{"unknown_state", "State:\t?\n", 0, false, true},
		{"empty", "", 0, false, true},
		{"negative_rss", "State:\tS (sleeping)\nVmRSS:\t-1 kB\n", 0, false, true},
		{"overflow_rss", "State:\tS (sleeping)\nVmRSS:\t9007199254740992 kB\n", 0, false, true},
		{"wrong_units", "State:\tS (sleeping)\nVmRSS:\t123 MB\n", 0, false, true},
		{"zombie_malformed_rss", "State:\tZ (zombie)\nVmRSS:\t123 MB\n", 0, false, true},
		{"malformed_rss", "State:\tS (sleeping)\nVmRSS:\tinvalid kB\n", 0, false, true},
	} {
		t.Run(value.name, func(t *testing.T) {
			resident, err := linuxChildResidentBytes([]byte(value.raw))
			if resident != value.resident || errors.Is(err, errProcessorExitSampleUnavailable) != value.terminal || (!value.terminal && (err != nil) != value.invalid) {
				t.Fatalf("invalid memory classification: resident=%d error=%v", resident, err)
			}
			if value.invalid {
				// Even a buffered successful receipt cannot forgive a live or
				// malformed sample. The production caller still joins its child.
				done := make(chan error, 1)
				done <- nil
				_, resolved := processorMemoryExitReceipt(context.Background(), done, err, nil, time.Second)
				if resolved == nil || resolved.Error() != "DATASET_PROCESSOR_MEMORY_MONITOR_FAILED" || len(done) != 1 {
					t.Fatal("live sampling failure was swallowed or consumed a receipt", resolved)
				}
			}
		})
	}
	p, _ := nativeProcessor(t)
	command := processorExitLinuxCommand(t, "import signal;signal.pause()")
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	joined := false
	t.Cleanup(func() {
		if !joined {
			stopChild(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
		}
	})
	deadline := time.Now().Add(time.Duration(p.policy.ChildStopSeconds) * time.Second)
	for {
		resident, err := childResidentBytes(context.Background(), command.Process.Pid, time.Duration(p.policy.MemoryPollMilliseconds)*time.Millisecond)
		if err == nil && resident > 0 {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("actual live CPython did not supply an RSS sample", err)
		}
		time.Sleep(time.Millisecond)
	}
	unavailable := linuxChildSampleError(command.Process.Pid, os.ErrNotExist)
	if unavailable == nil || errors.Is(unavailable, errProcessorExitSampleUnavailable) {
		t.Fatal("missing proc observation classified an actual live PID as exited", unavailable)
	}
	for _, sample := range []error{os.ErrPermission, unavailable} {
		_, err := processorMemoryExitReceipt(context.Background(), done, sample, nil, time.Duration(p.policy.ChildStopSeconds)*time.Second)
		if err == nil || err.Error() != "DATASET_PROCESSOR_MEMORY_MONITOR_FAILED" {
			t.Fatal("actual live child sampling error was forgiven", err)
		}
	}
	stopChild(command, done, time.Duration(p.policy.ChildStopSeconds)*time.Second)
	joined = true
	if err := syscall.Kill(command.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatal("failed live monitor did not join its owned child", err)
	}
	if !errors.Is(linuxChildSampleError(command.Process.Pid, os.ErrNotExist), errProcessorExitSampleUnavailable) {
		t.Fatal("actually reaped PID did not permit bounded receipt reconciliation")
	}
}

func TestLinuxProcessorExitReceiptBudgetCancellationAndWall(t *testing.T) {
	p, _ := nativeProcessor(t)
	command := processorExitLinuxCommand(t, "pass")
	actualExit := command.Wait()
	if actualExit != nil {
		t.Fatal(actualExit)
	}
	_, terminal := childResidentBytes(context.Background(), command.Process.Pid, time.Duration(p.policy.MemoryPollMilliseconds)*time.Millisecond)
	if !errors.Is(terminal, errProcessorExitSampleUnavailable) {
		t.Fatal("actual reaped child was not terminal", terminal)
	}
	for _, kind := range []string{"missing_receipt", "cancelled", "wall"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wall := make(chan time.Time, 1)
			want := "DATASET_PROCESSOR_MEMORY_MONITOR_FAILED"
			if kind == "cancelled" {
				cancel()
				want = "DATASET_JOB_CANCELLED"
			}
			if kind == "wall" {
				wall <- time.Now()
				want = "DATASET_PROCESSOR_TIMEOUT"
			}
			started := time.Now()
			// The default typed stop budget is deliberately shortened to its
			// permitted minimum in this owned test, without changing production.
			p.policy.ChildStopSeconds = 1
			_, err := processorMemoryExitReceipt(ctx, make(chan error, 1), terminal, wall, time.Duration(p.policy.ChildStopSeconds)*time.Second)
			if err == nil || err.Error() != want {
				t.Fatalf("%s lost its failure classification: %v", kind, err)
			}
			if time.Since(started) > time.Duration(p.policy.ChildStopSeconds)*time.Second+time.Duration(p.policy.MemoryPollMilliseconds)*time.Millisecond {
				t.Fatal("terminal observation waited beyond the configured receipt budget")
			}
		})
	}
}
