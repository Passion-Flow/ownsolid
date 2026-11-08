package main

import (
	"reflect"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func TestBundleNoticeInventoryUsesSignedPathsAndReferences(t *testing.T) {
	result := bundle.Result{ReleaseID: "test-release", ManifestSHA256: "manifest-digest"}
	result.Manifest.Assets = []wire.Asset{
		{ID: "notice-z", Path: "notices/z.txt", Kind: "license", SHA256: "z-digest"},
		{ID: "model", Path: "model.tar", Kind: "model", Licenses: []string{"notice-a"}},
		{ID: "font", Path: "font.woff2", Kind: "font", Licenses: []string{"notice-z", "notice-a"}},
		{ID: "notice-a", Path: "notices/a.txt", Kind: "license", SHA256: "a-digest"},
	}
	want := noticeInventory{ReleaseID: "test-release", ManifestSHA256: "manifest-digest", Notices: []notice{
		{ID: "notice-a", Path: "notices/a.txt", SHA256: "a-digest", ReferencedBy: []string{"font", "model"}},
		{ID: "notice-z", Path: "notices/z.txt", SHA256: "z-digest", ReferencedBy: []string{"font"}},
	}}
	if got := bundleNotices(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
