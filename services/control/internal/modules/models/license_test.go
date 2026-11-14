package models

import (
	"errors"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestReviewedModelLicenseBindsExactCatalogAndText(t *testing.T) {
	catalog := wire.ApprovedModel()
	digest := wire.ModelCatalogDigest()
	asset, ok := wire.ModelFileForName("LICENSE")
	record, err := LicenseForCatalog("approved_base", digest)
	if err != nil || !ok || record.CatalogDigest != digest || record.Repository != catalog.Repository ||
		record.Revision != catalog.Revision || record.Identifier != "Apache-2.0" || record.TextSHA256 != asset.SHA256 ||
		!record.CommercialUse || !record.RedistributionWithNotices || record.AttributionOnRedistribution == "" {
		t.Fatalf("unbound reviewed license: %+v, %v", record, err)
	}
	for _, use := range []OnSiteUse{"", OnSiteCommercial, OnSiteNonCommercial} {
		if err := CheckOnSiteUse("approved_base", digest, use); err != nil {
			t.Fatalf("reviewed purpose %q refused: %v", use, err)
		}
	}
	for _, use := range []OnSiteUse{OnSiteUndeclared, "external_distribution"} {
		if err := CheckOnSiteUse("approved_base", digest, use); !errors.Is(err, ErrModelLicenseUseUndeclared) {
			t.Fatalf("unverified purpose %q: %v", use, err)
		}
	}
	for _, source := range []struct{ kind, digest string }{
		{"approved_base", strings.Repeat("0", 64)}, {wire.DerivedModelKind, digest},
	} {
		if err := CheckOnSiteUse(source.kind, source.digest, OnSiteCommercial); !errors.Is(err, ErrModelLicenseUnknown) {
			t.Fatalf("unreviewed catalogue %+v: %v", source, err)
		}
	}
}

func TestRestrictedTermsDoNotAdmitCommercialUse(t *testing.T) {
	record := ModelLicense{}
	if err := record.checkOnSiteUse(OnSiteCommercial); !errors.Is(err, ErrModelLicenseUseDenied) {
		t.Fatalf("commercial use without reviewed permission: %v", err)
	}
	if err := record.checkOnSiteUse(OnSiteNonCommercial); err != nil {
		t.Fatalf("declared non-commercial use refused: %v", err)
	}
}
