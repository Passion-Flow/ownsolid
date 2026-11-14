package v1

import "errors"

const ModelTransferChunkBytes int64 = 8 * 1024 * 1024

// ModelFileRequest identifies only an approved file and one bounded immutable range.
type ModelFileRequest struct {
	LeaseToken string `json:"lease_token"`
	File       string `json:"file"`
	Offset     int64  `json:"offset"`
	Length     int64  `json:"length"`
}

func (r ModelFileRequest) Validate() error {
	f, ok := ModelFileForName(r.File)
	if !ok || r.Offset < 0 || r.Offset%ModelTransferChunkBytes != 0 || r.Offset >= f.SizeBytes || r.Length != min(ModelTransferChunkBytes, f.SizeBytes-r.Offset) {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
