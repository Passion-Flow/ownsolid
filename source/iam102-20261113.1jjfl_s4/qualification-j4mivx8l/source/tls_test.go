package prototype

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestUntrustedTLSReleasesNoRequest(t *testing.T) {
	for _, name := range []string{"wrong_ca", "wrong_server_name"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int64
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
			defer s.Close()
			base := s.Client().Transport.(*http.Transport).Clone()
			defer base.CloseIdleConnections()
			base.TLSClientConfig = base.TLSClientConfig.Clone()
			base.TLSClientConfig.MinVersion = tls.VersionTLS12
			if name == "wrong_ca" {
				base.TLSClientConfig.RootCAs = x509.NewCertPool()
			} else {
				base.TLSClientConfig.ServerName = "wrong.iam102.test"
			}
			client := &http.Client{Transport: base, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			if _, e := discover(context.Background(), client, s.URL+"/realms/private"); e == nil {
				t.Fatal("untrusted TLS accepted")
			}
			if calls.Load() != 0 {
				t.Fatal("unverified TLS released a provider request")
			}
		})
	}
}
