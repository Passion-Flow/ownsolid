package boundary

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type childConfiguration struct {
	DSNFile, Tenant, Schema, Origin, SigningFile, SigningID, LoginUser, LoginName, CertificateFile, CertificateKeyFile string
	PublicFiles, CodeFiles, BearerFiles                                                                                map[string]string
	CodeActive, BearerActive                                                                                           string
	Policy                                                                                                             providerPolicy
	Crypto                                                                                                             cryptoPolicy
}

func loadChild(path string) (childConfiguration, error) {
	var c childConfiguration
	raw, err := readPrivateFile(path, 32768)
	if err != nil {
		return c, errConfig
	}
	defer clear(raw)
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return childConfiguration{}, errConfig
	}
	return c, nil
}
func fixturePool(ctx context.Context, c childConfiguration) (*pgxpool.Pool, error) {
	raw, err := readPrivateFile(c.DSNFile, 16384)
	if err != nil {
		return nil, errConfig
	}
	defer clear(raw)
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port < 1024 || cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") || c.Policy.MaxConnections < 2 || c.Policy.MaxConnections > 8 {
		return nil, errConfig
	}
	cfg.ConnConfig.ConnectTimeout = c.Policy.Operation
	cfg.ConnConfig.RuntimeParams["search_path"] = c.Schema
	cfg.ConnConfig.RuntimeParams["application_name"] = "iam106-private-op"
	cfg.MaxConns = int32(c.Policy.MaxConnections)
	cfg.MaxConnIdleTime = c.Policy.Operation
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errConfig
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errConfig
	}
	return pool, nil
}
func loadRing(files map[string]string) ([]cryptoKey, error) {
	if len(files) < 1 || len(files) > 3 {
		return nil, errConfig
	}
	var keys []cryptoKey
	for id, path := range files {
		raw, err := readPrivateFile(path, 32)
		if err != nil || len(raw) != 32 {
			return nil, errConfig
		}
		var value [32]byte
		copy(value[:], raw)
		clear(raw)
		keys = append(keys, cryptoKey{ID: id, Value: value})
	}
	return keys, nil
}
func TestOPChildServer(t *testing.T) {
	path := os.Getenv("IAM_OP_CHILD_CONFIGURATION")
	if path == "" {
		t.Skip("private process worker selected by parent protocol fixture")
	}
	c, err := loadChild(path)
	if err != nil {
		t.Fatal("private child configuration refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Policy.Operation)
	defer cancel()
	pool, err := fixturePool(ctx, c)
	if err != nil {
		t.Fatal("private child database refused")
	}
	defer pool.Close()
	codeKeys, err := loadRing(c.CodeFiles)
	if err != nil {
		t.Fatal("private code keys refused")
	}
	bearerKeys, err := loadRing(c.BearerFiles)
	if err != nil {
		t.Fatal("private bearer keys refused")
	}
	crypto, err := newProviderCrypto(c.Crypto, c.CodeActive, codeKeys, c.BearerActive, bearerKeys)
	if err != nil {
		t.Fatal("purpose configuration refused")
	}
	store := &sdkStore{pool: pool, Tenant: c.Tenant, Origin: c.Origin, SigningFile: c.SigningFile, Policy: c.Policy, publicFiles: c.PublicFiles, signingID: c.SigningID}
	handler, err := newFixtureProvider(store, crypto, c.LoginUser, c.LoginName)
	if err != nil {
		t.Fatal("private provider configuration refused")
	}
	cert, err := readPrivateFile(c.CertificateFile, 32768)
	if err != nil {
		t.Fatal("private server certificate refused")
	}
	key, err := readPrivateFile(c.CertificateKeyFile, 32768)
	if err != nil {
		t.Fatal("private server key refused")
	}
	pair, err := tls.X509KeyPair(cert, key)
	clear(key)
	if err != nil {
		t.Fatal("private TLS pair refused")
	}
	file := os.NewFile(3, "inherited-private-listener")
	listener, err := net.FileListener(file)
	file.Close()
	if err != nil {
		t.Fatal("private listener refused")
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: c.Policy.Operation, ReadTimeout: c.Policy.Operation, WriteTimeout: c.Policy.Operation, IdleTimeout: 2 * c.Policy.Operation, MaxHeaderBytes: c.Policy.MaxHeaders, TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(tls.NewListener(listener, server.TLSConfig)) }()
	ready := os.NewFile(4, "private-ready-notification")
	if _, err = ready.Write([]byte("ready\n")); err != nil {
		t.Fatal("private readiness pipe failed")
	}
	ready.Close()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	select {
	case <-stop:
	case err = <-finished:
		if err != http.ErrServerClosed {
			t.Fatal("private server stopped unexpectedly")
		}
		return
	}
	shutdown, done := context.WithTimeout(context.Background(), c.Policy.Operation)
	defer done()
	if server.Shutdown(shutdown) != nil {
		server.Close()
		t.Fatal("private server shutdown exceeded bound")
	}
	if err = <-finished; err != http.ErrServerClosed {
		t.Fatal("private server shutdown failed")
	}
}

type childProcess struct {
	command  *exec.Cmd
	finished chan error
	log      *os.File
}

func startChild(t *testing.T, listener *net.TCPListener, configuration, folder string, timeout time.Duration) *childProcess {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("private test executable unavailable")
	}
	socket, err := listener.File()
	if err != nil {
		t.Fatal("owned listener duplication failed")
	}
	defer socket.Close()
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal("private readiness pipe unavailable")
	}
	defer readyRead.Close()
	log, err := os.OpenFile(filepath.Join(folder, "child-"+time.Now().UTC().Format("150405.000000000")+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		readyWrite.Close()
		t.Fatal("private child log unavailable")
	}
	command := exec.Command(executable, "-test.run=^TestOPChildServer$", "-test.timeout=120s", "-test.v")
	command.Env = append(os.Environ(), "IAM_OP_CHILD_CONFIGURATION="+configuration)
	command.ExtraFiles = []*os.File{socket, readyWrite}
	command.Stdout, command.Stderr = log, log
	if err = command.Start(); err != nil {
		readyWrite.Close()
		log.Close()
		t.Fatal("private child process did not start")
	}
	readyWrite.Close()
	child := &childProcess{command: command, finished: make(chan error, 1), log: log}
	go func() { child.finished <- command.Wait() }()
	readyRead.SetReadDeadline(time.Now().Add(timeout))
	var message [6]byte
	if _, err = io.ReadFull(readyRead, message[:]); err != nil || string(message[:]) != "ready\n" {
		stopChild(t, child, timeout)
		t.Fatal("private child did not become ready")
	}
	return child
}
func stopChild(t *testing.T, child *childProcess, timeout time.Duration) {
	t.Helper()
	if child == nil || child.command == nil {
		return
	}
	child.command.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-child.finished:
		child.log.Close()
		child.command = nil
		if err != nil {
			t.Error("private child process did not exit cleanly")
		}
	case <-time.After(timeout):
		child.command.Process.Kill()
		<-child.finished
		child.log.Close()
		child.command = nil
		t.Error("private child process exceeded shutdown bound")
	}
}
