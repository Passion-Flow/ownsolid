package v1

import (
	"errors"
	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
	"regexp"
)

const TransferChunkBytes = training.TransferChunkBytes

var machineToken = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var flatAsset = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

type InputFileRequest struct {
	LeaseToken string `json:"lease_token"`
	Kind       string `json:"kind"`
	Asset      string `json:"asset"`
	Offset     int64  `json:"offset"`
	Length     int64  `json:"length"`
}

func (r InputFileRequest) Validate() error {
	if !machineToken.MatchString(r.LeaseToken) || r.Kind != "model" && r.Kind != "adapter" && r.Kind != "heldout" || !flatAsset.MatchString(r.Asset) || r.Asset == "." || r.Asset == ".." || r.Offset < 0 || r.Offset%TransferChunkBytes != 0 || r.Length < 1 || r.Length > TransferChunkBytes {
		return errors.New("MODEL_PREPARATION_TRANSFER_INVALID")
	}
	return nil
}

func (r InputFileRequest) ValidateAsset(asset dataset.Asset) error {
	if r.Validate() != nil || r.Asset != asset.Path || asset.SizeBytes < 1 || !dataset.ValidDigest(asset.SHA256) || r.Offset >= asset.SizeBytes || r.Length != min(TransferChunkBytes, asset.SizeBytes-r.Offset) {
		return errors.New("MODEL_PREPARATION_TRANSFER_INVALID")
	}
	return nil
}
