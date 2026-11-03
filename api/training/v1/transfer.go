package v1

import (
	"errors"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"regexp"
)

type InputFileRequest struct {
	LeaseToken string `json:"lease_token"`
	Kind       string `json:"kind"`
	Asset      string `json:"asset"`
	Offset     int64  `json:"offset"`
	Length     int64  `json:"length"`
}

var flatAsset = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
var machineToken = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func (r InputFileRequest) Validate() error {
	if !machineToken.MatchString(r.LeaseToken) || r.Kind != "model" && r.Kind != "dataset" && r.Kind != "checkpoint" || !flatAsset.MatchString(r.Asset) || r.Asset == "." || r.Asset == ".." || r.Offset < 0 || r.Offset%TransferChunkBytes != 0 || r.Length < 1 || r.Length > TransferChunkBytes {
		return errors.New("TRAINING_TRANSFER_INVALID")
	}
	return nil
}
func (r InputFileRequest) ValidateAsset(asset datawire.Asset) error {
	if r.Validate() != nil || r.Asset != asset.Path || asset.SizeBytes < 1 || !datawire.ValidDigest(asset.SHA256) || r.Offset >= asset.SizeBytes || r.Length != min(TransferChunkBytes, asset.SizeBytes-r.Offset) {
		return errors.New("TRAINING_TRANSFER_INVALID")
	}
	return nil
}
