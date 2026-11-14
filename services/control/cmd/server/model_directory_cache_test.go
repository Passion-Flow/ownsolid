package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	modelhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/models/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestModelDirectoryCacheCompositionRetainsActualOwnerAndOptionalAvailability(t *testing.T) {
	c := config.Config{}
	if e := openModelDirectoryCache(c, nil); e != nil {
		t.Fatal("disabled client allocated an owner", e)
	}
	c.ModelCache = config.DefaultModelCacheConfiguration()
	c.ModelCache.Enabled = true
	c.ModelCache.Address = "127.0.0.1:44499" // No listener: construction must not dial.
	c.ModelCache.TLS = false
	c.ModelCache.Username = "synthetic_cache"
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	c.ModelCache.PasswordFile = filepath.Join(root, "password")
	if e := os.WriteFile(c.ModelCache.PasswordFile, []byte("synthetic-cache-password"), 0600); e != nil {
		t.Fatal(e)
	}
	owner := &identity.Service{}
	h := &identityhttp.Handler{Service: owner, Models: &modelhttp.Handler{Service: &models.Service{Identity: owner}}}
	for _, invalid := range []*identityhttp.Handler{nil, {}, {Service: owner}, {Service: owner, Models: &modelhttp.Handler{}}, {Service: owner, Models: &modelhttp.Handler{Service: &models.Service{Identity: &identity.Service{}}}}} {
		if e := openModelDirectoryCache(c, invalid); e == nil || e.Error() != "MODEL_CACHE_OWNER_REQUIRED" {
			t.Fatal("foreign/missing model owner admitted", e)
		}
	}
	if e := openModelDirectoryCache(c, h); e != nil || h.Models.Service.DirectoryCache == nil {
		t.Fatal("actual current model owner was not assembled", e)
	}
	if e := openModelDirectoryCache(c, h); e == nil {
		t.Fatal("a second cache replaced the current owner")
	}
}
