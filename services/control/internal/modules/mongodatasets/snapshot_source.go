package mongodatasets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// SnapshotSource pins the exact protected URI/CA bytes used to construct the
// real driver. Its fingerprint is private receipt provenance, never an API DTO.
type SnapshotSource struct {
	source  config.MongoDBSource
	policy  config.MongoDBConfiguration
	options *options.ClientOptions
	digest  string
}

func (*SnapshotSource) String() string   { return "MONGODB_SNAPSHOT_SOURCE_REDACTED" }
func (*SnapshotSource) GoString() string { return "MONGODB_SNAPSHOT_SOURCE_REDACTED" }
func (s *SnapshotSource) Fingerprint() string {
	if s == nil {
		return ""
	}
	return s.digest
}

// CaptureSource uses the existing protected-file and typed-policy owners.
// It grants no identity/Workspace permission and opens no network connection.
func CaptureSource(source config.MongoDBSource, policy config.MongoDBConfiguration) (*SnapshotSource, error) {
	if policy.Validate() != nil || !policy.Enabled {
		return nil, errors.New("MONGODB_CONFIGURATION_INVALID")
	}
	matched := false
	encoded, e := json.Marshal(source)
	if e != nil {
		return nil, errors.New("MONGODB_CONFIGURATION_INVALID")
	}
	for _, v := range policy.Sources {
		raw, _ := json.Marshal(v)
		if string(raw) == string(encoded) {
			matched = true
			break
		}
	}
	if !matched {
		return nil, errors.New("MONGODB_SOURCE_CHANGED")
	}
	uri, e := config.MongoDBSecret(source.URIFile)
	if e != nil {
		return nil, errors.New("MONGODB_CONFIGURATION_INVALID")
	}
	var ca []byte
	if source.CAFile != "" {
		ca, e = config.MongoDBCA(source.CAFile)
		if e != nil {
			return nil, errors.New("MONGODB_CONFIGURATION_INVALID")
		}
	}
	o, e := sourceOptionsBytes(source, policy, uri, ca)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(struct {
		Version int
		Source  config.MongoDBSource
		URI, CA []byte
	}{1, source, uri, ca})
	if e != nil {
		return nil, errors.New("MONGODB_CONFIGURATION_INVALID")
	}
	hash := sha256.Sum256(raw)
	source.Fields = append([]string(nil), source.Fields...)
	return &SnapshotSource{source, policy, o, hex.EncodeToString(hash[:])}, nil
}

func (s *SnapshotSource) Read(ctx context.Context, mapping Mapping, limits wire.Limits, preflight bool) (Result, error) {
	if s == nil || s.options == nil || ctx == nil {
		return Result{}, errors.New("MONGODB_CONFIGURATION_INVALID")
	}
	if e := mapping.Validate(s.source); e != nil {
		return Result{}, e
	}
	return readOptions(ctx, s.source, s.policy, mapping, limits, preflight, s.options)
}
