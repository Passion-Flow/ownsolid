package config

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func objectStorageConfigurationFixture() ObjectStorageConfiguration {
	return ObjectStorageConfiguration{Enabled: true, Backend: "seaweedfs_s3", Endpoint: "https://store.example.invalid", Transport: "https", Region: "private", Bucket: "projectone-artifacts", RefusalBucket: "projectone-refusal", RefusalAccessKeyFile: "/protected/s3/refusal-access", RefusalSecretKeyFile: "/protected/s3/refusal-secret", AccessKeyFile: "/protected/s3/access", SecretKeyFile: "/protected/s3/secret", CAFile: "/protected/s3/ca.pem", DialAddresses: []string{"127.0.0.1"}, StagingRoot: "/private-artifact-stage", PartBytes: 8 << 20, MaximumObjectBytes: 2 << 30, MinimumFreeBytes: 1 << 30, OperationSeconds: 10, TransferSeconds: 300, UploadSeconds: 3600, ObjectLifetimeSeconds: 86400, MaximumGrantSeconds: 300, MaximumConcurrentTransfers: 1, MaximumListPages: 4, WorkerPollSeconds: 1, Management: objectStorageManagementProbeFixtures()}
}
func TestObjectStorageConfigurationExplicitFalseStrictTypesAndBounds(t *testing.T) {
	c := objectStorageConfigurationFixture()
	if e := c.Validate("/models", "/datasets"); e != nil {
		t.Fatal(e)
	}
	if e := (*ObjectStorageConfiguration)(nil).Validate(); e != nil {
		t.Fatal(e)
	}
	if e := (&ObjectStorageConfiguration{Enabled: false}).Validate(); e != nil {
		t.Fatal("false became true", e)
	}
	for name, change := range map[string]func(*ObjectStorageConfiguration){"disabled_contradiction": func(c *ObjectStorageConfiguration) { c.Enabled = false }, "unknown_backend": func(c *ObjectStorageConfiguration) { c.Backend = "aws" }, "HTTP_without_explicit_mode": func(c *ObjectStorageConfiguration) { c.Endpoint = "http://store.example.invalid" }, "userinfo": func(c *ObjectStorageConfiguration) { c.Endpoint = "https://secret@store.example.invalid" }, "query": func(c *ObjectStorageConfiguration) { c.Endpoint += "?sensitive-value" }, "shared_secret": func(c *ObjectStorageConfiguration) { c.SecretKeyFile = c.AccessKeyFile }, "ambiguous_IP": func(c *ObjectStorageConfiguration) { c.DialAddresses = []string{"127.000.0.1"} }, "part_small": func(c *ObjectStorageConfiguration) { c.PartBytes = 1 }, "parts_overflow": func(c *ObjectStorageConfiguration) { c.MaximumObjectBytes = c.PartBytes*MaximumS3Parts + 1 }, "inverted_deadlines": func(c *ObjectStorageConfiguration) { c.TransferSeconds = 1 }, "staging_secret_overlap": func(c *ObjectStorageConfiguration) { c.StagingRoot = "/protected/s3" }, "bucket_directory": func(c *ObjectStorageConfiguration) { c.Bucket = "private/path" }} {
		t.Run(name, func(t *testing.T) {
			c := objectStorageConfigurationFixture()
			change(&c)
			if e := c.Validate(); e == nil || strings.Contains(e.Error(), "sensitive-value") {
				t.Fatal("invalid configuration accepted or leaked", e)
			}
		})
	}
	for name, change := range map[string]func(*ObjectStorageConfiguration){
		"missing_refusal_bucket":  func(c *ObjectStorageConfiguration) { c.RefusalBucket = "" },
		"same_refusal_bucket":     func(c *ObjectStorageConfiguration) { c.RefusalBucket = c.Bucket },
		"bad_refusal_bucket":      func(c *ObjectStorageConfiguration) { c.RefusalBucket = "foreign/path" },
		"missing_refusal_secret":  func(c *ObjectStorageConfiguration) { c.RefusalSecretKeyFile = "" },
		"same_refusal_reference":  func(c *ObjectStorageConfiguration) { c.RefusalAccessKeyFile = c.AccessKeyFile },
		"refusal_staging_overlap": func(c *ObjectStorageConfiguration) { c.RefusalSecretKeyFile = c.StagingRoot + "/secret" },
	} {
		t.Run(name, func(t *testing.T) {
			c := objectStorageConfigurationFixture()
			change(&c)
			if c.Validate() == nil {
				t.Fatal("unverified dual identity profile accepted")
			}
		})
	}

	for _, value := range []string{"null", "false", "{enabled: 'false'}", "{enabled: null}", "{part_bytes: false}", "{unknown: sensitive-value}", "{enabled: false, enabled: true}", "{enabled: &flag false, backend: *flag}"} {
		if e := validateObjectStorageYAML([]byte("object_storage: " + value)); e == nil {
			t.Fatal("invalid YAML accepted", value)
		}
	}
	if e := validateObjectStorageYAML([]byte("object_storage: {enabled: false}")); e != nil {
		t.Fatal(e)
	}
	c = objectStorageConfigurationFixture()
	c.Endpoint = "http://store.example.invalid"
	c.Transport = "http_private"
	c.CAFile = ""
	if e := c.Validate(); e != nil {
		t.Fatal("explicit private HTTP", e)
	}
	c.DialAddresses = []string{"8.8.8.8"}
	if e := c.Validate(); e == nil {
		t.Fatal("private HTTP accepted public dial")
	}
}
func TestObjectStorageEnvironmentUsesOnlyExplicitProtectedFile(t *testing.T) {
	raw := workspaceConfigInput(t)
	t.Setenv("PROJECT_ONE_OBJECT_STORAGE_ENABLED", "true")
	c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_OBJECT_STORAGE_ENABLED=false\n"))
	if e != nil || c.ObjectStorage == nil || c.ObjectStorage.Enabled {
		t.Fatal("false or ambient override", e)
	}
	for _, body := range []string{"PROJECT_ONE_OBJECT_STORAGE_ENABLED=FALSE", "PROJECT_ONE_OBJECT_STORAGE_ENABLED=1", "PROJECT_ONE_OBJECT_STORAGE_UNKNOWN=sensitive-value", "PROJECT_ONE_OBJECT_STORAGE_PART_BYTES=-1", "PROJECT_ONE_OBJECT_STORAGE_ENDPOINT=", "PROJECT_ONE_OBJECT_STORAGE_ENABLED=false\nPROJECT_ONE_OBJECT_STORAGE_ENDPOINT=https://store.example.invalid", "PROJECT_ONE_OBJECT_STORAGE_ENABLED=true\n"} {
		_, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, body))
		if e == nil || strings.Contains(e.Error(), "sensitive-value") {
			t.Fatal("invalid environment accepted or leaked", e)
		}
	}
	c = Config{ObjectStorage: func() *ObjectStorageConfiguration { v := objectStorageConfigurationFixture(); return &v }()}
	if e = c.applyObjectStorageEnvironment("PROJECT_ONE_OBJECT_STORAGE_MAXIMUM_CONCURRENT_TRANSFERS", "2"); e != nil || c.ObjectStorage.MaximumConcurrentTransfers != 2 {
		t.Fatal("nondefault mapping", e)
	}
	if e = c.applyObjectStorageEnvironment("PROJECT_ONE_OBJECT_STORAGE_DIAL_ADDRESSES", `["127.0.0.1","::1"]`); e != nil || len(c.ObjectStorage.DialAddresses) != 2 {
		t.Fatal("explicit IP list", e)
	}
}
func TestObjectStorageSecretReferenceFailureIsRedacted(t *testing.T) {
	c := objectStorageConfigurationFixture()
	c.AccessKeyFile = "/not-existing/sensitive-value"
	a, s, e := c.ObjectStorageSecrets()
	if e == nil || a != "" || s != "" || strings.Contains(e.Error(), "sensitive-value") {
		t.Fatal("private reference leaked", e)
	}
	c = objectStorageConfigurationFixture()
	root := workspaceEnvironmentFixture(t, "SYNTHETIC_ONLY\n")
	c.AccessKeyFile = root
	c.SecretKeyFile = root + "-other"
	if e = os.WriteFile(c.SecretKeyFile, []byte("SYNTHETIC_SECRET_ONLY"), 0600); e != nil {
		t.Fatal(e)
	}
	// Preserve a meaningful unsafe-mode control even when the runner uses umask077.
	if e = os.Chmod(c.SecretKeyFile, 0644); e != nil {
		t.Fatal(e)
	}
	if info, err := os.Stat(c.SecretKeyFile); err != nil || info.Mode().Perm() != 0644 {
		t.Fatal("public fixture mode was not established", err)
	}
	a, s, e = c.ObjectStorageSecrets()
	if e == nil || a != "" || s != "" {
		t.Fatal("public secret permissions accepted")
	}
}

func objectStorageManagementProbeFixtures() []ObjectStorageManagementProbe {
	out := []ObjectStorageManagementProbe{}
	for index, kind := range []string{"s3_grpc", "master_grpc", "filer_grpc", "volume_grpc"} {
		out = append(out, ObjectStorageManagementProbe{Kind: kind, Authority: "management.example.invalid:" + strconv.Itoa(10443+index), DialAddresses: []string{"127.0.0.1"}, CAFile: "/protected/management-ca", ClientCertificateFile: "/protected/management-certificate", ClientKeyFile: "/protected/management-key"})
	}
	return out
}

func TestObjectStorageManagementEnvironmentRejectsAliasesDuplicatesAndUnknowns(t *testing.T) {
	for _, raw := range []string{`[{"Kind":"s3_grpc"}]`, `[{"kind":"s3_grpc","kind":"filer_grpc"}]`, `[{"unknown":"sensitive-value"}]`, `[{"kind":"s3_grpc","dial_addresses":[null]}]`, `null`, `[] {}`} {
		c := Config{}
		if e := c.applyObjectStorageEnvironment("PROJECT_ONE_OBJECT_STORAGE_MANAGEMENT", raw); e == nil || strings.Contains(e.Error(), "sensitive-value") {
			t.Fatal("noncanonical management accepted", e)
		}
	}
	c := objectStorageConfigurationFixture()
	c.WorkerPollSeconds = 0
	if c.Validate() == nil {
		t.Fatal("missing worker poll accepted")
	}
	c = objectStorageConfigurationFixture()
	c.Management = c.Management[:3]
	if c.Validate() == nil {
		t.Fatal("missing management listener accepted")
	}
	c = objectStorageConfigurationFixture()
	c.Management[0].DialAddresses = []string{"8.8.8.8"}
	if c.Validate() == nil {
		t.Fatal("public management address accepted")
	}
	c = objectStorageConfigurationFixture()
	c.Management[0].Authority = "secret@host:443"
	if c.Validate() == nil {
		t.Fatal("userinfo management authority accepted")
	}
}

func TestObjectManagementProbesCannotRepeatOneListenerUnderFourNames(t *testing.T) {
	c := objectStorageConfigurationFixture()
	c.Management[1].Authority = c.Management[0].Authority
	if c.Validate() == nil {
		t.Fatal("one listener represented several management planes")
	}
}

func TestAggregateObjectStorageRequiresActualLocalOwnerConfiguration(t *testing.T) {
	for _, missing := range []string{"models", "dataset_root", "python", "processor"} {
		c := validConfig()
		v := objectStorageConfigurationFixture()
		c.ObjectStorage = &v
		c.Models.ArtifactRoot = "/local-models"
		c.Datasets = DatasetConfiguration{ArtifactRoot: "/local-datasets", PythonExecutable: "/installed/python", ProcessorFile: "/installed/processor.py"}
		switch missing {
		case "models":
			c.Models.ArtifactRoot = ""
		case "dataset_root":
			c.Datasets.ArtifactRoot = ""
		case "python":
			c.Datasets.PythonExecutable = ""
		case "processor":
			c.Datasets.ProcessorFile = ""
		}
		if e := c.Validate(); e == nil || e.Error() != "CONFIG_OBJECT_LOCAL_OWNERS_REQUIRED" {
			t.Fatal(missing, e)
		}
	}
}
