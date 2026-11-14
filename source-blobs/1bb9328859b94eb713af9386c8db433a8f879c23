package view

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Configuration is the single policy shape shared by the original publisher
// binding and protected Control reader. Parsing grants no file or SQL authority.
type Configuration struct {
	Directory           string `json:"directory"`
	ScopeID             string `json:"scope_id"`
	ManifestSHA256      string `json:"manifest_sha256"`
	ConfigurationSHA256 string `json:"configuration_sha256"`
	PublisherUID        int    `json:"publisher_uid"`
	PageSize            int    `json:"page_size"`
	OperationSeconds    int    `json:"operation_seconds"`
}

func (c Configuration) Validate() error {
	if !filepath.IsAbs(c.Directory) || filepath.Clean(c.Directory) != c.Directory || strings.ContainsAny(c.Directory, "\x00\r\n") || !ValidScopeID(c.ScopeID) || !wire.ValidSHA256(c.ManifestSHA256) || !wire.ValidSHA256(c.ConfigurationSHA256) || c.PublisherUID < 0 || c.PublisherUID > 2147483647 || c.PageSize < 1 || c.PageSize > 100 || c.OperationSeconds < 1 || c.OperationSeconds > 30 {
		return errors.New("RELEASE_VIEW_CONFIGURATION_INVALID")
	}
	return nil
}

func ParseConfiguration(raw []byte) (Configuration, error) {
	var c Configuration
	bad := errors.New("RELEASE_VIEW_CONFIGURATION_INVALID")
	var fields map[string]json.RawMessage
	if wire.StrictJSON(raw, &fields) != nil || len(fields) != 7 {
		return c, bad
	}
	for _, name := range []string{"directory", "scope_id", "manifest_sha256", "configuration_sha256", "publisher_uid", "page_size", "operation_seconds"} {
		if _, present := fields[name]; !present {
			return c, bad
		}
	}
	if wire.StrictJSON(raw, &c) != nil || c.Validate() != nil {
		return Configuration{}, bad
	}
	return c, nil
}
