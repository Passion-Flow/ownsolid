package imageregistry

import (
	"encoding/json"
	"math"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Database JSON codecs may add a separator after each structural token. The
// canonical metadata budget is unchanged; this bounds decoding that representation.
const databaseJSONExpansionFactor int64 = 2

type storedPinAvailability uint8

const (
	storedPinReady storedPinAvailability = iota
	storedPinRecovery
	storedPinDeleted
)

// storedPinState classifies persisted intent facts, never an external outcome.
// Recovery availability permits control operations, not image data or grants.
func storedPinState(p Pin, owner, phase string) (storedPinAvailability, error) {
	bad := failure("REGISTRY_STORED_STATE_INVALID")
	if !validID(owner) {
		return storedPinRecovery, bad
	}
	bound := len(p.inventory) != 0
	for _, present := range []bool{p.ManifestDigest != nil, p.ConfigDigest != nil, p.ArchiveSHA256 != nil, p.ArchiveBytes != nil, p.inventorySHA != nil} {
		if present != bound {
			return storedPinRecovery, bad
		}
	}
	if bound && (!validDigest(*p.ManifestDigest) || !validDigest(*p.ConfigDigest) || !release.ValidSHA256(*p.ArchiveSHA256) || !release.ValidSHA256(*p.inventorySHA) || *p.ArchiveBytes < 1) {
		return storedPinRecovery, bad
	}
	switch p.State {
	case "ready":
		if bound && phase == "completed" {
			return storedPinReady, nil
		}
	case "importing":
		if !bound && phase == "prepared" || bound && phase == "remote" {
			return storedPinRecovery, nil
		}
	case "unknown":
		if bound && (phase == "remote" || phase == "unknown") {
			return storedPinRecovery, nil
		}
	case "deleting":
		if bound && phase == "completed" {
			return storedPinRecovery, nil
		}
	case "failed":
		if !bound && phase == "prepared" {
			return storedPinRecovery, nil
		}
	case "deleted":
		if !bound && phase == "prepared" || bound && phase == "completed" {
			return storedPinDeleted, nil
		}
	}
	return storedPinRecovery, bad
}

func storedBoundMetadata(p Pin, maximum int64) error {
	if len(p.inventory) == 0 {
		return nil
	}
	if maximum < 1 || maximum > math.MaxInt64/databaseJSONExpansionFactor || int64(len(p.inventory)) > maximum*databaseJSONExpansionFactor {
		return failure("REGISTRY_STORED_STATE_INVALID")
	}
	var image cachedImage
	if !json.Valid(p.inventory) || release.StrictJSON(p.inventory, &image) != nil || image.Asset.ID != p.AssetID || cachedMatches(p, image) != nil {
		return failure("REGISTRY_STORED_STATE_INVALID")
	}
	canonical, err := json.Marshal(image)
	if err != nil || int64(len(canonical)) > maximum {
		return failure("REGISTRY_STORED_STATE_INVALID")
	}
	return nil
}
