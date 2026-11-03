//go:build aws_integration

package http_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

const (
	awsTestRegion              = "us-east-1"
	awsTestAccount             = "123456789012"
	awsTestAccessKey           = "AKIAEXAMPLE0000000000"
	awsTestSecret              = "synthetic-enabled-http-fixture-only"
	awsTestSTS                 = "sts.us-east-1.amazonaws.com"
	awsTestEC2                 = "ec2.us-east-1.amazonaws.com"
	awsTestNextToken           = "page+2/="
	awsTestMaximumRequestBytes = 4096
)

type awsProtocolTLS struct {
	server          *httptest.Server
	calls           atomic.Int32
	deniedDials     atomic.Int32
	scenario        atomic.Int32
	certificateMode atomic.Int32
}

// These test-only certificates never enter the OS trust store or retained output.
func awsTLSCertificates(t *testing.T) (*x509.CertPool, []tls.Certificate) {
	t.Helper()
	newCA := func(serial int64) (*x509.Certificate, *ecdsa.PrivateKey) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "Synthetic AWS protocol fixture CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		certificate, err := x509.ParseCertificate(raw)
		if err != nil {
			t.Fatal(err)
		}
		return certificate, key
	}
	trusted, key := newCA(1)
	untrusted, untrustedKey := newCA(2)
	leaf := func(serial int64, parent *x509.Certificate, signer *ecdsa.PrivateKey, names []string) tls.Certificate {
		private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: names, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		raw, err := x509.CreateCertificate(rand.Reader, template, parent, &private.PublicKey, signer)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{raw, parent.Raw}, PrivateKey: private}
	}
	roots := x509.NewCertPool()
	roots.AddCert(trusted)
	return roots, []tls.Certificate{
		leaf(3, trusted, key, []string{awsTestSTS, awsTestEC2}),
		leaf(4, trusted, key, []string{"wrong.example.invalid"}),
		leaf(5, untrusted, untrustedKey, []string{awsTestSTS, awsTestEC2}),
	}
}

func newAWSProtocolTLS(t *testing.T) *awsProtocolTLS {
	t.Helper()
	f := &awsProtocolTLS{}
	roots, certificates := awsTLSCertificates(t)
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		raw, err := io.ReadAll(io.LimitReader(r.Body, awsTestMaximumRequestBytes+1))
		if err != nil || len(raw) > awsTestMaximumRequestBytes || r.Method != http.MethodPost || r.URL.Path != "/" || r.URL.RawQuery != "" || r.TLS == nil || r.TLS.Version < tls.VersionTLS12 || r.TLS.ServerName != r.Host {
			t.Error("synthetic AWS TLS/method/request boundary violated")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		values, err := url.ParseQuery(string(raw))
		service := ""
		switch r.Host {
		case awsTestSTS:
			service = "sts"
		case awsTestEC2:
			service = "ec2"
		}
		if err != nil || service == "" || !awsValidSignature(r, raw, service) {
			t.Error("synthetic AWS independent signature/authority verification failed")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		for _, v := range values {
			if len(v) != 1 {
				t.Error("duplicate AWS protocol field")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "text/xml")
		if service == "sts" {
			if len(values) != 2 || values.Get("Action") != "GetCallerIdentity" || values.Get("Version") != "2011-06-15" {
				t.Error("unexpected STS protocol")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, `<GetCallerIdentityResponse><GetCallerIdentityResult><Arn>arn:aws:iam::123456789012:user/synthetic-fixture</Arn><Account>%s</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`, awsTestAccount)
			return
		}
		if values.Get("Action") != "DescribeInstances" || values.Get("Version") != "2016-11-15" || values.Get("MaxResults") != "100" || len(values) != 3 && len(values) != 4 || len(values) == 4 && values.Get("NextToken") != awsTestNextToken {
			t.Error("unexpected EC2 pagination protocol")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if f.scenario.Load() == 2 {
			fmt.Fprint(w, awsInstancePage("i-0123456789abcdef2", ""))
			return
		}
		if values.Get("NextToken") == "" {
			fmt.Fprint(w, awsInstancePage("i-0123456789abcdef0", awsTestNextToken))
			return
		}
		if f.scenario.Load() == 1 {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<Response><Errors><Error><Code>UnauthorizedOperation</Code></Error></Errors></Response>`)
			return
		}
		fmt.Fprint(w, awsInstancePage("i-0123456789abcdef1", ""))
	}))
	f.server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificates[0]}, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return &certificates[f.certificateMode.Load()], nil
	}}
	f.server.StartTLS()
	t.Cleanup(f.server.Close)
	target, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(target.Host)
	if err != nil || host != "127.0.0.1" {
		t.Fatal("explicit loopback AWS listener required")
	}
	previous := http.DefaultTransport
	transport := previous.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.DialTLSContext = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	transport.DialContext = func(ctx context.Context, network, authority string) (net.Conn, error) {
		if network != "tcp" || authority != awsTestSTS+":443" && authority != awsTestEC2+":443" {
			f.deniedDials.Add(1)
			return nil, fmt.Errorf("SYNTHETIC_AWS_UNADMITTED_DIAL")
		}
		dialer := net.Dialer{Timeout: time.Second}
		return dialer.DialContext(ctx, "tcp", target.Host)
	}
	// Only this nonparallel selected test process is affected. The production
	// constructor clones this transport and still performs ordinary HTTPS/TLS.
	http.DefaultTransport = transport
	t.Cleanup(func() {
		if http.DefaultTransport != transport {
			t.Error("owned AWS test transport was replaced")
		} else {
			http.DefaultTransport = previous
		}
		transport.CloseIdleConnections()
		if f.deniedDials.Load() != 0 {
			t.Error("unexpected provider dial was refused")
		}
	})
	return f
}

func awsInstancePage(id, next string) string {
	return `<DescribeInstancesResponse><reservationSet><item><ownerId>` + awsTestAccount + `</ownerId><instancesSet><item><instanceId>` + id + `</instanceId><instanceType>t3.micro</instanceType><instanceState><name>running</name></instanceState><placement><availabilityZone>us-east-1a</availabilityZone></placement></item></instancesSet></item></reservationSet><nextToken>` + next + `</nextToken></DescribeInstancesResponse>`
}

// The fixture recomputes the documented SigV4 contract from received wire bytes;
// it does not call the production signer or merely check an Authorization prefix.
func awsValidSignature(r *http.Request, body []byte, service string) bool {
	stamp := r.Header.Get("X-Amz-Date")
	when, err := time.Parse("20060102T150405Z", stamp)
	if err != nil || time.Since(when) > time.Minute || time.Until(when) > time.Minute || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded; charset=utf-8" || r.Header.Get("X-Amz-Security-Token") != "" {
		return false
	}
	hash := func(raw []byte) string { value := sha256.Sum256(raw); return hex.EncodeToString(value[:]) }
	mac := func(key []byte, text string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(text))
		return h.Sum(nil)
	}
	const headers = "content-type;host;x-amz-date"
	canonical := "POST\n/\n\ncontent-type:" + r.Header.Get("Content-Type") + "\nhost:" + r.Host + "\nx-amz-date:" + stamp + "\n\n" + headers + "\n" + hash(body)
	scope := stamp[:8] + "/" + awsTestRegion + "/" + service + "/aws4_request"
	key := []byte("AWS4" + awsTestSecret)
	for _, part := range []string{stamp[:8], awsTestRegion, service, "aws4_request"} {
		key = mac(key, part)
	}
	signature := hex.EncodeToString(mac(key, "AWS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+hash([]byte(canonical))))
	clear(key)
	expected := "AWS4-HMAC-SHA256 Credential=" + awsTestAccessKey + "/" + scope + ", SignedHeaders=" + headers + ", Signature=" + signature
	return hmac.Equal([]byte(r.Header.Get("Authorization")), []byte(expected))
}
