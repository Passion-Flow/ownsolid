package models

import "errors"

// OnSiteUse declares the installation's purpose for workloads inside the site.
type OnSiteUse string

const (
	// OnSiteCommercial permits workloads only under terms that allow commercial use.
	OnSiteCommercial OnSiteUse = "internal_commercial"
	// OnSiteNonCommercial declares an internal non-commercial workload.
	OnSiteNonCommercial OnSiteUse = "internal_noncommercial"
	// OnSiteUndeclared prevents a new workload from starting.
	OnSiteUndeclared OnSiteUse = "undeclared"
)

var (
	// ErrModelLicenseUnknown means the exact catalogue has no reviewed terms.
	ErrModelLicenseUnknown = errors.New("MODEL_LICENSE_UNKNOWN")
	// ErrModelLicenseUseUndeclared means the installation cannot establish its purpose.
	ErrModelLicenseUseUndeclared = errors.New("MODEL_LICENSE_USE_UNDECLARED")
	// ErrModelLicenseUseDenied means the reviewed terms exclude the declared purpose.
	ErrModelLicenseUseDenied = errors.New("MODEL_LICENSE_USE_DENIED")
)

// Validate accepts the legacy empty configuration as commercial on-site use.
func (use OnSiteUse) Validate() error {
	switch use {
	case "", OnSiteCommercial, OnSiteNonCommercial, OnSiteUndeclared:
		return nil
	default:
		return ErrModelLicenseUseUndeclared
	}
}

// ModelLicense describes reviewed terms for one verified catalogue.
type ModelLicense struct {
	CatalogDigest               string
	Repository                  string
	Revision                    string
	Identifier                  string
	TextSHA256                  string
	CommercialUse               bool
	RedistributionWithNotices   bool
	AttributionOnRedistribution string
}

// LicenseForCatalog resolves only reviewed terms for a kind and digest obtained
// from the scoped Models lookup. A derived catalogue's base license is provenance.
func LicenseForCatalog(kind, digest string) (ModelLicense, error) {
	if kind != "approved_base" || digest != qwenImportManifest.digest {
		return ModelLicense{}, ErrModelLicenseUnknown
	}
	source := qwenImportManifest
	text, ok := source.file("LICENSE")
	if !ok || source.catalog.License != "Apache-2.0" {
		return ModelLicense{}, ErrModelLicenseUnknown
	}
	return ModelLicense{
		CatalogDigest: digest, Repository: source.catalog.Repository, Revision: source.catalog.Revision,
		Identifier: source.catalog.License, TextSHA256: text.SHA256, CommercialUse: true,
		RedistributionWithNotices:   true,
		AttributionOnRedistribution: "Keep the license and applicable notices, and identify modifications.",
	}, nil
}

// CheckOnSiteUse evaluates the reviewed catalogue under the installation's purpose.
func CheckOnSiteUse(kind, digest string, use OnSiteUse) error {
	license, err := LicenseForCatalog(kind, digest)
	if err != nil {
		return err
	}
	return license.checkOnSiteUse(use)
}

func (license ModelLicense) checkOnSiteUse(use OnSiteUse) error {
	if use.Validate() != nil || use == OnSiteUndeclared {
		return ErrModelLicenseUseUndeclared
	}
	if use != OnSiteNonCommercial && !license.CommercialUse {
		return ErrModelLicenseUseDenied
	}
	return nil
}
