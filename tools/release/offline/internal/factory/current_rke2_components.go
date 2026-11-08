package factory

import wire "github.com/Passion-Flow/ownsolid/api/release/v1"

const (
	rke2SPDXID      = "rke2-component-spdx"
	rke2NoticeID    = "rke2-component-notices"
	rke2DocumentID  = "DocumentRef-RKE2"
	rke2DocumentURI = "urn:ownsolid:spdx:rke2:v1.36.6-rke2r1:linux-amd64:1"
)

type spdxChecksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"checksumValue"`
}

type spdxExternalDocument struct {
	ID        string       `json:"externalDocumentId"`
	Namespace string       `json:"spdxDocument"`
	Checksum  spdxChecksum `json:"checksum"`
}

type spdxRelationship struct {
	From string `json:"spdxElementId"`
	Type string `json:"relationshipType"`
	To   string `json:"relatedSpdxElement"`
}

// These pin the reviewed SPDX and notice evidence against the same fixed image
// archives accepted by the Agent. Update the inventory and pins together.
var rke2ComponentAssets = []wire.Asset{
	{ID: rke2SPDXID, Path: "rke2/components.spdx.json", Kind: "sbom",
		Size: 3131733, SHA256: "6062b5a8891cf22d328f7e53d9a6e9861b9601d0f0fa1814b59b5487df58aceb"},
	{ID: rke2NoticeID, Path: "rke2/component-notices.tar", Kind: "license",
		Size: 7393280, SHA256: "fbbfebbf0339f07e17b65f02487792ef7abcc59c5922cca67284a0b7282e16c3"},
}

func checkRKE2SPDXLink(assets []wire.Asset, references []spdxExternalDocument, relationships []spdxRelationship) error {
	var digest string
	for _, asset := range assets {
		if asset.ID == rke2SPDXID {
			digest = asset.SHA256
			break
		}
	}
	found := false
	for _, reference := range references {
		if reference.ID != rke2DocumentID && reference.Namespace != rke2DocumentURI {
			continue
		}
		if found || digest == "" || reference.ID != rke2DocumentID || reference.Namespace != rke2DocumentURI ||
			reference.Checksum != (spdxChecksum{Algorithm: "SHA256", Value: digest}) {
			return fail("RELEASE_RKE2_SPDX_REFERENCE_INVALID")
		}
		found = true
	}
	links := 0
	for _, relationship := range relationships {
		if relationship == (spdxRelationship{From: "SPDXRef-DOCUMENT", Type: "DESCRIBES", To: rke2DocumentID + ":SPDXRef-RKE2"}) {
			links++
		}
	}
	if digest != "" && (!found || links != 1) || digest == "" && links != 0 {
		return fail("RELEASE_RKE2_SPDX_REFERENCE_INVALID")
	}
	return nil
}
