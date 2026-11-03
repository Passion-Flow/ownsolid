package objectstore

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// Completion has no byte stream to validate. Its retained part sizes must be
// admitted before the signed mutation, including the selected object budget.
// These real-SDK TLS fixtures are not installed-provider qualification.
func TestS3SDKCompletionAdmitsExactPartAndObjectBounds(t *testing.T) {
	const partBytes int64 = 5 << 20
	for _, tc := range []struct {
		name   string
		sizes  []int64
		budget int64
		valid  bool
	}{
		{"single_short", []int64{17}, 0, true},
		{"single_full", []int64{partBytes}, 0, true},
		{"full_with_remainder", []int64{partBytes, 17}, 0, true},
		{"exact_default_budget", []int64{partBytes, partBytes}, 0, true},
		{"exact_selected_budget", []int64{partBytes, 17}, partBytes + 17, true},
		{"selected_budget_below_part", []int64{17}, 17, true},
		{"zero_part", []int64{0}, 0, false},
		{"negative_part", []int64{-1}, 0, false},
		{"oversized_last", []int64{partBytes, partBytes + 1}, 0, false},
		{"short_nonfinal", []int64{17, 17}, 0, false},
		{"oversized_nonfinal", []int64{partBytes + 1, 17}, 0, false},
		{"default_budget_exceeded", []int64{partBytes, partBytes, 1}, 0, false},
		{"selected_budget_exceeded", []int64{partBytes, 18}, partBytes + 17, false},
		{"single_selected_budget_exceeded", []int64{18}, 17, false},
		{"overflowing_part", []int64{math.MaxInt64}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := protocolRef()
			key, _ := ref.Key()
			var calls atomic.Int32
			client, _ := protocolS3(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/synthetic-artifact-fixture/"+key || r.URL.Query().Get("uploadId") != "exact-upload" || r.Header.Get("If-None-Match") != "*" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
					t.Error("completion lost signed exact conditional scope")
				}
				var body struct {
					Parts []struct {
						Number   int32  `xml:"PartNumber"`
						ETag     string `xml:"ETag"`
						Checksum string `xml:"ChecksumSHA256"`
					} `xml:"Part"`
				}
				if e := xml.NewDecoder(r.Body).Decode(&body); e != nil || len(body.Parts) != len(tc.sizes) {
					t.Error("completion did not retain exact parts", e)
				}
				raw, _ := hex.DecodeString(strings.Repeat("a", 64))
				for i, p := range body.Parts {
					if p.Number != int32(i+1) || p.ETag != `"owned-part"` || p.Checksum != base64.StdEncoding.EncodeToString(raw) {
						t.Error("completion changed accepted part identity")
					}
				}
				w.Header().Set("Content-Type", "application/xml")
				io.WriteString(w, "<CompleteMultipartUploadResult><Bucket>synthetic-artifact-fixture</Bucket><Key>"+key+"</Key><ETag>\"completed\"</ETag></CompleteMultipartUploadResult>")
			})
			if tc.budget != 0 {
				client.configuration.MaximumObjectBytes = tc.budget
				if e := client.configuration.Validate(); e != nil {
					t.Fatal("invalid selected test configuration", e)
				}
			}
			parts := make([]Part, len(tc.sizes))
			for i, size := range tc.sizes {
				parts[i] = Part{Number: int32(i + 1), Bytes: size, SHA256: strings.Repeat("a", 64), ETag: `"owned-part"`}
			}
			out, err := client.Complete(context.Background(), ref, "exact-upload", parts)
			if tc.valid {
				if err != nil || out.ETag != `"completed"` || calls.Load() != 1 {
					t.Fatal("valid boundary was rejected or replayed", err, calls.Load())
				}
			} else if err == nil || err.Error() != "OBJECT_PART_INVALID" || !reflect.DeepEqual(out, Info{}) || calls.Load() != 0 {
				t.Fatal("invalid byte plan reached remote mutation or returned receipt", err, calls.Load())
			}
		})
	}
}
