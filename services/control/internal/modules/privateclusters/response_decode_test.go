package privateclusters

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func TestPrivateClusterResponseFieldsRetainAPIExtensionsWithoutAliases(t *testing.T) {
	type review struct {
		object
		Status struct {
			Allowed *bool `json:"allowed"`
			Denied  bool  `json:"denied"`
		} `json:"status"`
	}
	valid := `{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","metadata":{"uid":"synthetic","labels":{"any-key":"value"}},"status":{"allowed":false,"denied":true,"futureField":true},"futureTopLevel":{"anything":null}}`
	var out review
	if decodeResponse([]byte(valid), &out) != nil || out.Status.Allowed == nil || *out.Status.Allowed || !out.Status.Denied || out.Metadata.UID != "synthetic" {
		t.Fatal("canonical explicit refusal or API extension lost")
	}
	for name, raw := range map[string]string{
		"null response":       "null",
		"case alias":          strings.Replace(valid, `"allowed"`, `"Allowed"`, 1),
		"canonical and alias": strings.Replace(valid, `"allowed":false`, `"allowed":false,"Allowed":true`, 1),
		"embedded alias":      strings.Replace(valid, `"apiVersion"`, `"APIVersion"`, 1),
		"metadata alias":      strings.Replace(valid, `"uid"`, `"UID"`, 1),
		"null status":         strings.Replace(valid, `"status":{"allowed":false,"denied":true,"futureField":true}`, `"status":null`, 1),
		"null boolean":        strings.Replace(valid, `"denied":true`, `"denied":null`, 1),
		"null map value":      strings.Replace(valid, `"any-key":"value"`, `"any-key":null`, 1),
		"duplicate extension": strings.Replace(valid, `"futureField":true`, `"futureField":true,"futureField":false`, 1),
		"invalid utf8":        strings.Replace(valid, "synthetic", string([]byte{0xff}), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if decodeResponse([]byte(raw), new(review)) == nil {
				t.Fatal("malformed response gained canonical meaning")
			}
		})
	}
	var list objectList[object]
	if decodeResponse([]byte(`{"items":[{"metadata":{"UID":"alias"}}]}`), &list) == nil {
		t.Fatal("array member alias accepted")
	}
}

func TestPrivateClusterIncompleteInventoryNeverProvesAbsence(t *testing.T) {
	for _, items := range []string{``, `,"items":null`, `,"items":[]`} {
		t.Run(items, func(t *testing.T) {
			deletes := 0
			c, _ := wireClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					deletes++
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				api, kind := "v1", "PodList"
				if strings.Contains(r.URL.Path, "/jobs") {
					api, kind = "batch/v1", "JobList"
				}
				w.Write([]byte(`{"apiVersion":"` + api + `","kind":"` + kind + `","metadata":{}` + items + `}`))
			})
			jobs, pods, e := c.inventory(t.Context())
			if items == `,"items":[]` {
				if e != nil || jobs == nil || pods == nil || len(jobs) != 0 || len(pods) != 0 {
					t.Fatal("explicit empty inventory refused", e)
				}
			} else if e == nil {
				t.Fatal("incomplete list became observed absence")
			}
			if deletes != 0 {
				t.Fatal("inventory performed a mutation")
			}
		})
	}
}

func TestPrivateClusterProtectedProofRequiresCanonicalWrapper(t *testing.T) {
	// These are envelope controls only, deliberately not valid signed approval.
	p := bundle.ImageProof{FormatVersion: 1, AssetID: "synthetic-probe", Manifest: []byte(`{}`), Signature: []byte(`{}`)}
	raw, e := json.Marshal(p)
	if e != nil || decodeProtectedJSON(raw, new(bundle.ImageProof)) != nil {
		t.Fatal("canonical base64 wrapper refused", e)
	}
	for _, changed := range []string{
		strings.Replace(string(raw), `"asset_id"`, `"Asset_id"`, 1),
		strings.Replace(string(raw), `"manifest_bytes"`, `"Manifest_bytes"`, 1),
		strings.Replace(string(raw), `"format_version":1`, `"format_version":1,"Format_version":1`, 1),
		strings.Replace(string(raw), `"signature_bytes":"e30="`, `"signature_bytes":"invalid base64"`, 1),
	} {
		if changed == string(raw) {
			t.Fatal("test input unchanged")
		}
		if decodeProtectedJSON([]byte(changed), new(bundle.ImageProof)) == nil {
			t.Fatal("malformed or aliased proof wrapper accepted")
		}
	}
}
