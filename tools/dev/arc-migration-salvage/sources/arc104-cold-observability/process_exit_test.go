//go:build repository_integration

package cellclusters_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	cellhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/cellclusters/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/coldtrace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func diagnosticJSON(kind, id string, fields map[string]any) {
	fields["event"], fields["request_id"], fields["time"] = kind, id, time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}

type childReady struct {
	URL, Cluster, RequestID string
	Certificate             []byte
}

// Run in a child process so the observer can distinguish an exit from an HTTP error.
func TestARC104ControlExitChild(t *testing.T) {
	path := os.Getenv("ARC104_CHILD_READY_FILE")
	if path == "" {
		t.Skip("child process only")
	}
	x := setup(t, 50, 1)
	cluster := diagnosticSeed(t, x)
	cfg := x.account.DB.Config()
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "0"
	cfg.ConnConfig.RuntimeParams["application_name"] = "arc104-exit-child"
	cfg.ConnConfig.Tracer = coldtrace.Tracer{}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	account := *x.account
	account.DB = pool
	service := *x.s
	service.Identity = &account
	handler := cellhttp.Handler{Service: &service}
	lock, err := x.account.DB.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(t.Context())
	if _, err = lock.Exec(t.Context(), "LOCK TABLE cell_cluster_members IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bounded, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		ctx := coldtrace.Stage(coldtrace.Start(bounded, id), "member_read")
		page, status, err := handler.Handle(r.WithContext(ctx), x.admin, id)
		if err != nil {
			code, problemStatus := httpserver.PublicProblem(err)
			coldtrace.End(ctx, problemStatus, code)
			httpserver.Problem(w, id, err)
			return
		}
		coldtrace.End(ctx, status, "")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()
	data, err := json.Marshal(childReady{URL: server.URL, Cluster: cluster, RequestID: id, Certificate: server.Certificate().Raw})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path+".next", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path+".next", path); err != nil {
		t.Fatal(err)
	}
	<-t.Context().Done()
}

func awaitCondition(t *testing.T, name string, read func() bool) {
	t.Helper()
	end := time.Now().Add(12 * time.Second)
	for time.Now().Before(end) {
		if read() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("timed out observing", name)
}

func TestARC104ControlExitDiagnosis(t *testing.T) {
	root := t.TempDir()
	readyFile := filepath.Join(root, "ready.json")
	outputFile := filepath.Join(root, "child.log")
	log, err := os.OpenFile(outputFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestARC104ControlExitChild$", "-test.v", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "ARC104_CHILD_READY_FILE="+readyFile)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Kill()
		raw, _ := os.ReadFile(outputFile)
		fmt.Print(string(raw))
	}()
	var ready childReady
	awaitCondition(t, "child TLS readiness", func() bool {
		data, e := os.ReadFile(readyFile)
		return e == nil && json.Unmarshal(data, &ready) == nil && ready.RequestID != ""
	})
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ready.Certificate})) {
		t.Fatal("child trust")
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	responseDone := make(chan error, 1)
	go func() {
		response, e := client.Get(ready.URL + cellhttp.Root + "/" + ready.Cluster + "/members")
		if response != nil {
			response.Body.Close()
			responseDone <- fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
		} else {
			responseDone <- e
		}
	}()
	awaitCondition(t, "identified blocked member query", func() bool {
		data, _ := os.ReadFile(outputFile)
		for line := range strings.SplitSeq(string(data), "\n") {
			var event map[string]any
			if json.Unmarshal([]byte(line), &event) == nil && event["request_id"] == ready.RequestID && event["event"] == "sql_start" && event["phase"] == "member_select" {
				return true
			}
		}
		return false
	})
	diagnosticJSON("process_kill_requested", ready.RequestID, map[string]any{"pid": cmd.Process.Pid, "signal": "SIGKILL"})
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-waited:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var failed *exec.ExitError
	if !errors.As(err, &failed) {
		t.Fatal("expected process failure", err)
	}
	status := failed.ProcessState.Sys().(syscall.WaitStatus)
	if !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatal("unexpected wait status", status)
	}
	diagnosticJSON("process_exit", ready.RequestID, map[string]any{"pid": cmd.Process.Pid, "signal": "SIGKILL", "shell_exit": 128 + int(status.Signal())})
	select {
	case err = <-responseDone:
		var networkError interface{ Unwrap() error }
		if err == nil || !errors.As(err, &networkError) {
			t.Fatal("expected client transport failure without a response", err)
		}
		diagnosticJSON("client_transport_error", ready.RequestID, map[string]any{"http_status": nil, "error_type": fmt.Sprintf("%T", err)})
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
