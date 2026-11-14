package mongodbfixture

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type Receipt struct {
	Format                    string `json:"format"`
	InputSHA256               string `json:"input_sha256"`
	BinarySHA256              string `json:"binary_sha256"`
	SeedSHA256                string `json:"seed_sha256"`
	ServerConfigurationSHA256 string `json:"server_configuration_sha256"`
	Version                   string `json:"version"`
	PID                       int    `json:"pid"`
	Port                      int    `json:"port"`
	TLS                       bool   `json:"tls"`
	ReplicaSet                string `json:"replica_set"`
	Authenticated             bool   `json:"authenticated"`
	ReadOnlyVerified          bool   `json:"read_only_verified"`
	SeedRows                  int    `json:"seed_rows"`
	Stopped                   bool   `json:"stopped"`
	NormalExit                bool   `json:"normal_exit"`
	DisposableRemoved         bool   `json:"disposable_removed"`
	ServerLogSHA256           string `json:"server_log_sha256"`
	Failure                   string `json:"failure,omitempty"`
}

type Fixture struct {
	Source     config.MongoDBSource
	Root       string
	Input      Input
	Admin      *mongo.Client
	Receipt    Receipt
	inputPath  string
	inputHash  string
	cmd        *exec.Cmd
	done       chan struct{}
	waitErr    error // Written before closing done; read only after observing done.
	log        *os.File
	rootHandle *os.Root
	stop       sync.Once
	ca         *x509.CertPool
	password   string
}

// A fixed cryptographic profile, not an adjustable deployment policy. PKCS#1
// RSA PEM is selected for MongoDB's Darwin SecItemImport path after its EC
// PKCS#8 import failed. The fixture still verifies its CA and exact DNS/IP SAN.
const fixtureRSAKeyBits = 3072

var (
	errEarlyExit      = errors.New("NATIVE_MONGODB_EARLY_EXIT")
	errStartupTimeout = errors.New("NATIVE_MONGODB_STARTUP_TIMEOUT")
	errLogBudget      = errors.New("NATIVE_MONGODB_LOG_BUDGET_EXCEEDED")
)

func secret() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("NATIVE_MONGODB_RANDOM_FAILED")
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func writeNew(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, we := f.Write(raw)
	se := f.Sync()
	ce := f.Close()
	return errors.Join(we, se, ce)
}

func (f *Fixture) save() error {
	raw, err := json.MarshalIndent(f.Receipt, "", "  ")
	if err != nil {
		return err
	}
	name := "receipt-" + secret() + ".tmp"
	if err = writeNew(filepath.Join(f.Root, name), append(raw, '\n')); err != nil {
		return err
	}
	if err = f.rootHandle.Rename(name, "receipt.json"); err != nil {
		return err
	}
	dir, err := f.rootHandle.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func certificates(root string, days int, host string) (*x509.CertPool, error) {
	key, err := rsa.GenerateKey(rand.Reader, fixtureRSAKeyBits)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "M21 isolated fixture CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Duration(days) * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err = writeNew(filepath.Join(root, "ca.pem"), caPEM); err != nil {
		return nil, err
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, fixtureRSAKeyBits)
	if err != nil {
		return nil, err
	}
	server := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP(host)}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment}
	cert, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, key)
	if err != nil {
		return nil, err
	}
	privateKey := x509.MarshalPKCS1PrivateKey(serverKey)
	combined := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privateKey})...)
	if err = writeNew(filepath.Join(root, "server.pem"), combined); err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("NATIVE_MONGODB_CA_INVALID")
	}
	return pool, nil
}

func (f *Fixture) client(user, password string) (*mongo.Client, error) {
	o := options.Client().SetHosts([]string{net.JoinHostPort(f.Input.Policy.ListenHost, strconv.Itoa(f.Input.Policy.Port))}).SetDirect(true).
		SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: f.ca}).SetMaxPoolSize(1).SetRetryReads(false).SetRetryWrites(false).
		SetTimeout(time.Duration(f.Input.Policy.QuerySeconds) * time.Second).SetServerSelectionTimeout(time.Duration(f.Input.Policy.QuerySeconds) * time.Second).
		SetConnectTimeout(time.Duration(f.Input.Policy.QuerySeconds) * time.Second)
	if user != "" {
		o.SetAuth(options.Credential{AuthSource: "admin", AuthMechanism: "SCRAM-SHA-256", Username: user, Password: password})
	}
	return mongo.Connect(o)
}

func (f *Fixture) disconnect(client *mongo.Client) {
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(f.Input.Policy.StopSeconds)*time.Second)
	defer cancel()
	_ = client.Disconnect(ctx)
}

func (f *Fixture) exited() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func (f *Fixture) processContext(ctx context.Context) (context.Context, func()) {
	// The MongoDB driver honors cancellation while selecting/dialing a server.
	// Cancel in-flight bootstrap work when the owned child exits, then join this
	// watcher before returning; no detached probe may outlive cleanup.
	ctx, cancel := context.WithCancel(ctx)
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-f.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() { cancel(); <-watcherDone }
}

func (f *Fixture) wait(ctx context.Context, fn func(context.Context) bool) error {
	if f.exited() {
		return errEarlyExit
	}
	ctx, join := f.processContext(ctx)
	defer join()
	ticker := time.NewTicker(time.Duration(f.Input.Policy.PollMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	for {
		if f.exited() {
			return errEarlyExit
		}
		if ctx.Err() != nil {
			return errStartupTimeout
		}
		info, err := f.log.Stat()
		if err != nil || info.Size() > f.Input.Policy.MaximumLogBytes {
			return errLogBudget
		}
		probe, stop := context.WithTimeout(ctx, time.Duration(f.Input.Policy.QuerySeconds)*time.Second)
		ready := fn(probe)
		stop()
		if f.exited() {
			return errEarlyExit
		}
		if ctx.Err() != nil {
			return errStartupTimeout
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			if f.exited() {
				return errEarlyExit
			}
			return errStartupTimeout
		case <-f.done:
			return errEarlyExit
		case <-ticker.C:
		}
	}
}

func New(t *testing.T) *Fixture {
	t.Helper()
	path := os.Getenv(InputEnvironment)
	input, hash, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(input.EvidenceDirectory, "fixture-")
	if err != nil {
		t.Fatal("NATIVE_MONGODB_EXCLUSIVE_ROOT_FAILED")
	}
	handle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &Fixture{Root: root, Input: input, inputPath: path, inputHash: hash, rootHandle: handle}
	f.Receipt = Receipt{Format: ReceiptFormat, InputSHA256: hash, BinarySHA256: input.BinarySHA256, SeedSHA256: input.SeedSHA256, Version: input.Version, Port: input.Policy.Port, TLS: true, ReplicaSet: input.Policy.ReplicaSet}
	t.Cleanup(func() { f.Close(t) })
	if err = f.prepare(); err != nil {
		f.Receipt.Failure = "NATIVE_MONGODB_PREPARATION_FAILED"
		_ = f.save()
		t.Fatal(f.Receipt.Failure, "receipt", filepath.Join(root, "receipt.json"))
	}
	if err = f.start(); err != nil {
		f.Receipt.Failure = err.Error()
		_ = f.save()
		t.Fatal("native MongoDB startup failed; retained receipt", filepath.Join(root, "receipt.json"), err)
	}
	if err = f.bootstrap(); err != nil {
		f.Receipt.Failure = "NATIVE_MONGODB_BOOTSTRAP_FAILED"
		if f.exited() {
			f.Receipt.Failure = errEarlyExit.Error()
		}
		for _, cause := range []error{errEarlyExit, errStartupTimeout, errLogBudget} {
			if errors.Is(err, cause) {
				f.Receipt.Failure = cause.Error()
			}
		}
		_ = f.save()
		t.Fatal(f.Receipt.Failure, "receipt", filepath.Join(root, "receipt.json"))
	}
	if err = f.save(); err != nil {
		t.Fatal(err)
	}
	t.Log("M21_MONGODB_FIXTURE=" + filepath.Join(root, "receipt.json"))
	return f
}

func (f *Fixture) prepare() error {
	for _, name := range []string{"data", "credentials", "tmp"} {
		if err := f.rootHandle.Mkdir(name, 0700); err != nil {
			return err
		}
	}
	credentials := filepath.Join(f.Root, "credentials")
	pool, err := certificates(credentials, f.Input.Policy.CertificateDays, f.Input.Policy.ListenHost)
	if err != nil {
		return err
	}
	f.ca = pool
	keyBytes := make([]byte, 64)
	if _, err = rand.Read(keyBytes); err != nil {
		return err
	}
	if err = writeNew(filepath.Join(credentials, "keyfile"), []byte(base64.StdEncoding.EncodeToString(keyBytes))); err != nil {
		return err
	}
	p := f.Input.Policy
	configuration := map[string]any{
		"storage":      map[string]any{"dbPath": filepath.Join(f.Root, "data"), "wiredTiger": map[string]any{"engineConfig": map[string]any{"cacheSizeGB": float64(p.CacheMiB) / 1024}}},
		"net":          map[string]any{"bindIp": p.ListenHost, "port": p.Port, "maxIncomingConnections": p.MaximumConnections, "unixDomainSocket": map[string]any{"enabled": false}, "tls": map[string]any{"mode": "requireTLS", "certificateKeyFile": filepath.Join(credentials, "server.pem"), "CAFile": filepath.Join(credentials, "ca.pem"), "allowConnectionsWithoutCertificates": true}},
		"replication":  map[string]any{"replSetName": p.ReplicaSet},
		"security":     map[string]any{"authorization": "enabled", "keyFile": filepath.Join(credentials, "keyfile")},
		"setParameter": map[string]any{"diagnosticDataCollectionEnabled": false, "enableTestCommands": true}}
	raw, err := json.Marshal(configuration)
	if err != nil {
		return err
	}
	f.Receipt.ServerConfigurationSHA256 = hashBytes(raw)
	return writeNew(filepath.Join(f.Root, "mongod.json"), raw)
}

func (f *Fixture) start() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(f.Input.Policy.QuerySeconds)*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, f.Input.Binary, "--version")
	command.Env = []string{"LANG=C", "TMPDIR=" + filepath.Join(f.Root, "tmp")}
	raw, err := command.Output()
	if err != nil || !strings.HasPrefix(string(raw), "db version v"+f.Input.Version+"\n") {
		return errors.New("NATIVE_MONGODB_ACTUAL_VERSION_MISMATCH")
	}
	if err = writeNew(filepath.Join(f.Root, "version.txt"), raw); err != nil {
		return err
	}
	f.log, err = os.OpenFile(filepath.Join(f.Root, "server.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	f.cmd = exec.Command(f.Input.Binary, "--config", filepath.Join(f.Root, "mongod.json"))
	f.cmd.Env = []string{"LANG=C", "TMPDIR=" + filepath.Join(f.Root, "tmp")}
	f.cmd.Stdout = f.log
	f.cmd.Stderr = f.log
	// Inherit the bounded test process group: the outer runner can stop the
	// complete owned tree on timeout. Never daemonize or adopt an existing port.
	if err = f.cmd.Start(); err != nil {
		return errors.New("NATIVE_MONGODB_START_FAILED")
	}
	f.Receipt.PID = f.cmd.Process.Pid
	f.done = make(chan struct{})
	go func() { f.waitErr = f.cmd.Wait(); close(f.done) }()
	return f.save()
}

func (f *Fixture) bootstrap() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(f.Input.Policy.StartupSeconds)*time.Second)
	defer cancel()
	ctx, join := f.processContext(ctx)
	defer join()
	local, err := f.client("", "")
	if err != nil {
		return err
	}
	defer f.disconnect(local)
	if err = f.wait(ctx, func(c context.Context) bool {
		return local.Database("admin").RunCommand(c, bson.D{{Key: "hello", Value: 1}}).Err() == nil
	}); err != nil {
		return err
	}
	p := f.Input.Policy
	init := bson.D{{Key: "replSetInitiate", Value: bson.D{{Key: "_id", Value: p.ReplicaSet}, {Key: "members", Value: bson.A{bson.D{{Key: "_id", Value: 0}, {Key: "host", Value: net.JoinHostPort("localhost", strconv.Itoa(p.Port))}}}}}}}
	if err = local.Database("admin").RunCommand(ctx, init).Err(); err != nil {
		return err
	}
	if err = f.wait(ctx, func(c context.Context) bool {
		var result struct {
			Primary bool `bson:"isWritablePrimary"`
		}
		return local.Database("admin").RunCommand(c, bson.D{{Key: "hello", Value: 1}}).Decode(&result) == nil && result.Primary
	}); err != nil {
		return err
	}
	adminPassword := secret()
	if err = local.Database("admin").RunCommand(ctx, bson.D{{Key: "createUser", Value: "m21_fixture_admin"}, {Key: "pwd", Value: adminPassword}, {Key: "mechanisms", Value: bson.A{"SCRAM-SHA-256"}}, {Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "root"}, {Key: "db", Value: "admin"}}}}}).Err(); err != nil {
		return err
	}
	f.Admin, err = f.client("m21_fixture_admin", adminPassword)
	if err != nil {
		return err
	}
	if err = f.Admin.Ping(ctx, nil); err != nil {
		return err
	}
	f.Receipt.Authenticated = true
	if err = f.Admin.Database(p.Database).CreateCollection(ctx, p.Collection); err != nil {
		return err
	}
	raw, err := os.ReadFile(f.Input.SeedFile)
	if err != nil || hashBytes(raw) != f.Input.SeedSHA256 {
		return errors.New("NATIVE_MONGODB_SEED_CHANGED")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	documents := []any{}
	for scanner.Scan() {
		var document bson.D
		if err = bson.UnmarshalExtJSON(scanner.Bytes(), true, &document); err != nil {
			return err
		}
		documents = append(documents, document)
	}
	if scanner.Err() != nil || len(documents) != 8 {
		return errors.New("NATIVE_MONGODB_EXACT_SEED_REQUIRED")
	}
	if _, err = f.Admin.Database(p.Database).Collection(p.Collection).InsertMany(ctx, documents); err != nil {
		return err
	}
	f.Receipt.SeedRows = 8
	role := bson.D{{Key: "createRole", Value: "m21_find_only"}, {Key: "privileges", Value: bson.A{bson.D{{Key: "resource", Value: bson.D{{Key: "db", Value: p.Database}, {Key: "collection", Value: p.Collection}}}, {Key: "actions", Value: bson.A{"find"}}}}}, {Key: "roles", Value: bson.A{}}}
	if err = f.Admin.Database("admin").RunCommand(ctx, role).Err(); err != nil {
		return err
	}
	f.password = secret()
	if err = f.Admin.Database("admin").RunCommand(ctx, bson.D{{Key: "createUser", Value: "m21_reader"}, {Key: "pwd", Value: f.password}, {Key: "mechanisms", Value: bson.A{"SCRAM-SHA-256"}}, {Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "m21_find_only"}, {Key: "db", Value: "admin"}}}}}).Err(); err != nil {
		return err
	}
	u := url.URL{Scheme: "mongodb", Host: net.JoinHostPort(p.ListenHost, strconv.Itoa(p.Port)), Path: "/" + p.Database, User: url.UserPassword("m21_reader", f.password)}
	query := url.Values{"authSource": {"admin"}, "authMechanism": {"SCRAM-SHA-256"}, "replicaSet": {p.ReplicaSet}, "directConnection": {"true"}}
	u.RawQuery = query.Encode()
	uriFile := filepath.Join(f.Root, "credentials", "source-uri")
	if err = writeNew(uriFile, []byte(u.String())); err != nil {
		return err
	}
	f.Source = config.MongoDBSource{ID: "00000000-0000-4000-8000-000000000021", Database: p.Database, Collection: p.Collection, URIFile: uriFile, TLS: true, CAFile: filepath.Join(f.Root, "credentials", "ca.pem"), Fields: []string{"question", "answer"}}
	reader, err := f.client("m21_reader", f.password)
	if err != nil {
		return err
	}
	defer f.disconnect(reader)
	if err = reader.Ping(ctx, nil); err != nil {
		return err
	}
	_, err = reader.Database(p.Database).Collection(p.Collection).InsertOne(ctx, bson.D{{Key: "forbidden", Value: true}})
	var denied mongo.ServerError
	if !errors.As(err, &denied) || !denied.HasErrorCode(13) {
		return errors.New("NATIVE_MONGODB_SOURCE_WRITE_NOT_DENIED")
	}
	if err = reader.Database(p.Database).Collection("m21_ungranted").FindOne(ctx, bson.D{}).Err(); !errors.As(err, &denied) || !denied.HasErrorCode(13) {
		return errors.New("NATIVE_MONGODB_FOREIGN_READ_NOT_DENIED")
	}
	f.Receipt.ReadOnlyVerified = true
	return nil
}

func (f *Fixture) Close(t *testing.T) {
	t.Helper()
	f.stop.Do(func() {
		defer f.rootHandle.Close()
		f.disconnect(f.Admin)
		if f.cmd != nil && f.cmd.Process != nil {
			if !f.exited() {
				_ = f.cmd.Process.Signal(syscall.SIGTERM)
			}
			select {
			case <-f.done:
				f.Receipt.Stopped = true
				f.Receipt.NormalExit = f.waitErr == nil
			case <-time.After(time.Duration(f.Input.Policy.StopSeconds) * time.Second):
				_ = f.cmd.Process.Kill()
				select {
				case <-f.done:
					f.Receipt.Stopped = true
				case <-time.After(time.Duration(f.Input.Policy.StopSeconds) * time.Second):
				}
				t.Error("NATIVE_MONGODB_FORCED_STOP")
			}
		} else {
			f.Receipt.Stopped = true
			f.Receipt.NormalExit = true
		}
		if f.log != nil {
			_ = f.log.Sync()
			_ = f.log.Close()
			if info, err := os.Stat(filepath.Join(f.Root, "server.log")); err == nil && info.Size() > 0 {
				sum, e := fileSHA(filepath.Join(f.Root, "server.log"), f.Input.Policy.MaximumLogBytes, false)
				if e != nil {
					t.Error("NATIVE_MONGODB_LOG_BUDGET_OR_IDENTITY_CHANGED")
				} else {
					f.Receipt.ServerLogSHA256 = sum
				}
			}
		}
		if !f.Receipt.Stopped || !f.Receipt.NormalExit {
			t.Error("NATIVE_MONGODB_CLEAN_EXIT_REQUIRED")
		}
		if f.Receipt.Stopped {
			if f.removeDisposable() != nil {
				t.Error("NATIVE_MONGODB_DISPOSABLE_CLEANUP_FAILED")
			}
		}
		if raw, err := config.MongoDBCA(f.inputPath); err != nil || hashBytes(raw) != f.inputHash {
			t.Error("NATIVE_MONGODB_INPUT_CHANGED")
		}
		if err := f.save(); err != nil {
			t.Error("NATIVE_MONGODB_RECEIPT_FAILED")
		}
		t.Log("M21_MONGODB_FIXTURE_CLOSED=" + filepath.Join(f.Root, "receipt.json"))
	})
}

func (f *Fixture) removeDisposable() error {
	if !f.Receipt.Stopped {
		return errors.New("NATIVE_MONGODB_STOP_REQUIRED_FOR_CLEANUP")
	}
	var cleanup error
	for _, name := range []string{"data", "credentials", "tmp"} {
		cleanup = errors.Join(cleanup, f.rootHandle.RemoveAll(name))
	}
	f.Receipt.DisposableRemoved = cleanup == nil
	return cleanup
}

// NegativeSource creates only fixed private fixture variants; it is not an
// endpoint override or a runtime hook. All retain the actual loopback endpoint.
func (f *Fixture) NegativeSource(t *testing.T, variant string) config.MongoDBSource {
	t.Helper()
	source := f.Source
	switch variant {
	case "wrong_password":
		raw, err := config.MongoDBSecret(source.URIFile)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword("m21_reader", secret())
		source.URIFile = filepath.Join(f.Root, "credentials", "wrong-password")
		if err = writeNew(source.URIFile, []byte(u.String())); err != nil {
			t.Fatal(err)
		}
	case "foreign_collection":
		source.Collection = "m21_ungranted"
	case "wrong_ca":
		root := filepath.Join(f.Root, "credentials", "wrong-ca")
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := certificates(root, f.Input.Policy.CertificateDays, f.Input.Policy.ListenHost); err != nil {
			t.Fatal(err)
		}
		source.CAFile = filepath.Join(root, "ca.pem")
	default:
		t.Fatal(fmt.Sprintf("unknown fixed MongoDB negative case %q", variant))
	}
	return source
}
