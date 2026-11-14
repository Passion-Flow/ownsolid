package config

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// ObjectStorageConfiguration selects one optional private S3 ingress transport.
// Existing model/dataset roots remain the artifact publication authorities.
type ObjectStorageConfiguration struct {
	Enabled                    bool                           `yaml:"enabled" json:"enabled"`
	Backend                    string                         `yaml:"backend" json:"backend"`
	Endpoint                   string                         `yaml:"endpoint" json:"endpoint"`
	Region                     string                         `yaml:"region" json:"region"`
	Bucket                     string                         `yaml:"bucket" json:"bucket"`
	RefusalBucket              string                         `yaml:"refusal_bucket" json:"refusal_bucket"`
	RefusalAccessKeyFile       string                         `yaml:"refusal_access_key_file" json:"refusal_access_key_file"`
	RefusalSecretKeyFile       string                         `yaml:"refusal_secret_key_file" json:"refusal_secret_key_file"`
	Transport                  string                         `yaml:"transport" json:"transport"`
	AccessKeyFile              string                         `yaml:"access_key_file" json:"access_key_file"`
	SecretKeyFile              string                         `yaml:"secret_key_file" json:"secret_key_file"`
	CAFile                     string                         `yaml:"ca_file" json:"ca_file"`
	DialAddresses              []string                       `yaml:"dial_addresses" json:"dial_addresses"`
	StagingRoot                string                         `yaml:"staging_root" json:"staging_root"`
	PartBytes                  int64                          `yaml:"part_bytes" json:"part_bytes"`
	MaximumObjectBytes         int64                          `yaml:"maximum_object_bytes" json:"maximum_object_bytes"`
	MinimumFreeBytes           int64                          `yaml:"minimum_free_bytes" json:"minimum_free_bytes"`
	OperationSeconds           int                            `yaml:"operation_seconds" json:"operation_seconds"`
	TransferSeconds            int                            `yaml:"transfer_seconds" json:"transfer_seconds"`
	UploadSeconds              int                            `yaml:"upload_seconds" json:"upload_seconds"`
	ObjectLifetimeSeconds      int                            `yaml:"object_lifetime_seconds" json:"object_lifetime_seconds"`
	MaximumGrantSeconds        int                            `yaml:"maximum_grant_seconds" json:"maximum_grant_seconds"`
	MaximumConcurrentTransfers int                            `yaml:"maximum_concurrent_transfers" json:"maximum_concurrent_transfers"`
	MaximumListPages           int                            `yaml:"maximum_list_pages" json:"maximum_list_pages"`
	WorkerPollSeconds          int                            `yaml:"worker_poll_seconds" json:"worker_poll_seconds"`
	Management                 []ObjectStorageManagementProbe `yaml:"management" json:"management"`
}

const MinimumS3PartBytes int64 = 5 << 20
const MaximumS3PartBytes int64 = 5 << 30
const MaximumS3Parts = 10000

// This initial project envelope deliberately bounds staging and whole readback.
const ObjectStorageSchemaMaximumBytes int64 = 5 << 40

func (c *ObjectStorageConfiguration) Validate(otherRoots ...string) error {
	bad := errors.New("CONFIG_OBJECT_STORAGE_INVALID")
	if c == nil {
		return nil
	}
	if !c.Enabled {
		zero := *c
		zero.Enabled = false
		if !reflect.DeepEqual(zero, ObjectStorageConfiguration{}) {
			return bad
		}
		return nil
	}
	if c.Backend != "seaweedfs_s3" || c.Region == "" || len(c.Region) > 64 || strings.ContainsFunc(c.Region, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return bad
	}
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || strings.Contains(u.Hostname(), "%") || u.RawPath != "" {
		return bad
	}
	if c.Transport == "https" {
		if u.Scheme != "https" {
			return bad
		}
	} else if c.Transport == "http_private" {
		if u.Scheme != "http" || c.CAFile != "" {
			return bad
		}
	} else {
		return bad
	}
	if u.Port() != "" {
		p, e := strconv.Atoi(u.Port())
		if e != nil || p < 1 || p > 65535 {
			return bad
		}
	}
	if !objectStorageBucket(c.Bucket) || !objectStorageBucket(c.RefusalBucket) || c.RefusalBucket == c.Bucket {
		return bad
	}
	if len(c.DialAddresses) < 1 || len(c.DialAddresses) > 8 {
		return bad
	}
	seen := map[string]bool{}
	for _, value := range c.DialAddresses {
		a, e := netip.ParseAddr(value)
		if e != nil || a.String() != value || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || seen[value] || c.Transport == "http_private" && !a.IsPrivate() && !a.IsLoopback() {
			return bad
		}
		seen[value] = true
	}
	for _, p := range []string{c.AccessKeyFile, c.SecretKeyFile, c.RefusalAccessKeyFile, c.RefusalSecretKeyFile, c.StagingRoot} {
		if !objectStoragePath(p) {
			return bad
		}
	}
	references := map[string]bool{}
	for _, p := range []string{c.AccessKeyFile, c.SecretKeyFile, c.RefusalAccessKeyFile, c.RefusalSecretKeyFile} {
		if references[p] {
			return bad
		}
		references[p] = true
	}
	if c.CAFile != "" && (!objectStoragePath(c.CAFile) || references[c.CAFile]) {
		return bad
	}
	for _, p := range append(otherRoots, c.AccessKeyFile, c.SecretKeyFile, c.RefusalAccessKeyFile, c.RefusalSecretKeyFile, c.CAFile) {
		if p != "" && (p == c.StagingRoot || strings.HasPrefix(p, c.StagingRoot+string(filepath.Separator)) || strings.HasPrefix(c.StagingRoot, p+string(filepath.Separator))) {
			return bad
		}
	}
	if c.PartBytes < MinimumS3PartBytes || c.PartBytes > MaximumS3PartBytes || c.MaximumObjectBytes < 1 || c.MaximumObjectBytes > ObjectStorageSchemaMaximumBytes || (c.MaximumObjectBytes+c.PartBytes-1)/c.PartBytes > MaximumS3Parts || c.MinimumFreeBytes < 4096 {
		return bad
	}
	if c.OperationSeconds < 1 || c.OperationSeconds > 300 || c.TransferSeconds < c.OperationSeconds || c.TransferSeconds > 86400 || c.UploadSeconds < c.TransferSeconds || c.UploadSeconds > 604800 || c.ObjectLifetimeSeconds < c.UploadSeconds || c.ObjectLifetimeSeconds > 31536000 || c.MaximumGrantSeconds < 1 || c.MaximumGrantSeconds > c.ObjectLifetimeSeconds || c.MaximumConcurrentTransfers < 1 || c.MaximumConcurrentTransfers > 8 || c.MaximumListPages < 1 || c.MaximumListPages > 100 {
		return bad
	}
	if c.WorkerPollSeconds < 1 || c.WorkerPollSeconds > 60 || len(c.Management) != 4 {
		return bad
	}
	kinds := map[string]bool{}
	targets := map[string]bool{}
	for _, probe := range c.Management {
		if probe.Validate() != nil || kinds[probe.Kind] {
			return bad
		}
		kinds[probe.Kind] = true
		_, port, e := net.SplitHostPort(probe.Authority)
		if e != nil {
			return bad
		}
		for _, address := range probe.DialAddresses {
			target := net.JoinHostPort(address, port)
			if targets[target] {
				return bad
			}
			targets[target] = true
		}

		for _, path := range []string{probe.CAFile, probe.ClientCertificateFile, probe.ClientKeyFile} {
			if path == c.StagingRoot || strings.HasPrefix(path, c.StagingRoot+string(filepath.Separator)) || strings.HasPrefix(c.StagingRoot, path+string(filepath.Separator)) {
				return bad
			}
		}
	}

	return nil
}
func objectStorageBucket(bucket string) bool {
	if len(bucket) < 3 || len(bucket) > 63 || strings.Contains(bucket, "..") || net.ParseIP(bucket) != nil {
		return false
	}
	for i, r := range bucket {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || (r == '-' || r == '.') && i > 0 && i < len(bucket)-1) {
			return false
		}
	}
	return true
}
func objectStoragePath(p string) bool {
	return p != "/" && filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsFunc(p, unicode.IsControl)
}
func validateObjectStorageYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_OBJECT_STORAGE_INVALID")
	}
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "object_storage" {
			if strictKnowledgeYAML(root.Content[i+1], reflect.TypeOf(ObjectStorageConfiguration{})) != nil {
				return errors.New("CONFIG_OBJECT_STORAGE_INVALID")
			}
		}
	}
	return nil
}

// ObjectStorageSecrets is consumed only by the explicitly configured S3 client.
// No ambient AWS environment, shared credentials or metadata service is read.
func (c *ObjectStorageConfiguration) ObjectStorageSecrets() (string, string, error) {
	bad := errors.New("CONFIG_OBJECT_STORAGE_SECRET_INVALID")
	if c == nil || !c.Enabled || c.Validate() != nil {
		return "", "", bad
	}
	return objectStorageCredentialFiles(c.AccessKeyFile, c.SecretKeyFile)
}
func (c *ObjectStorageConfiguration) ObjectStorageRefusalSecrets() (string, string, error) {
	if c == nil || !c.Enabled || c.Validate() != nil {
		return "", "", errors.New("CONFIG_OBJECT_STORAGE_SECRET_INVALID")
	}
	return objectStorageCredentialFiles(c.RefusalAccessKeyFile, c.RefusalSecretKeyFile)
}
func objectStorageCredentialFiles(accessFile, secretFile string) (string, string, error) {
	bad := errors.New("CONFIG_OBJECT_STORAGE_SECRET_INVALID")
	access, e := readPrivateFile(accessFile, 4096, "CONFIG_OBJECT_STORAGE_SECRET_INVALID")
	if e != nil {
		return "", "", bad
	}
	secret, e := readPrivateFile(secretFile, 4096, "CONFIG_OBJECT_STORAGE_SECRET_INVALID")
	if e != nil {
		return "", "", bad
	}
	a, b := strings.TrimSpace(string(access)), strings.TrimSpace(string(secret))
	if len(a) < 1 || len(a) > 256 || len(b) < 1 || len(b) > 1024 || strings.ContainsFunc(a+b, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return "", "", bad
	}
	return a, b, nil
}

func (c *ObjectStorageConfiguration) ObjectStorageCA() ([]byte, error) {
	if c.CAFile == "" {
		return nil, nil
	}
	return readPrivateFile(c.CAFile, 1<<20, "CONFIG_OBJECT_STORAGE_CA_INVALID")
}
