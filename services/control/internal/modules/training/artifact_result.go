package training

import (
	"reflect"
	"slices"
	"strings"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func adapterResultFiles(result wire.Result) []datawire.Asset {
	var files []datawire.Asset
	for _, asset := range result.Files {
		if name, ok := strings.CutPrefix(asset.Path, "adapter/"); ok {
			asset.Path = name
			files = append(files, asset)
		}
	}
	return files
}

func decodeAcceptedArtifactResult(command wire.Command, descriptor UploadDescriptor, raw, attemptOutcome []byte) (wire.Result, error) {
	request, e := command.ContainerRequest()
	if e != nil || rawDigest(raw) != descriptor.ManifestSHA256 {
		return wire.Result{}, fail("TRAINING_DATA_INVALID")
	}
	repository := agentwire.ApprovedModel().Repository
	result, e := wire.DecodeResult(raw, request, repository)
	if e != nil || result.Status != "trained" || result.GlobalStep != descriptor.GlobalStep ||
		!slices.Equal(adapterResultFiles(result), descriptor.Files) {
		return wire.Result{}, fail("TRAINING_DATA_INVALID")
	}

	// The SQL outcome is JSON, whose stored representation can differ from
	// the original protected bytes. Compare the two strictly decoded values.
	recorded, e := wire.DecodeResult(attemptOutcome, request, repository)
	if e != nil || !reflect.DeepEqual(recorded, result) {
		return wire.Result{}, fail("TRAINING_DATA_INVALID")
	}
	return result, nil
}
