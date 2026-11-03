package mongodbfixture

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeMongoCertificateUsesPKCS1AndVerifiedSANs(t *testing.T) {
	root := t.TempDir()
	pool, err := certificates(root, 1, "127.0.0.1")
	if err != nil {
		t.Fatal("certificate generation failed")
	}
	raw, err := os.ReadFile(filepath.Join(root, "server.pem"))
	if err != nil {
		t.Fatal(err)
	}
	leafBlock, rest := pem.Decode(raw)
	keyBlock, rest := pem.Decode(rest)
	if leafBlock == nil || leafBlock.Type != "CERTIFICATE" || keyBlock == nil || keyBlock.Type != "RSA PRIVATE KEY" || len(rest) != 0 {
		t.Fatal("expected only certificate and unencrypted PKCS#1 RSA blocks")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil || key.Validate() != nil || key.N.BitLen() != fixtureRSAKeyBits {
		t.Fatal("invalid RSA key profile")
	}
	pair, err := tls.X509KeyPair(raw, raw)
	if err != nil {
		t.Fatal("certificate and private key do not match")
	}
	if _, ok := pair.PrivateKey.(*rsa.PrivateKey); !ok {
		t.Fatal("non-RSA private key")
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil || leaf.IsCA {
		t.Fatal("invalid leaf certificate")
	}
	for _, host := range []string{"localhost", "127.0.0.1"} {
		if _, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: host, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			t.Fatal("owned CA/SAN verification failed", host)
		}
	}
	for _, host := range []string{"wrong.example.invalid", "127.0.0.2"} {
		if _, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: host}); err == nil {
			t.Fatal("unowned DNS/IP identity accepted")
		}
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: x509.NewCertPool(), DNSName: "localhost"}); err == nil {
		t.Fatal("untrusted CA accepted")
	}
	for _, name := range []string{"ca.pem", "server.pem"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("certificate material is not private")
		}
	}
}

func waitFixture(t *testing.T) *Fixture {
	t.Helper()
	log, err := os.CreateTemp(t.TempDir(), "server-log-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	return &Fixture{log: log, done: make(chan struct{}), Input: Input{Policy: Policy{
		QuerySeconds: 1, PollMilliseconds: 50, MaximumLogBytes: 1024,
	}}}
}

func TestNativeMongoWaitRefusesExitedChildBeforeProbe(t *testing.T) {
	f := waitFixture(t)
	close(f.done)
	called := false
	err := f.wait(context.Background(), func(context.Context) bool { called = true; return true })
	if !errors.Is(err, errEarlyExit) || called {
		t.Fatal("an exited child reached a startup probe")
	}
}

func TestNativeMongoWaitCancelsAndJoinsProbeOnChildExit(t *testing.T) {
	f := waitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	go func() { <-started; close(f.done) }()
	joined := false
	err := f.wait(ctx, func(c context.Context) bool {
		close(started)
		<-c.Done()
		joined = true
		return false
	})
	if !errors.Is(err, errEarlyExit) || !joined || ctx.Err() != nil {
		t.Fatal("child exit did not promptly cancel and join the in-flight probe")
	}
}

func TestNativeMongoWaitBoundsIndividualProbe(t *testing.T) {
	f := waitFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probeExpired := false
	err := f.wait(ctx, func(c context.Context) bool {
		deadline, ok := c.Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Error("probe inherited the longer startup deadline")
		}
		<-c.Done()
		probeExpired = errors.Is(c.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancel()
		return false
	})
	if !errors.Is(err, errStartupTimeout) || !probeExpired {
		t.Fatal("query deadline did not independently expire")
	}
}

func TestNativeMongoWaitNeverAcceptsReadinessAfterExit(t *testing.T) {
	f := waitFixture(t)
	if err := f.wait(context.Background(), func(context.Context) bool { return true }); err != nil {
		t.Fatal("live ready probe refused")
	}
	err := f.wait(context.Background(), func(context.Context) bool { close(f.done); return true })
	if !errors.Is(err, errEarlyExit) {
		t.Fatal("readiness won over an observed process exit")
	}
}

func TestNativeMongoWaitRefusesLogBudgetBeforeProbe(t *testing.T) {
	f := waitFixture(t)
	f.Input.Policy.MaximumLogBytes = 0
	if _, err := f.log.WriteString("owned diagnostic"); err != nil {
		t.Fatal(err)
	}
	called := false
	err := f.wait(context.Background(), func(context.Context) bool { called = true; return true })
	if !errors.Is(err, errLogBudget) || called {
		t.Fatal("over-budget log reached a probe")
	}
}

func TestNativeMongoFailedStoppedFixtureRemovesOnlyDisposableState(t *testing.T) {
	root := t.TempDir()
	handle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	f := &Fixture{Root: root, rootHandle: handle}
	for _, name := range []string{"data", "credentials", "tmp"} {
		if err = handle.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
		if err = writeNew(filepath.Join(root, name, "synthetic"), []byte("owned")); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"server.log", "receipt.json", "mongod.json", "version.txt"} {
		if err = writeNew(filepath.Join(root, name), []byte("retained")); err != nil {
			t.Fatal(err)
		}
	}
	if f.removeDisposable() == nil || f.Receipt.DisposableRemoved {
		t.Fatal("cleanup admitted without an observed child stop")
	}
	if _, err = handle.Stat("credentials/synthetic"); err != nil {
		t.Fatal("refused cleanup removed credentials")
	}
	// A failed exit remains failed, but its isolated scratch data is disposable
	// once the owned process has been joined. Keep every diagnostic outside it.
	f.Receipt.Stopped = true
	f.Receipt.NormalExit = false
	if err = f.removeDisposable(); err != nil || !f.Receipt.DisposableRemoved || f.Receipt.NormalExit {
		t.Fatal("failed-exit cleanup did not preserve the failure state")
	}
	for _, name := range []string{"data", "credentials", "tmp"} {
		if _, err = handle.Stat(name); !os.IsNotExist(err) {
			t.Fatal("owned disposable state remains")
		}
	}
	for _, name := range []string{"server.log", "receipt.json", "mongod.json", "version.txt"} {
		if _, err = handle.Stat(name); err != nil {
			t.Fatal("cleanup removed retained evidence")
		}
	}
}
