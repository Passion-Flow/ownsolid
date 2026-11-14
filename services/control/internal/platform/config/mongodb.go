package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const mongoDBConfigEnvironment = "PROJECT_ONE_MONGODB_CONFIG_FILE"
const maximumMongoDBConfigurationBytes = 1 << 20

// MongoDBConfiguration is loaded once through a protected operator file, never from a request.
type MongoDBConfiguration struct {
	Enabled                  bool            `json:"enabled"`
	TimeoutSeconds           int             `json:"timeout_seconds"`
	CloseSeconds             int             `json:"close_seconds"`
	BatchSize                int32           `json:"batch_size"`
	MaximumConcurrent        int             `json:"maximum_concurrent"`
	MaximumAttempts          int             `json:"maximum_attempts"`
	MaximumImportsPerDataset int             `json:"maximum_imports_per_dataset"`
	Sources                  []MongoDBSource `json:"sources"`
}
type MongoDBSource struct {
	ID          string   `json:"id"`
	TenantID    string   `json:"tenant_id"`
	WorkspaceID string   `json:"workspace_id"`
	URIFile     string   `json:"uri_file"`
	CAFile      string   `json:"ca_file"`
	TLS         bool     `json:"tls"`
	Database    string   `json:"database"`
	Collection  string   `json:"collection"`
	Fields      []string `json:"fields"`
}

var mongoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)
var mongoField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

func ValidMongoDBField(field string) bool {
	return len(field) <= 255 && field != "_id" && !strings.HasPrefix(field, "_id.") && mongoField.MatchString(field)
}
func DefaultMongoDBConfiguration() MongoDBConfiguration {
	return MongoDBConfiguration{TimeoutSeconds: 15, CloseSeconds: 2, BatchSize: 100, MaximumConcurrent: 1, MaximumAttempts: 3, MaximumImportsPerDataset: 100, Sources: []MongoDBSource{}}
}
func (m MongoDBConfiguration) Validate() error {
	bad := errors.New("CONFIG_MONGODB_INVALID")
	if m.TimeoutSeconds < 1 || m.TimeoutSeconds > 120 || m.CloseSeconds < 1 || m.CloseSeconds > 10 || m.BatchSize < 1 || m.BatchSize > 1000 || m.MaximumConcurrent < 1 || m.MaximumConcurrent > 2 || m.MaximumAttempts < 1 || m.MaximumAttempts > 20 || m.MaximumImportsPerDataset < 1 || m.MaximumImportsPerDataset > 1000 || len(m.Sources) > 1000 {
		return bad
	}
	if !m.Enabled && len(m.Sources) != 0 || m.Enabled && len(m.Sources) == 0 {
		return bad
	}
	seen := map[string]bool{}
	for _, s := range m.Sources {
		if !mongoName.MatchString(s.ID) || seen[s.ID] || !wire.ValidID(s.TenantID) || !wire.ValidID(s.WorkspaceID) || !mongoName.MatchString(s.Database) || !mongoName.MatchString(s.Collection) || strings.HasPrefix(s.Collection, "system") || !filepath.IsAbs(s.URIFile) || filepath.Clean(s.URIFile) != s.URIFile || s.CAFile != "" && (!s.TLS || !filepath.IsAbs(s.CAFile) || filepath.Clean(s.CAFile) != s.CAFile) || len(s.Fields) < 1 || len(s.Fields) > 100 {
			return bad
		}
		seen[s.ID] = true
		paths := map[string]bool{}
		for _, field := range s.Fields {
			if !ValidMongoDBField(field) || paths[field] {
				return bad
			}
			paths[field] = true
		}
	}
	return nil
}
func (c *Config) resolveMongoDBConfiguration() error {
	c.MongoDB = DefaultMongoDBConfiguration()
	if c.MongoDBConfigFile == "" {
		return nil
	}
	raw, e := readPrivateFile(c.MongoDBConfigFile, maximumMongoDBConfigurationBytes, "CONFIG_MONGODB_PRIVATE_FILE_INVALID")
	if e != nil {
		return e
	}
	// DecodeStrict rejects duplicate/unknown keys and malformed Unicode as well as incorrect boolean types.
	if wire.DecodeStrict(raw, &c.MongoDB) != nil {
		return errors.New("CONFIG_MONGODB_INVALID")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var value any
	if d.Decode(&value) != nil || d.Decode(&value) != io.EOF {
		return errors.New("CONFIG_MONGODB_INVALID")
	}
	return c.MongoDB.Validate()
}

// MongoDBSecret pins and validates the private secret file before reading bounded bytes.
func MongoDBSecret(path string) ([]byte, error) {
	return readPrivateFile(path, 8192, "MONGODB_CONFIGURATION_INVALID")
}

// MongoDBCA reads a bounded private trust bundle through the same pinned-file boundary.
func MongoDBCA(path string) ([]byte, error) {
	return readPrivateFile(path, 1<<20, "MONGODB_CONFIGURATION_INVALID")
}
