package objectstore

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// These are SDK protocol controls, not a SeaweedFS deployment qualification.
// The actual paired fixture invokes the same admission against its real service.
func TestBucketAuthorityRequiresExistingPositiveAndExactBidirectionalDenial(t *testing.T) {
	for _, mode := range []string{"accepted", "missing_canary", "foreign_missing", "broader_controller", "broader_observer", "wrong_signature", "inventory_leak", "inventory_page", "inventory_missing",
		"observer_own_read", "canary_wrong_name", "canary_large_limit", "canary_nonempty", "canary_prefixes", "canary_truncated", "canary_token", "canary_hidden_prefix", "canary_start_after", "canary_missing_count", "canary_missing_truncation", "canary_blank", "canary_invalid_xml", "canary_readback_changed"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			var canaryLists atomic.Int32
			client, _ := protocolS3(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				observer := strings.Contains(r.Header.Get("Authorization"), "Credential=SYNTHETIC_REFUSAL_ACCESS_ONLY/")
				controller := strings.Contains(r.Header.Get("Authorization"), "Credential=SYNTHETIC_ACCESS_ONLY/")
				if observer == controller {
					t.Error("missing or ambiguous actual SDK identity")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				own := "synthetic-artifact-fixture"
				if observer {
					own = "synthetic-refusal-fixture"
				}
				w.Header().Set("Content-Type", "application/xml")
				if r.URL.Path == "/" {
					if r.Method != "GET" || r.URL.Query().Get("max-buckets") != "2" {
						t.Error("unbounded or unexpected bucket inventory")
					}
					buckets := "<Bucket><Name>" + own + "</Name></Bucket>"
					if mode == "inventory_leak" {
						buckets += "<Bucket><Name>foreign-existing-bucket</Name></Bucket>"
					}
					if mode == "inventory_missing" {
						buckets = ""
					}
					continuation := ""
					if mode == "inventory_page" {
						continuation = "<ContinuationToken>extra-scope</ContinuationToken>"
					}
					io.WriteString(w, "<ListAllMyBucketsResult><Buckets>"+buckets+"</Buckets>"+continuation+"</ListAllMyBucketsResult>")
					return
				}
				if r.URL.Path == "/"+own && r.Method == "HEAD" {
					if observer && mode != "observer_own_read" {
						w.WriteHeader(http.StatusForbidden)
					}
					return
				}
				if observer && r.URL.Path == "/"+own && r.Method == "GET" && r.URL.Query().Get("list-type") == "2" {
					if r.URL.Query().Get("max-keys") != "1" || r.URL.Query().Get("prefix") != "" || r.URL.Query().Get("continuation-token") != "" || r.URL.Query().Get("start-after") != "" {
						t.Error("canary request escaped its bounded complete namespace")
					}
					if mode == "missing_canary" {
						w.WriteHeader(http.StatusNotFound)
						io.WriteString(w, "<Error><Code>NoSuchBucket</Code></Error>")
						return
					}
					sequence := canaryLists.Add(1)
					body := "<ListBucketResult><Name>" + own + "</Name><MaxKeys>1</MaxKeys><KeyCount>0</KeyCount><IsTruncated>false</IsTruncated></ListBucketResult>"
					switch mode {
					case "canary_wrong_name":
						body = strings.ReplaceAll(body, own, "another-private-bucket")
					case "canary_large_limit":
						body = strings.ReplaceAll(body, "<MaxKeys>1</MaxKeys>", "<MaxKeys>2</MaxKeys>")
					case "canary_nonempty":
						body = strings.ReplaceAll(body, "<KeyCount>0</KeyCount>", "<KeyCount>1</KeyCount><Contents><Key>synthetic-existing-object</Key></Contents>")
					case "canary_prefixes":
						body = strings.ReplaceAll(body, "</ListBucketResult>", "<CommonPrefixes><Prefix>hidden/</Prefix></CommonPrefixes></ListBucketResult>")
					case "canary_truncated":
						body = strings.ReplaceAll(body, "<IsTruncated>false</IsTruncated>", "<IsTruncated>true</IsTruncated>")
					case "canary_token":
						body = strings.ReplaceAll(body, "</ListBucketResult>", "<NextContinuationToken>hidden-scope</NextContinuationToken></ListBucketResult>")
					case "canary_hidden_prefix":
						body = strings.ReplaceAll(body, "</ListBucketResult>", "<Prefix>hidden/</Prefix></ListBucketResult>")
					case "canary_start_after":
						body = strings.ReplaceAll(body, "</ListBucketResult>", "<StartAfter>hidden</StartAfter></ListBucketResult>")
					case "canary_missing_count":
						body = strings.ReplaceAll(body, "<KeyCount>0</KeyCount>", "")
					case "canary_missing_truncation":
						body = strings.ReplaceAll(body, "<IsTruncated>false</IsTruncated>", "")
					case "canary_blank":
						body = "<ListBucketResult/>"
					case "canary_invalid_xml":
						body = "<ListBucketResult><Name>"
					case "canary_readback_changed":
						if sequence == 2 {
							body = strings.ReplaceAll(body, own, "different-at-final-readback")
						}
					}
					io.WriteString(w, body)
					return
				}
				if mode == "broader_controller" && controller || mode == "broader_observer" && observer {
					return
				}
				if mode == "foreign_missing" {
					w.WriteHeader(http.StatusNotFound)
					io.WriteString(w, "<Error><Code>NoSuchBucket</Code></Error>")
					return
				}
				w.WriteHeader(http.StatusForbidden)
				code := "AccessDenied"
				if mode == "wrong_signature" {
					code = "SignatureDoesNotMatch"
				}
				io.WriteString(w, "<Error><Code>"+code+"</Code><Message>sensitive-value</Message></Error>")
			})
			err := client.verifyBucketAuthority(context.Background())
			if mode == "accepted" {
				if err != nil || calls.Load() != 12 || canaryLists.Load() != 2 {
					t.Fatal("positive/negative controls incomplete", err, calls.Load())
				}
			} else if err == nil || strings.Contains(err.Error(), "sensitive-value") {
				t.Fatal("unverified bucket authority admitted or leaked", err)
			}
		})
	}
}

func TestBucketAuthorityCancellationNeverProvesPermission(t *testing.T) {
	client, _ := protocolS3(t, func(http.ResponseWriter, *http.Request) { t.Error("cancelled authority check performed network I/O") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.verifyBucketAuthority(ctx); err == nil || err.Error() != "OBJECT_BUCKET_AUTHORITY_UNVERIFIED" {
		t.Fatal("cancelled request admitted")
	}
}

func TestBucketAuthorityRefusesSharedCredentialMaterial(t *testing.T) {
	client, _ := protocolS3(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid profile performed network I/O") })
	configuration := client.configuration
	for _, mode := range []string{"access", "secret"} {
		path, value := configuration.RefusalAccessKeyFile, "SYNTHETIC_ACCESS_ONLY"
		if mode == "secret" {
			path, value = configuration.RefusalSecretKeyFile, "SYNTHETIC_SECRET_ONLY"
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		created, err := NewS3(configuration)
		if created != nil || err == nil || err.Error() != "OBJECT_BUCKET_AUTHORITY_INVALID" {
			t.Fatal("shared identity material admitted", err)
		}
		if err := os.WriteFile(configuration.RefusalAccessKeyFile, []byte("SYNTHETIC_REFUSAL_ACCESS_ONLY"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
