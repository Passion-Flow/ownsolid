package objectstore

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestManagementDenialDoesNotTreatUnavailableAsMTLS(t *testing.T) {
	for _, e := range []error{context.DeadlineExceeded, context.Canceled, errors.New("tls: certificate required"), &net.OpError{Op: "dial", Err: errors.New("tls: certificate required")}, &net.OpError{Op: "remote error", Err: errors.New("connection reset by peer")}} {
		if managementCertificateDenied(e) {
			t.Fatalf("unverified absence accepted: %T", e)
		}
	}
	for _, e := range []error{tls.AlertError(tlsCertificateRequiredAlert), &net.OpError{Op: "remote error", Err: errors.New("tls: certificate required")}} {
		if !managementCertificateDenied(e) {
			t.Fatal("reviewed certificate alert rejected")
		}
	}
}
func TestPrivateObjectRequiresAnonymousAndWrongSignatureDenial(t *testing.T) {
	var calls atomic.Int32
	client, _ := protocolS3(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/synthetic-artifact-fixture/artifacts/11111111-1111-4111-8111-111111111111/22222222-2222-4222-8222-222222222222/33333333-3333-4333-8333-333333333333/content" {
			t.Error("nonexact object request")
		}
		w.WriteHeader(http.StatusForbidden)
	})
	ref := Ref{Tenant: "11111111-1111-4111-8111-111111111111", Workspace: "22222222-2222-4222-8222-222222222222", Object: "33333333-3333-4333-8333-333333333333"}
	if e := client.PrivateObject(context.Background(), ref); e != nil || calls.Load() != 2 {
		t.Fatal("negative controls missing", e, calls.Load())
	}
}

func TestS3ExactConditionalIdentityRejectsWildcardWeakAndHeaderLists(t *testing.T) {
	for _, value := range []string{"*", `W/"weak"`, `"first", "second"`, "\"line\nfeed\"", "", `unquoted`} {
		if validETag(value) {
			t.Fatal("nonexact ETag accepted")
		}
	}
	for _, value := range []string{`"opaque-multipart-2"`, `"opaque,inside-tag"`} {
		if !validETag(value) {
			t.Fatal("valid strong opaque ETag rejected")
		}
	}
	for _, value := range []string{"", " leading", "trailing ", "header\r\nvalue"} {
		if validUploadID(value) {
			t.Fatal("unbounded/ambiguous upload ID accepted")
		}
	}
}

func TestManagementWrongCertificateRequiresExplicitPeerRejection(t *testing.T) {
	for _, e := range []error{context.DeadlineExceeded, errors.New("tls: unknown certificate authority"), &net.OpError{Op: "remote error", Err: errors.New("tls: certificate required")}} {
		if managementClientRejected(e, managementClientUntrusted) {
			t.Fatal("wrong certificate was not actually rejected", e)
		}
	}
	for _, e := range []error{tls.AlertError(tlsUnknownCAAlert), &net.OpError{Op: "remote error", Err: errors.New("tls: unknown certificate authority")}} {
		if !managementClientRejected(e, managementClientUntrusted) {
			t.Fatal("explicit unknown CA alert rejected")
		}
	}
	pair, e := untrustedManagementCertificate(5)
	if e != nil || len(pair.Certificate) != 1 || pair.PrivateKey == nil {
		t.Fatal("synthetic negative certificate unavailable", e)
	}
}
