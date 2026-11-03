//go:build object_storage_integration && object_storage_diagnostic

package objectstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const maximumDiagnosticErrorDepth = 12

// This suite observes the real installed SDK and private provider. No SQL or
// remote mutation is performed, and observation alone is not product acceptance.
func TestActualPrivateBucketAuthorityReadback(t *testing.T) {
	cfg, err := config.Load(os.Getenv("PROJECT_ONE_ACTUAL_OBJECT_STORAGE_CONFIGURATION"))
	if err != nil || cfg.ObjectStorage == nil || !cfg.ObjectStorage.Enabled ||
		!regexp.MustCompile(`^m22-fixture-[a-f0-9]{32}$`).MatchString(cfg.ObjectStorage.Bucket) {
		t.Fatal("ACTUAL_SYNTHETIC_PRIVATE_OBJECT_CONFIGURATION_REQUIRED")
	}
	client, err := NewS3(*cfg.ObjectStorage)
	if err != nil {
		t.Fatal("ACTUAL_SDK_CONFIGURATION_REQUIRED")
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error("ACTUAL_SDK_CLOSE_FAILED")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(cfg.ObjectStorage.TransferSeconds)*time.Second)
	defer cancel()
	endpoint, err := url.Parse(cfg.ObjectStorage.Endpoint)
	if err != nil || endpoint.Port() == "" {
		t.Fatal("ACTUAL_EXPLICIT_FIXTURE_ENDPOINT_REQUIRED")
	}
	for _, address := range cfg.ObjectStorage.DialAddresses {
		target := net.JoinHostPort(address, endpoint.Port())
		connection, dialErr := (&net.Dialer{Timeout: time.Duration(cfg.ObjectStorage.OperationSeconds) * time.Second}).DialContext(ctx, "tcp", target)
		value := map[string]any{"operation": "stdlib_private_tcp", "target": target, "connected": dialErr == nil}
		if connection != nil {
			if closeErr := connection.Close(); closeErr != nil {
				t.Error("ACTUAL_TCP_PROBE_CLOSE_FAILED")
			}
		}
		var types []string
		for current, depth := dialErr, 0; current != nil && depth < maximumDiagnosticErrorDepth; current, depth = errors.Unwrap(current), depth+1 {
			types = append(types, fmt.Sprintf("%T", current))
		}
		value["error_types"] = types
		var number syscall.Errno
		if errors.As(dialErr, &number) {
			value["errno"] = int64(number)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal("ACTUAL_TCP_OBSERVATION_ENCODING_FAILED")
		}
		t.Log("ACTUAL_TCP_OBSERVATION " + string(raw))
	}
	type observation struct {
		Identity              string   `json:"identity"`
		Operation             string   `json:"operation"`
		HTTPStatus            int      `json:"http_status,omitempty"`
		APIError              string   `json:"api_error,omitempty"`
		SDKSuccess            bool     `json:"sdk_success"`
		Buckets               []string `json:"buckets,omitempty"`
		Continuation          bool     `json:"continuation"`
		ExpectedControlPassed bool     `json:"expected_control_passed"`
		ErrorTypes            []string `json:"error_types,omitempty"`
		LocalCode             string   `json:"local_code,omitempty"`
		CanaryNameMatches     bool     `json:"canary_name_matches,omitempty"`
		CanaryMaximumKeys     int32    `json:"canary_maximum_keys,omitempty"`
		CanaryCountPresent    bool     `json:"canary_count_present,omitempty"`
		CanaryKeyCount        int32    `json:"canary_key_count,omitempty"`
		CanaryTruncated       bool     `json:"canary_truncated,omitempty"`
	}
	failed := false
	report := func(value observation, err error, valid bool) {
		value.SDKSuccess = err == nil
		for current, depth := err, 0; current != nil && depth < maximumDiagnosticErrorDepth; current, depth = errors.Unwrap(current), depth+1 {
			value.ErrorTypes = append(value.ErrorTypes, fmt.Sprintf("%T", current))
			// Only exact first-party/protocol constants may enter diagnostics.
			// Error messages, request URLs, headers and provider bodies stay private.
			switch current.Error() {
			case "OBJECT_ENDPOINT_CHANGED", "OBJECT_STORAGE_UNAVAILABLE", "OBJECT_STORAGE_CLOSED",
				"not found: ComputePayloadHash", "not found: GetIdentity", "not found: Signing", "not found: ResolveEndpointV2":
				value.LocalCode = current.Error()
			}
		}
		var response *smithyhttp.ResponseError
		if errors.As(err, &response) {
			value.HTTPStatus = response.HTTPStatusCode()
		}
		var api smithy.APIError
		if errors.As(err, &api) {
			switch api.ErrorCode() {
			case "AccessDenied", "Forbidden", "NotFound", "NoSuchBucket", "SignatureDoesNotMatch", "InvalidAccessKeyId",
				"NotImplemented", "InvalidRequest", "InternalError", "SlowDown", "RequestTimeout", "AuthorizationHeaderMalformed":
				value.APIError = api.ErrorCode()
			default:
				value.APIError = "UNREVIEWED_API_ERROR_REDACTED"
			}
		}
		value.ExpectedControlPassed = valid
		raw, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			t.Fatal("ACTUAL_BUCKET_OBSERVATION_ENCODING_FAILED")
		}
		t.Log("ACTUAL_BUCKET_OBSERVATION " + string(raw))
		failed = failed || !valid
	}
	canary := func(client *s3.Client, bucket, operation string) {
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), MaxKeys: aws.Int32(privateCanaryListLimit)})
		value := observation{Identity: "observer", Operation: operation}
		if out != nil {
			value.CanaryNameMatches = aws.ToString(out.Name) == bucket
			value.CanaryMaximumKeys = aws.ToInt32(out.MaxKeys)
			value.CanaryCountPresent = out.KeyCount != nil
			value.CanaryKeyCount = aws.ToInt32(out.KeyCount)
			value.CanaryTruncated = aws.ToBool(out.IsTruncated)
		}
		report(value, err, err == nil && emptyCanaryListing(out, bucket))
	}
	for _, target := range []struct {
		name         string
		client       *s3.Client
		own, foreign string
		encryption   bool
	}{{"controller", client.client, cfg.ObjectStorage.Bucket, cfg.ObjectStorage.RefusalBucket, true},
		{"observer", client.refusalClient, cfg.ObjectStorage.RefusalBucket, cfg.ObjectStorage.Bucket, false}} {
		if target.name == "observer" {
			canary(target.client, target.own, "own_empty_canary")
		} else {
			_, err := target.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(target.own)})
			report(observation{Identity: target.name, Operation: "own_head"}, err, err == nil)
		}
		out, err := target.client.ListBuckets(ctx, &s3.ListBucketsInput{MaxBuckets: aws.Int32(privateBucketInventoryLimit)})
		value := observation{Identity: target.name, Operation: "own_inventory"}
		if out != nil {
			for _, bucket := range out.Buckets {
				name := aws.ToString(bucket.Name)
				if name != target.own && name != target.foreign {
					name = "UNEXPECTED_BUCKET_REDACTED"
				}
				value.Buckets = append(value.Buckets, name)
			}
			value.Continuation = aws.ToString(out.ContinuationToken) != ""
		}
		report(value, err, err == nil && out != nil && len(out.Buckets) == 1 && aws.ToString(out.Buckets[0].Name) == target.own && !value.Continuation)
		if target.name == "observer" {
			_, err := target.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(target.own)})
			report(observation{Identity: target.name, Operation: "own_read_denied"}, err, bucketPermissionDenied(err, true))
		}
		_, err = target.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(target.foreign)})
		report(observation{Identity: target.name, Operation: "foreign_head"}, err, bucketPermissionDenied(err, true))
		_, err = target.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(target.foreign), MaxKeys: aws.Int32(1)})
		report(observation{Identity: target.name, Operation: "foreign_objects"}, err, bucketPermissionDenied(err, false))
		if target.encryption {
			_, err = target.client.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(target.foreign)})
			report(observation{Identity: target.name, Operation: "foreign_encryption"}, err, bucketPermissionDenied(err, false))
		}
		if target.name == "observer" {
			canary(target.client, target.own, "own_canary_readback")
		} else {
			_, err = target.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(target.own)})
			report(observation{Identity: target.name, Operation: "own_head_readback"}, err, err == nil)
		}
	}
	if failed {
		t.Fatal("ACTUAL_STRICT_BUCKET_AUTHORITY_CONTROL_FAILED")
	}
}
