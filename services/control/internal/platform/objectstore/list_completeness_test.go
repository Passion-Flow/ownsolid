package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// Actual SDK parsing must distinguish an absent completion flag from false.
// These TLS protocol fixtures do not qualify an installed S3 provider.
func TestS3SDKMultipartObservationRequiresCompleteUniquePages(t *testing.T) {
	for _, mode := range []string{"complete", "missing_flag", "duplicate_upload", "duplicate_across_pages", "cycle", "page_limit", "paginated"} {
		t.Run(mode, func(t *testing.T) {
			ref := protocolRef()
			key, _ := ref.Key()
			var calls atomic.Int32
			client, _ := protocolS3(t, func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if r.Method != http.MethodGet || !r.URL.Query().Has("uploads") || r.URL.Query().Get("prefix") != key {
					t.Error("unexpected multipart observation scope")
				}
				flag, cursor := "<IsTruncated>false</IsTruncated>", ""
				upload := fmt.Sprintf("<Upload><Key>%s</Key><UploadId>owned-%d</UploadId></Upload>", key, n)
				switch mode {
				case "missing_flag":
					flag = ""
				case "duplicate_upload":
					upload += upload
				case "duplicate_across_pages":
					upload = fmt.Sprintf("<Upload><Key>%s</Key><UploadId>same-owned</UploadId></Upload>", key)
					fallthrough
				case "paginated":
					if n == 1 {
						flag, cursor = "<IsTruncated>true</IsTruncated>", "<NextKeyMarker>next-key</NextKeyMarker><NextUploadIdMarker>next-upload</NextUploadIdMarker>"
					} else if r.URL.Query().Get("key-marker") != "next-key" || r.URL.Query().Get("upload-id-marker") != "next-upload" {
						t.Error("continuation not bound to original response")
					}
				case "cycle", "page_limit":
					flag = "<IsTruncated>true</IsTruncated>"
					if mode == "cycle" && n == 2 {
						cursor = "" // Returns to the initial cursor.
					} else {
						cursor = fmt.Sprintf("<NextKeyMarker>key-%d</NextKeyMarker><NextUploadIdMarker>upload-%d</NextUploadIdMarker>", n, n)
					}
				}
				w.Header().Set("Content-Type", "application/xml")
				io.WriteString(w, "<ListMultipartUploadsResult><Bucket>synthetic-artifact-fixture</Bucket>"+flag+cursor+upload+"</ListMultipartUploadsResult>")
			})
			if mode == "cycle" {
				client.configuration.MaximumListPages = 4
			}
			out, err := client.FindUploads(context.Background(), ref)
			if mode == "complete" || mode == "paginated" {
				want := 1
				if mode == "paginated" {
					want = 2
				}
				if err != nil || len(out) != want || int(calls.Load()) != want {
					t.Fatal("complete observation rejected", out, err, calls.Load())
				}
			} else if err == nil || out != nil || calls.Load() > int32(client.configuration.MaximumListPages) {
				t.Fatal("incomplete or duplicate observation returned", out, err, calls.Load())
			}
			if mode == "cycle" && calls.Load() != 2 {
				t.Fatal("cyclic cursor was queried again", calls.Load())
			}
		})
	}
}

func TestS3SDKPartObservationRequiresExplicitCompletion(t *testing.T) {
	for _, mode := range []string{"complete", "missing_flag", "cycle", "paginated"} {
		t.Run(mode, func(t *testing.T) {
			ref := protocolRef()
			key, _ := ref.Key()
			var calls atomic.Int32
			client, _ := protocolS3(t, func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Query().Get("uploadId") != "exact-upload" {
					t.Error("unexpected exact upload")
				}
				flag, cursor := "<IsTruncated>false</IsTruncated>", ""
				if mode == "missing_flag" {
					flag = ""
				} else if mode == "cycle" || mode == "paginated" && n == 1 {
					flag, cursor = "<IsTruncated>true</IsTruncated>", "<NextPartNumberMarker>1</NextPartNumberMarker>"
					if n == 2 {
						cursor = "" // Repeating the initial marker is not progress.
					}
				}
				if n == 2 && r.URL.Query().Get("part-number-marker") != "1" {
					t.Error("part continuation not retained")
				}
				w.Header().Set("Content-Type", "application/xml")
				io.WriteString(w, fmt.Sprintf("<ListPartsResult><Bucket>synthetic-artifact-fixture</Bucket><Key>%s</Key><UploadId>exact-upload</UploadId>%s%s<Part><PartNumber>%d</PartNumber><Size>17</Size><ETag>\"opaque-%d\"</ETag></Part></ListPartsResult>", key, flag, cursor, n, n))
			})
			out, err := client.Parts(context.Background(), ref, "exact-upload")
			if mode == "complete" || mode == "paginated" {
				if err != nil || len(out) != int(calls.Load()) || len(out) < 1 {
					t.Fatal("complete parts rejected", out, err)
				}
				for _, part := range out {
					if part.SHA256 != "" || !strings.HasPrefix(part.ETag, `"opaque-`) {
						t.Fatal("ETag mistaken for source SHA", part)
					}
				}
			} else if err == nil || out != nil || calls.Load() > 2 {
				t.Fatal("partial parts returned", out, err, calls.Load())
			}
		})
	}
}
