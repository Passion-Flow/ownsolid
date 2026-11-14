package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// AWSConfiguration owns read-only discovery. No ambient AWS credentials are read.
type AWSConfiguration struct {
	MaximumConcurrentImports int             `yaml:"maximum_concurrent_imports" json:"maximum_concurrent_imports"`
	Enabled                  bool            `yaml:"enabled" json:"enabled"`
	Connections              []AWSConnection `yaml:"connections" json:"connections"`
	RequestSeconds           int             `yaml:"request_seconds" json:"request_seconds"`
	ImportSeconds            int             `yaml:"import_seconds" json:"import_seconds"`
	RetryAttempts            int             `yaml:"retry_attempts" json:"retry_attempts"`
	RetryMilliseconds        int             `yaml:"retry_milliseconds" json:"retry_milliseconds"`
	PageSize                 int             `yaml:"page_size" json:"page_size"`
	MaximumPages             int             `yaml:"maximum_pages" json:"maximum_pages"`
	MaximumResources         int             `yaml:"maximum_resources" json:"maximum_resources"`
	MaximumReceipts          int             `yaml:"maximum_receipts" json:"maximum_receipts"`
	MaximumResponseBytes     int             `yaml:"maximum_response_bytes" json:"maximum_response_bytes"`
}
type AWSConnection struct {
	Name            string   `yaml:"name" json:"name"`
	Partition       string   `yaml:"partition" json:"partition"`
	AccountID       string   `yaml:"account_id" json:"account_id"`
	Regions         []string `yaml:"regions" json:"regions"`
	WorkspaceIDs    []string `yaml:"workspace_ids" json:"workspace_ids"`
	CredentialsFile string   `yaml:"credentials_file" json:"credentials_file"`
}

func DefaultAWSConfiguration() AWSConfiguration {
	return AWSConfiguration{MaximumConcurrentImports: 1, RequestSeconds: 10, ImportSeconds: 25, RetryAttempts: 3, RetryMilliseconds: 250, PageSize: 100, MaximumPages: 100, MaximumResources: 10000, MaximumReceipts: 10000, MaximumResponseBytes: 4 << 20}
}
func (c AWSConfiguration) Validate() error {
	bad := errors.New("CONFIG_AWS_INVALID")
	if c.MaximumConcurrentImports < 1 || c.MaximumConcurrentImports > 8 || c.RequestSeconds < 1 || c.RequestSeconds > 60 || c.ImportSeconds < c.RequestSeconds || c.ImportSeconds > 600 || c.RetryAttempts < 1 || c.RetryAttempts > 5 || c.RetryMilliseconds < 1 || c.RetryMilliseconds > 5000 || c.PageSize < 5 || c.PageSize > 1000 || c.MaximumPages < 1 || c.MaximumPages > 1000 || c.MaximumResources < 1 || c.MaximumResources > 100000 || c.MaximumReceipts < 1 || c.MaximumReceipts > 100000 || c.MaximumResponseBytes < 1024 || c.MaximumResponseBytes > 16<<20 || len(c.Connections) > 100 {
		return bad
	}
	if c.Enabled && len(c.Connections) == 0 {
		return bad
	}
	names := map[string]bool{}
	for _, a := range c.Connections {
		if !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,47}$`).MatchString(a.Name) || names[a.Name] || !regexp.MustCompile(`^[0-9]{12}$`).MatchString(a.AccountID) || !filepath.IsAbs(a.CredentialsFile) || filepath.Clean(a.CredentialsFile) != a.CredentialsFile || len(a.Regions) == 0 || len(a.Regions) > 100 || len(a.WorkspaceIDs) == 0 || len(a.WorkspaceIDs) > 1000 {
			return bad
		}
		names[a.Name] = true
		seen := map[string]bool{}
		for _, r := range a.Regions {
			if !AWSRegion(a.Partition, r) || seen[r] {
				return bad
			}
			seen[r] = true
		}
		seen = map[string]bool{}
		for _, w := range a.WorkspaceIDs {
			id, e := uuid.Parse(w)
			if e != nil || id == uuid.Nil || id.String() != w || seen[w] {
				return bad
			}
			seen[w] = true
		}
	}
	return nil
}
func AWSRegion(partition, region string) bool {
	if !regexp.MustCompile(`^[a-z]{2}(-[a-z]+){1,2}-[1-9][0-9]?$`).MatchString(region) {
		return false
	}
	switch partition {
	case "aws":
		return !strings.HasPrefix(region, "cn-") && !strings.HasPrefix(region, "us-gov-") && !strings.Contains(region, "-iso")
	case "aws-cn":
		return strings.HasPrefix(region, "cn-")
	case "aws-us-gov":
		return strings.HasPrefix(region, "us-gov-")
	}
	return false
}
func (c *AWSConfiguration) UnmarshalYAML(node *yaml.Node) error {
	if strictKnowledgeYAML(node, reflect.TypeOf(*c)) != nil {
		return errors.New("CONFIG_AWS_INVALID")
	}
	type plain AWSConfiguration
	v := plain(*c)
	if node.Decode(&v) != nil {
		return errors.New("CONFIG_AWS_INVALID")
	}
	*c = AWSConfiguration(v)
	return nil
}
func ReadAWSCredentials(path string) ([]byte, error) {
	return readPrivateFile(path, 16<<10, "AWS_CREDENTIALS_INVALID")
}
func awsEnvironment(key string) bool { return strings.HasPrefix(key, "PROJECT_ONE_AWS_") }
func (c *AWSConfiguration) applyEnvironment(key, value string) error {
	bad := errors.New("CONFIG_AWS_ENVIRONMENT_INVALID")
	if key == "PROJECT_ONE_AWS_CONNECTIONS_FILE" {
		raw, e := readPrivateFile(value, MaximumPolicyEnvironmentBytes, "CONFIG_AWS_CONNECTIONS_INVALID")
		if e != nil {
			return e
		}
		var connections []AWSConnection
		if strict.DecodeStrict(raw, &connections) != nil || connections == nil {
			return bad
		}
		c.Connections = connections
		return nil
	}
	if key == "PROJECT_ONE_AWS_ENABLED" {
		if value != "true" && value != "false" {
			return bad
		}
		c.Enabled = value == "true"
		return nil
	}
	fields := map[string]*int{"MAXIMUM_CONCURRENT_IMPORTS": &c.MaximumConcurrentImports, "REQUEST_SECONDS": &c.RequestSeconds, "IMPORT_SECONDS": &c.ImportSeconds, "RETRY_ATTEMPTS": &c.RetryAttempts, "RETRY_MILLISECONDS": &c.RetryMilliseconds, "PAGE_SIZE": &c.PageSize, "MAXIMUM_PAGES": &c.MaximumPages, "MAXIMUM_RESOURCES": &c.MaximumResources, "MAXIMUM_RECEIPTS": &c.MaximumReceipts, "MAXIMUM_RESPONSE_BYTES": &c.MaximumResponseBytes}
	p := fields[strings.TrimPrefix(key, "PROJECT_ONE_AWS_")]
	if p == nil {
		return bad
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return bad
		}
	}
	n, e := strconv.Atoi(value)
	if e != nil {
		return bad
	}
	*p = n
	return nil
}

func validateAWSYAML(raw []byte) error {
	var node yaml.Node
	if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_AWS_INVALID")
	}
	root := node.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "aws" && strictKnowledgeYAML(root.Content[i+1], reflect.TypeOf(AWSConfiguration{})) != nil {
			return errors.New("CONFIG_AWS_INVALID")
		}
	}
	return nil
}

func (c AWSConfiguration) omitted() bool { return reflect.DeepEqual(c, AWSConfiguration{}) }
