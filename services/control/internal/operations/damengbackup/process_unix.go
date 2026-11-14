//go:build linux || darwin

package damengbackup

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Execution struct {
	Started             bool   `json:"started"`
	ProcessGroupID      int    `json:"process_group_id"`
	ProcessGroupStopped bool   `json:"process_group_stopped"`
	ExitCode            int    `json:"exit_code"`
	Cancelled           bool   `json:"cancelled"`
	OutputLimited       bool   `json:"output_limited"`
	OutputBytes         int64  `json:"output_bytes"`
	OutputSHA256        string `json:"output_sha256"`
	Code                string `json:"code"`
}
type boundedOutput struct {
	sync.Mutex
	data     []byte
	total    int64
	limit    int
	exceeded chan struct{}
	once     sync.Once
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	b.total += int64(len(p))
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		b.data = append(b.data, p[:min(remaining, len(p))]...)
	}
	if b.total > int64(b.limit) {
		b.once.Do(func() { close(b.exceeded) })
	}
	return len(p), nil
}

// runProcess owns a process group, bounds retained output, terminates on budget
// or cancellation and always waits. Raw vendor output is never returned in errors
// or journalled; only HELP is interpreted privately by the inventory validator.
func runProcess(ctx context.Context, tool string, args []string, dir string, env []string, p Policy, observe func() error) (result Execution, output []byte, err error) {
	result.ExitCode = -1
	if ctx == nil || ctx.Err() != nil {
		result.Cancelled = true
		result.Code = "CANCELLED_BEFORE_START"
		return result, nil, fail(result.Code)
	}
	cmd := exec.Command(tool, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Duration(p.TerminationGraceSeconds) * time.Second
	buffer := &boundedOutput{limit: p.MaximumOutputBytes, exceeded: make(chan struct{})}
	cmd.Stdout = buffer
	cmd.Stderr = buffer
	if cmd.Start() != nil {
		result.Code = "START_FAILED"
		return result, nil, fail(result.Code)
	}
	result.Started = true
	result.ProcessGroupID = cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(time.Duration(p.ObservationMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	code := ""
	var wait error
	for code == "" {
		select {
		case wait = <-done:
			code = "JOINED"
		case <-ctx.Done():
			result.Cancelled = true
			code = "CANCELLED"
		case <-buffer.exceeded:
			result.OutputLimited = true
			code = "OUTPUT_LIMIT"
		case <-ticker.C:
			if observe != nil && observe() != nil {
				code = "RESOURCE_OR_CUSTODY_LOST"
			}
		}
	}
	if code != "JOINED" {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(time.Duration(p.TerminationGraceSeconds) * time.Second)
		select {
		case wait = <-done:
			timer.Stop()
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			wait = <-done
		}
	}
	// Also stop any child still in this group after the original process exits.
	// No unjoined worker may continue writing into an operation root.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	stopDeadline := time.Now().Add(time.Duration(p.TerminationGraceSeconds) * time.Second)
	for {
		result.ProcessGroupStopped = processGroupStopped(cmd.Process.Pid)
		if result.ProcessGroupStopped || !time.Now().Before(stopDeadline) {
			break
		}
		time.Sleep(time.Duration(p.ObservationMilliseconds) * time.Millisecond)
	}
	if !result.ProcessGroupStopped {
		code = "PROCESS_GROUP_STOP_UNCERTAIN"
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	buffer.Lock()
	output = append([]byte(nil), buffer.data...)
	result.OutputBytes = buffer.total
	buffer.Unlock()
	result.OutputSHA256 = sha(output)
	if result.OutputBytes > int64(p.MaximumOutputBytes) {
		result.OutputLimited = true
		if code == "JOINED" {
			code = "OUTPUT_LIMIT"
		}
	}
	if ctx.Err() != nil {
		result.Cancelled = true
		if code == "JOINED" {
			code = "CANCELLED"
		}
	}
	if code == "JOINED" && wait == nil {
		result.Code = "JOINED_SUCCESS"
		return result, output, nil
	}
	if code == "JOINED" {
		code = "JOINED_FAILURE"
	}
	result.Code = code
	return result, output, fail(code)
}
