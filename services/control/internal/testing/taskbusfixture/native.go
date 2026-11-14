//go:build task_events_native_integration

// Package taskbusfixture owns actual isolated synthetic native NATS processes.
package taskbusfixture

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

type Profile struct {
	Tenant        string
	Configuration taskbus.Configuration
}
type Native struct {
	t                                     *testing.T
	Root, binary, configuration, evidence string
	timeout                               time.Duration
	memory                                int
	port                                  int
	command                               *exec.Cmd
	done                                  chan struct{}
	operator                              nkeys.KeyPair
	clientTLS                             *tls.Config
	connection                            *nats.Conn
	Profiles                              []Profile
	logs                                  []string
	shutdowns                             []map[string]any
}

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func private(t *testing.T, path string, raw []byte) {
	t.Helper()
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	must(t, e)
	_, e = f.Write(raw)
	must(t, e)
	must(t, f.Sync())
	must(t, f.Close())
}
func hash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}
func input(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv("PROJECT_ONE_NATS_NATIVE_" + name)
	if v == "" {
		t.Fatal("EXPLICIT_NATIVE_NATS_INPUT_REQUIRED", name)
	}
	return v
}
func integer(t *testing.T, name string, low, high int) int {
	t.Helper()
	v := input(t, name)
	n, e := strconv.Atoi(v)
	if e != nil || strconv.Itoa(n) != v || n < low || n > high {
		t.Fatal("INVALID_NATIVE_NATS_BOUND", name)
	}
	return n
}

// New consumes only explicit test inputs and a complete caller-owned test policy.
// It never adopts a live broker, provisions a production resource or downloads.
func New(t *testing.T, base taskbus.Configuration) *Native {
	t.Helper()
	return newNative(t, base, "")
}

// NewForTenant provisions only the test's already installed synthetic tenant.
// Production serving never provisions broker resources.
func NewForTenant(t *testing.T, base taskbus.Configuration, tenant string) *Native {
	t.Helper()
	if !taskbus.ValidID(tenant) {
		t.Fatal("NATIVE_NATS_EXISTING_SYNTHETIC_TENANT_REQUIRED")
	}
	return newNative(t, base, tenant)
}

func newNative(t *testing.T, base taskbus.Configuration, firstTenant string) *Native {
	t.Helper()
	pause := os.Getenv("PROJECT_ONE_NATS_NATIVE_PAUSE_AFTER_PREPARED")
	if pause != "" && pause != "true" && pause != "false" {
		t.Fatal("NATIVE_NATS_EXPLICIT_PAUSE_BOOLEAN_REQUIRED")
	}
	n := &Native{t: t, binary: input(t, "BINARY"), timeout: time.Duration(integer(t, "OPERATION_SECONDS", 5, 60)) * time.Second, memory: integer(t, "MEMORY_MIB", 64, 1024), evidence: input(t, "EVIDENCE_DIRECTORY")}
	resolved, e := filepath.EvalSymlinks(n.binary)
	must(t, e)
	info, e := os.Lstat(n.binary)
	must(t, e)
	if resolved != n.binary || !filepath.IsAbs(n.binary) || !info.Mode().IsRegular() || info.Mode().Perm() != 0700 {
		t.Fatal("EXACT_NATIVE_NATS_BINARY_REQUIRED")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 || info.Size() <= 0 || info.Size() > 64<<20 {
		t.Fatal("OWNED_BOUNDED_NATIVE_NATS_BINARY_REQUIRED")
	}
	sum, e := hash(n.binary)
	must(t, e)
	if sum != input(t, "SHA256") {
		t.Fatal("NATIVE_NATS_BINARY_HASH_MISMATCH")
	}
	resolved, e = filepath.EvalSymlinks(n.evidence)
	must(t, e)
	if resolved != n.evidence {
		t.Fatal("EXACT_NATIVE_NATS_EVIDENCE_REQUIRED")
	}
	for _, path := range []string{n.evidence, os.Getenv("TMPDIR")} {
		resolved, e := filepath.EvalSymlinks(path)
		must(t, e)
		info, e := os.Stat(path)
		must(t, e)
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !filepath.IsAbs(path) || resolved != path || !info.IsDir() || info.Mode().Perm() != 0700 || !ok || owner.Uid != uint32(os.Geteuid()) {
			t.Fatal("OWNED_PRIVATE_NATIVE_NATS_DIRECTORY_REQUIRED")
		}
	}
	n.Root, e = os.MkdirTemp(os.Getenv("TMPDIR"), "native-nats-")
	must(t, e)
	n.configuration = filepath.Join(n.Root, "server.json")
	t.Cleanup(n.close)
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	must(t, e)
	n.port = listener.Addr().(*net.TCPAddr).Port
	must(t, listener.Close())
	n.clientTLS = n.certificates()
	n.operator, e = nkeys.CreateUser()
	must(t, e)
	operatorPublic, e := n.operator.PublicKey()
	must(t, e)
	users := []any{map[string]any{"nkey": operatorPublic, "permissions": map[string]any{"publish": map[string]any{"allow": []string{"$JS.API.>", "projectone.tasks.v1.>"}}, "subscribe": map[string]any{"allow": []string{"_INBOX.>"}}}}}
	for range 2 {
		c := base
		c.Enabled = true
		c.Driver = taskbus.DriverJetStream
		c.NamespaceID = uuid.NewString()
		tenant := uuid.NewString()
		if len(n.Profiles) == 0 && firstTenant != "" {
			tenant = firstTenant
		}
		suffix := strings.ReplaceAll(c.NamespaceID, "-", "")
		c.Stream = "M25Fixture_" + suffix
		c.Consumer = "M25Receiver_" + suffix
		c.Endpoint = "tls://localhost:" + strconv.Itoa(n.port)
		c.DialAddresses = []string{"127.0.0.1"}
		c.CAFile = filepath.Join(n.Root, "ca.pem")
		c.ClientCertificateFile = filepath.Join(n.Root, "client.pem")
		c.ClientKeyFile = filepath.Join(n.Root, "client.key")
		filter, e := taskbus.FilterSubject(c.NamespaceID, tenant)
		must(t, e)
		for _, role := range []string{"publisher", "consumer"} {
			key, e := nkeys.CreateUser()
			must(t, e)
			public, e := key.PublicKey()
			must(t, e)
			seed, e := key.Seed()
			must(t, e)
			path := filepath.Join(n.Root, suffix+"-"+role+".nk")
			private(t, path, seed)
			key.Wipe()
			clear(seed)
			publish := []string{"$JS.API.CONSUMER.INFO." + c.Stream + "." + c.Consumer}
			if role == "publisher" {
				c.PublisherSeedFile = path
				publish = append(publish, filter, "$JS.API.STREAM.INFO."+c.Stream, "$JS.API.STREAM.MSG.GET."+c.Stream)
			} else {
				c.ConsumerSeedFile = path
				publish = append(publish, "$JS.API.CONSUMER.MSG.NEXT."+c.Stream+"."+c.Consumer, "$JS.ACK."+c.Stream+"."+c.Consumer+".>")
			}
			users = append(users, map[string]any{"nkey": public, "permissions": map[string]any{"publish": map[string]any{"allow": publish}, "subscribe": map[string]any{"allow": []string{"projectone.reply.v1." + c.NamespaceID + "." + role + ".>"}}}})
		}
		must(t, c.Validate())
		n.Profiles = append(n.Profiles, Profile{Tenant: tenant, Configuration: c})
	}
	configuration := map[string]any{"host": "127.0.0.1", "port": n.port, "max_connections": 16, "max_payload": 1 << 20,
		"jetstream":     map[string]any{"store_dir": filepath.Join(n.Root, "store"), "max_memory_store": 1 << 20, "max_file_store": 64 << 20},
		"tls":           map[string]any{"cert_file": filepath.Join(n.Root, "server.pem"), "key_file": filepath.Join(n.Root, "server.key"), "ca_file": filepath.Join(n.Root, "ca.pem"), "verify": true, "handshake_first": true, "timeout": base.ConnectSeconds},
		"authorization": map[string]any{"timeout": base.ConnectSeconds, "users": users}}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	// NATS configuration syntax accepts quoted UTF-8 strings, but not JSON's
	// HTML Unicode escapes for subject wildcards such as greater-than.
	encoder.SetEscapeHTML(false)
	must(t, encoder.Encode(configuration))
	private(t, n.configuration, encoded.Bytes())
	n.start()
	ctx, cancel := context.WithTimeout(t.Context(), n.timeout)
	defer cancel()
	api, e := jetstream.New(n.connection)
	must(t, e)
	for _, p := range n.Profiles {
		sc, e := taskbus.ExpectedStreamConfiguration(p.Configuration, p.Tenant)
		must(t, e)
		_, e = api.CreateStream(ctx, sc)
		must(t, e)
		cc, e := taskbus.ExpectedConsumerConfiguration(p.Configuration, p.Tenant)
		must(t, e)
		_, e = api.CreateConsumer(ctx, p.Configuration.Stream, cc)
		must(t, e)
	}
	n.receipt("prepared")
	if pause == "true" {
		time.Sleep(n.timeout)
		t.Fatal("NATIVE_NATS_EXPECTED_PARENT_INTERRUPTION_NOT_OBSERVED")
	}
	return n
}

func (n *Native) certificates() *tls.Config {
	t := n.t
	now := time.Now().UTC()
	public, privateKey, e := ed25519.GenerateKey(rand.Reader)
	must(t, e)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic native NATS CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, public, privateKey)
	must(t, e)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	private(t, filepath.Join(n.Root, "ca.pem"), caPEM)
	for i, name := range []string{"server", "client"} {
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		must(t, e)
		cert := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: "Synthetic native NATS " + name}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
		if name == "server" {
			cert.DNSNames = []string{"localhost"}
			cert.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		} else {
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		der, e := x509.CreateCertificate(rand.Reader, cert, ca, pub, privateKey)
		must(t, e)
		encoded, e := x509.MarshalPKCS8PrivateKey(key)
		must(t, e)
		private(t, filepath.Join(n.Root, name+".pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		private(t, filepath.Join(n.Root, name+".key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
		clear(key)
		clear(encoded)
	}
	clear(privateKey)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("SYNTHETIC_CA_PARSE_FAILED")
	}
	pair, e := tls.LoadX509KeyPair(filepath.Join(n.Root, "client.pem"), filepath.Join(n.Root, "client.key"))
	must(t, e)
	return &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12, ServerName: "localhost"}
}

func (n *Native) start() {
	t := n.t
	log := filepath.Join(n.evidence, fmt.Sprintf("%s-generation-%d.log", filepath.Base(n.Root), len(n.logs)))
	f, e := os.OpenFile(log, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	must(t, e)
	n.logs = append(n.logs, log)
	c := exec.Command(n.binary, "-c", n.configuration)
	c.Env = []string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + n.Root, "GOMAXPROCS=1", "GOMEMLIMIT=" + strconv.Itoa(n.memory) + "MiB"}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Stdout = f
	c.Stderr = f
	must(t, c.Start())
	n.command = c
	n.done = make(chan struct{})
	go func() { c.Wait(); f.Sync(); f.Close(); close(n.done) }()
	n.receipt(fmt.Sprintf("starting-%d", len(n.logs)-1))
	public, e := n.operator.PublicKey()
	must(t, e)
	deadline := time.Now().Add(n.timeout)
	for time.Now().Before(deadline) {
		select {
		case <-n.done:
			t.Fatal("ACTUAL_NATIVE_NATS_EXITED_DURING_STARTUP", c.ProcessState.ExitCode())
		default:
		}
		connection, e := nats.Connect("tls://localhost:"+strconv.Itoa(n.port), nats.Secure(n.clientTLS), nats.TLSHandshakeFirst(), nats.Nkey(public, n.operator.Sign), nats.NoReconnect(), nats.IgnoreDiscoveredServers(), nats.Timeout(time.Second), nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {}))
		if e == nil {
			n.connection = connection
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("ACTUAL_NATIVE_NATS_STARTUP_TIMEOUT")
}

func (n *Native) stop() {
	if n.connection != nil {
		n.connection.Close()
		n.connection = nil
	}
	if n.command == nil {
		return
	}
	c := n.command
	forced := false
	_ = c.Process.Signal(syscall.SIGTERM)
	select {
	case <-n.done:
	case <-time.After(n.timeout):
		forced = true
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		select {
		case <-n.done:
		case <-time.After(n.timeout):
			n.t.Error("NATIVE_NATS_JOIN_TIMEOUT")
			return
		}
	}
	code := c.ProcessState.ExitCode()
	n.shutdowns = append(n.shutdowns, map[string]any{"pid": c.Process.Pid, "exit_code": code, "forced": forced})
	n.command = nil
	if forced || code != 0 {
		n.t.Error("NATIVE_NATS_UNCLEAN_SHUTDOWN", code, forced)
	}
}

func (n *Native) Restart() { n.RestartWithInterruption(nil) }

// RestartWithInterruption observes a joined real outage before same-store restart.
func (n *Native) RestartWithInterruption(observe func()) {
	if n.command == nil {
		n.t.Fatal("NATIVE_NATS_RESTART_REQUIRES_LIVE_OWNED_GENERATION")
	}
	select {
	case <-n.done:
		n.t.Fatal("NATIVE_NATS_RESTART_REQUIRES_LIVE_OWNED_GENERATION")
	default:
	}
	beforePID := n.command.Process.Pid
	store := filepath.Join(n.Root, "store")
	before, e := os.Stat(store)
	must(n.t, e)
	configurationSHA, e := hash(n.configuration)
	must(n.t, e)
	binarySHA, e := hash(n.binary)
	must(n.t, e)
	if binarySHA != input(n.t, "SHA256") {
		n.t.Fatal("NATIVE_NATS_RESTART_BINARY_CHANGED")
	}
	n.stop()
	if n.t.Failed() {
		n.t.Fatal("NATIVE_NATS_RESTART_REQUIRES_JOINED_STOP")
	}
	if observe != nil {
		connection, e := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(n.port), n.timeout)
		if e == nil {
			connection.Close()
			n.t.Fatal("NATIVE_NATS_INTERRUPTION_PORT_STILL_OPEN")
		}
		raw, e := json.Marshal(map[string]any{"pid": beforePID, "port": n.port, "joined_stop": true, "port_closed": true})
		must(n.t, e)
		n.t.Log("TASK_EVENTS_NATIVE_INTERRUPTION " + string(raw))
		observe()
	}
	n.start()
	after, e := os.Stat(store)
	must(n.t, e)
	currentConfiguration, e := hash(n.configuration)
	must(n.t, e)
	currentBinary, e := hash(n.binary)
	must(n.t, e)
	if !os.SameFile(before, after) || currentConfiguration != configurationSHA || currentBinary != binarySHA || beforePID == n.command.Process.Pid {
		n.t.Fatal("NATIVE_NATS_RESTART_PERSISTENCE_OR_IDENTITY_CHANGED")
	}
	info, ok := after.Sys().(*syscall.Stat_t)
	if !ok {
		n.t.Fatal("NATIVE_NATS_RESTART_STORE_IDENTITY_REQUIRED")
	}
	raw, e := json.Marshal(map[string]any{"before_pid": beforePID, "after_pid": n.command.Process.Pid,
		"configuration_sha256": configurationSHA, "binary_sha256": binarySHA,
		"store_device": info.Dev, "store_inode": info.Ino, "port": n.port})
	must(n.t, e)
	n.t.Log("TASK_EVENTS_NATIVE_RESTART_EVIDENCE " + string(raw))
}

// VerifyTLSNegatives uses a valid operator NKey so an absent certificate cannot
// accidentally pass as an unrelated application-authentication refusal.
func (n *Native) VerifyTLSNegatives(t *testing.T) {
	public, e := n.operator.PublicKey()
	must(t, e)
	for _, missingCertificate := range []bool{true, false} {
		selected := n.clientTLS.Clone()
		if missingCertificate {
			selected.Certificates = nil
		} else {
			selected.RootCAs = x509.NewCertPool()
		}
		connection, err := nats.Connect("tls://localhost:"+strconv.Itoa(n.port), nats.Secure(selected), nats.TLSHandshakeFirst(), nats.Nkey(public, n.operator.Sign), nats.NoReconnect(), nats.IgnoreDiscoveredServers(), nats.Timeout(time.Second), nats.ErrorHandler(func(*nats.Conn, *nats.Subscription, error) {}))
		if connection != nil {
			connection.Close()
			t.Fatal("NATIVE_NATS_INVALID_TLS_ADMITTED")
		}
		if missingCertificate {
			if err == nil || !strings.Contains(err.Error(), "remote error: tls: certificate required") {
				t.Fatal("NATIVE_NATS_EXACT_CLIENT_CERTIFICATE_REFUSAL_REQUIRED", err)
			}
		} else {
			var unknown x509.UnknownAuthorityError
			if !errors.As(err, &unknown) {
				t.Fatal("NATIVE_NATS_EXACT_UNTRUSTED_CA_REFUSAL_REQUIRED", err)
			}
		}
		must(t, n.connection.FlushTimeout(time.Second))
	}
}
func (n *Native) receipt(stage string) {
	logs := []map[string]any{}
	for _, p := range n.logs {
		h, e := hash(p)
		if e != nil {
			n.t.Error(e)
			continue
		}
		logs = append(logs, map[string]any{"path": p, "sha256": h})
	}
	configurationSHA, e := hash(n.configuration)
	if e != nil {
		n.t.Error(e)
		return
	}
	value := map[string]any{"stage": stage, "root": n.Root, "port": n.port, "shutdowns": n.shutdowns, "logs": logs, "retained_failed_fixture": n.t.Failed(), "binary": n.binary, "configuration_sha256": configurationSHA}
	if n.command != nil {
		value["running_pid"] = n.command.Process.Pid
	}
	raw, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		n.t.Error(e)
		return
	}
	private(n.t, filepath.Join(n.evidence, filepath.Base(n.Root)+"-"+stage+".json"), raw)
}
func (n *Native) close() {
	n.stop()
	if n.operator != nil {
		n.operator.Wipe()
	}
	n.receipt("stopped")
	if n.t.Failed() {
		return
	}
	resolved, e := filepath.EvalSymlinks(n.Root)
	if e != nil || resolved != n.Root || !strings.HasPrefix(filepath.Base(n.Root), "native-nats-") {
		n.t.Error("NATIVE_NATS_CLEANUP_SCOPE_REJECTED")
		return
	}
	inventory := []map[string]any{}
	e = filepath.WalkDir(n.Root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("NATIVE_NATS_CLEANUP_LINK_REJECTED")
		}
		v, err := entry.Info()
		if err != nil {
			return err
		}
		st, ok := v.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uint32(os.Geteuid()) || !v.IsDir() && (!v.Mode().IsRegular() || st.Nlink != 1) {
			return fmt.Errorf("NATIVE_NATS_CLEANUP_OWNER_REJECTED")
		}
		relative, err := filepath.Rel(n.Root, path)
		if err != nil {
			return err
		}
		inventory = append(inventory, map[string]any{"path": relative, "bytes": v.Size(), "mode": v.Mode().String()})
		if len(inventory) > 10000 {
			return fmt.Errorf("NATIVE_NATS_CLEANUP_INVENTORY_LIMIT")
		}
		return nil
	})
	if e != nil {
		n.t.Error(e)
		return
	}
	raw, e := json.Marshal(inventory)
	must(n.t, e)
	private(n.t, filepath.Join(n.evidence, filepath.Base(n.Root)+"-cleanup.json"), raw)
	must(n.t, os.RemoveAll(n.Root))
}
