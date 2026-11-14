package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// Controlled TLS XML exercises the real SDK, not a real provider. Inconsistent
// response identities must never become an upload receipt or partial observation.
func TestS3SDKMultipartResponseIdentity(t *testing.T) {
	for _, operation := range []string{"create", "complete", "uploads", "parts"} {
		modes := []string{"valid", "missing_bucket", "foreign_bucket"}
		if operation != "uploads" {
			modes = append(modes, "missing_key", "foreign_key")
		}
		if operation == "parts" {
			modes = append(modes, "missing_upload", "foreign_upload", "foreign_key_page2", "foreign_upload_page2")
		}
		if operation == "uploads" || operation == "parts" {
			modes = append(modes, "foreign_bucket_page2")
		}
		for _, mode := range modes {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				ref := protocolRef()
				key, _ := ref.Key()
				bucket := "synthetic-artifact-fixture"
				var calls atomic.Int32
				client, _ := protocolS3(t, func(w http.ResponseWriter, r *http.Request) {
					n := calls.Add(1)
					if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
						t.Error("unsigned request")
					}
					path := "/" + bucket + "/" + key
					if operation == "uploads" {
						path = "/" + bucket
					}
					if r.URL.Path != path {
						t.Error("nonexact request path")
					}
					if operation == "create" || operation == "complete" {
						if r.Method != http.MethodPost {
							t.Error("unexpected mutation method")
						}
					} else if r.Method != http.MethodGet {
						t.Error("unexpected observation method")
					}
					if operation == "parts" || operation == "complete" {
						if r.URL.Query().Get("uploadId") != "exact-upload" {
							t.Error("wrong original upload ID")
						}
					}
					if operation == "complete" && r.Header.Get("If-None-Match") != "*" {
						t.Error("conditional completion absent")
					}
					if operation == "uploads" && (!r.URL.Query().Has("uploads") || r.URL.Query().Get("prefix") != key) {
						t.Error("wrong exact prefix")
					}
					field := func(name, tag, value string) string {
						if mode == "missing_"+name {
							return ""
						}
						if mode == "foreign_"+name || mode == "foreign_"+name+"_page2" && n == 2 {
							value = "sensitive-provider-identity"
						}
						return "<" + tag + ">" + value + "</" + tag + ">"
					}
					identity := field("bucket", "Bucket", bucket)
					if operation != "uploads" {
						identity += field("key", "Key", key)
					}
					w.Header().Set("Content-Type", "application/xml")
					switch operation {
					case "create":
						io.WriteString(w, "<InitiateMultipartUploadResult>"+identity+"<UploadId>new-owned-upload</UploadId></InitiateMultipartUploadResult>")
					case "complete":
						io.WriteString(w, "<CompleteMultipartUploadResult>"+identity+"<ETag>\"opaque\"</ETag></CompleteMultipartUploadResult>")
					case "uploads":
						flag, cursor := "<IsTruncated>false</IsTruncated>", ""
						if strings.HasSuffix(mode, "_page2") && n == 1 {
							flag = "<IsTruncated>true</IsTruncated>"
							cursor = "<NextKeyMarker>next</NextKeyMarker><NextUploadIdMarker>next-upload</NextUploadIdMarker>"
						}
						io.WriteString(w, fmt.Sprintf("<ListMultipartUploadsResult>%s%s%s<Upload><Key>%s</Key><UploadId>owned-%d</UploadId></Upload></ListMultipartUploadsResult>", identity, flag, cursor, key, n))
					case "parts":
						identity += field("upload", "UploadId", "exact-upload")
						flag, cursor := "<IsTruncated>false</IsTruncated>", ""
						if strings.HasSuffix(mode, "_page2") && n == 1 {
							flag = "<IsTruncated>true</IsTruncated>"
							cursor = "<NextPartNumberMarker>1</NextPartNumberMarker>"
						}
						io.WriteString(w, fmt.Sprintf("<ListPartsResult>%s%s%s<Part><PartNumber>%d</PartNumber><Size>17</Size><ETag>\"opaque\"</ETag></Part></ListPartsResult>", identity, flag, cursor, n))
					}
				})
				var err error
				success, zero := false, false
				switch operation {
				case "create":
					var out Upload
					out, err = client.Create(context.Background(), ref, map[string]string{"object-id": ref.Object, "bytes": "17", "sha256": strings.Repeat("a", 64)})
					success = out.ID == "new-owned-upload"
					zero = reflect.DeepEqual(out, Upload{})
				case "complete":
					var out Info
					out, err = client.Complete(context.Background(), ref, "exact-upload", []Part{{Number: 1, Bytes: 17, SHA256: strings.Repeat("a", 64), ETag: `"part"`}})
					success = out.ETag == `"opaque"`
					zero = reflect.DeepEqual(out, Info{})
				case "uploads":
					var out []Upload
					out, err = client.FindUploads(context.Background(), ref)
					success = len(out) == 1 && out[0].ID == "owned-1"
					zero = out == nil
				case "parts":
					var out []Part
					out, err = client.Parts(context.Background(), ref, "exact-upload")
					success = len(out) == 1 && out[0].Number == 1
					zero = out == nil
				}
				expectedCalls := int32(1)
				if strings.HasSuffix(mode, "_page2") {
					expectedCalls = 2
				}
				if calls.Load() != expectedCalls {
					t.Fatal("unexpected replay or observation count", calls.Load(), expectedCalls)
				}
				if mode == "valid" {
					if err != nil || !success {
						t.Fatal("valid exact receipt rejected", err)
					}
				} else {
					code := "OBJECT_RESPONSE_INVALID"
					if operation == "create" || operation == "complete" {
						code = "OBJECT_REMOTE_OUTCOME_UNKNOWN"
					}
					if err == nil || err.Error() != code || !zero {
						t.Fatal("unbound identity returned a result or wrong outcome", err, zero)
					}
					if strings.Contains(err.Error(), "sensitive-provider-identity") {
						t.Fatal("provider field leaked")
					}
				}
			})
		}
	}
}
