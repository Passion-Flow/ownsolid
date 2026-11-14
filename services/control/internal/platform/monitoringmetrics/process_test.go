package monitoringmetrics

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This private test-only selector is injected solely into a newly exec'd copy of
// this exact test binary. It is never a production setting or existing PID input.
const processChildEnvironment = "PROJECT_ONE_MONITORING_PROCESS_CHILD"

func TestMonitoringOwnedProcessChild(t *testing.T) {
	if os.Getenv(processChildEnvironment) != "true" {
		return
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	r, e := New(Defaults(), nil)
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/snapshot", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(r.Snapshot())
	})
	mux.HandleFunc("/incomplete", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "owned-stream-started\n")
		http.NewResponseController(w).Flush()
		<-req.Context().Done()
	})
	server := &http.Server{Handler: r.Middleware(mux), ReadHeaderTimeout: time.Second, IdleTimeout: time.Second}
	defer server.Close()
	raw, _ := json.Marshal(map[string]string{"origin": "http://" + listener.Addr().String(), "epoch": r.Snapshot().Epoch})
	fmt.Println(string(raw))
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-t.Context().Done():
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		server.Shutdown(ctx)
	case e := <-done:
		if e != http.ErrServerClosed {
			t.Fatal(e)
		}
	}
}

type ownedChild struct {
	cmd           *exec.Cmd
	origin, epoch string
	waited        bool
}

func startOwnedProcess(t *testing.T) *ownedChild {
	t.Helper()
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestMonitoringOwnedProcessChild$", "-test.timeout=9s")
	cmd.Env = append(os.Environ(), processChildEnvironment+"=true")
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	child := &ownedChild{cmd: cmd}
	t.Cleanup(func() {
		if !child.waited {
			cmd.Process.Kill()
			cmd.Wait()
			child.waited = true
		}
		if cmd.ProcessState == nil {
			t.Error("owned process missing final wait state")
		}
	})
	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(io.LimitReader(stdout, 4096))
		if scanner.Scan() {
			line <- scanner.Text()
		} else {
			line <- ""
		}
	}()
	select {
	case value := <-line:
		var ready struct {
			Origin string `json:"origin"`
			Epoch  string `json:"epoch"`
		}
		if json.Unmarshal([]byte(value), &ready) != nil || !strings.HasPrefix(ready.Origin, "http://127.0.0.1:") || ready.Epoch == "" {
			t.Fatal("owned child readiness")
		}
		child.origin, child.epoch = ready.Origin, ready.Epoch
	case <-ctx.Done():
		t.Fatal("owned child readiness deadline")
	}
	return child
}
func TestOwnedProcessFailureHasRealPartialStreamAndRestartEpoch(t *testing.T) {
	first := startOwnedProcess(t)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, e := client.Get(first.origin + "/incomplete")
	if e != nil {
		t.Fatal(e)
	}
	reader := bufio.NewReader(resp.Body)
	line, e := reader.ReadString('\n')
	if e != nil || line != "owned-stream-started\n" {
		t.Fatal("real started stream", e)
	}
	if e = first.cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	if e = first.cmd.Wait(); e == nil {
		t.Fatal("killed process reported success")
	}
	first.waited = true
	if _, e = io.ReadAll(reader); e == nil {
		t.Fatal("truncated HTTP stream fabricated complete")
	}
	resp.Body.Close()
	second := startOwnedProcess(t)
	if first.epoch == second.epoch {
		t.Fatal("process restart reused epoch")
	}
	resp, e = client.Get(second.origin + "/snapshot")
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	var snap Snapshot
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&snap); e != nil || snap.Epoch != second.epoch || len(snap.Series) != 0 {
		t.Fatal("restart did not reset actual counters", e)
	}
	// This proves only the owned Go process/HTTP failure and reset boundary. It
	// neither retries a business task nor infers physical gateway/node release.
}
