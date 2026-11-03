package objectstore

import (
	"context"
	"errors"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// The initial profile permits exactly one bucket per identity. Request one extra
// entry so a broader grant cannot hide behind a single-entry response page.
const privateBucketInventoryLimit int32 = 2
const privateCanaryListLimit int32 = 1

func emptyCanaryListing(out *s3.ListObjectsV2Output, bucket string) bool {
	return out != nil && aws.ToString(out.Name) == bucket && aws.ToInt32(out.MaxKeys) == privateCanaryListLimit &&
		out.KeyCount != nil && aws.ToInt32(out.KeyCount) == 0 && out.IsTruncated != nil && !aws.ToBool(out.IsTruncated) &&
		len(out.Contents) == 0 && len(out.CommonPrefixes) == 0 && aws.ToString(out.Prefix) == "" &&
		aws.ToString(out.Delimiter) == "" && aws.ToString(out.StartAfter) == "" &&
		aws.ToString(out.ContinuationToken) == "" && aws.ToString(out.NextContinuationToken) == ""
}

func existingListOnlyCanary(ctx context.Context, client *s3.Client, bucket string) error {
	out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), MaxKeys: aws.Int32(privateCanaryListLimit)})
	if err != nil || !emptyCanaryListing(out, bucket) {
		return failure("OBJECT_BUCKET_AUTHORITY_UNVERIFIED")
	}
	return nil
}

func bucketPermissionDenied(err error, head bool) bool {
	var response *smithyhttp.ResponseError
	if !errors.As(err, &response) || response.Response == nil || response.Response.Response == nil || response.HTTPStatusCode() != http.StatusForbidden {
		return false
	}
	var api smithy.APIError
	if !errors.As(err, &api) {
		return head
	}
	// HEAD has no XML body and the SDK may supply only the HTTP reason phrase.
	// A signature error, missing bucket or arbitrary service code is not proof.
	return api.ErrorCode() == "AccessDenied" || head && api.ErrorCode() == "Forbidden"
}

func singleBucketInventory(ctx context.Context, client *s3.Client, bucket string) error {
	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{MaxBuckets: aws.Int32(privateBucketInventoryLimit)})
	if err != nil || out == nil || len(out.Buckets) != 1 || aws.ToString(out.Buckets[0].Name) != bucket || aws.ToString(out.ContinuationToken) != "" {
		return failure("OBJECT_BUCKET_AUTHORITY_UNVERIFIED")
	}
	return nil
}

func deniedBucketReads(ctx context.Context, client *s3.Client, bucket string, encryption bool) error {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if !bucketPermissionDenied(err, true) {
		return failure("OBJECT_BUCKET_AUTHORITY_UNVERIFIED")
	}
	_, err = client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), MaxKeys: aws.Int32(1)})
	if !bucketPermissionDenied(err, false) {
		return failure("OBJECT_BUCKET_AUTHORITY_UNVERIFIED")
	}
	if encryption {
		_, err = client.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(bucket)})
		if !bucketPermissionDenied(err, false) {
			return failure("OBJECT_BUCKET_AUTHORITY_UNVERIFIED")
		}
	}
	return nil
}

func (s *S3) verifyBucketAuthority(ctx context.Context) error {
	if s.client == nil || s.refusalClient == nil {
		return failure("OBJECT_BUCKET_AUTHORITY_UNVERIFIED")
	}
	// SeaweedFS4.48 HEAD requires Read; the observer intentionally has List only.
	// Prove its real empty bucket by a bounded parsed listing, without granting Read.
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.configuration.Bucket)}); err != nil {
		return failure("OBJECT_BUCKET_AUTHORITY_UNVERIFIED")
	}
	if err := singleBucketInventory(ctx, s.client, s.configuration.Bucket); err != nil {
		return err
	}
	if err := existingListOnlyCanary(ctx, s.refusalClient, s.configuration.RefusalBucket); err != nil {
		return err
	}
	if err := singleBucketInventory(ctx, s.refusalClient, s.configuration.RefusalBucket); err != nil {
		return err
	}
	if _, err := s.refusalClient.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.configuration.RefusalBucket)}); !bucketPermissionDenied(err, true) {
		return failure("OBJECT_BUCKET_AUTHORITY_UNVERIFIED")
	}
	if err := deniedBucketReads(ctx, s.client, s.configuration.RefusalBucket, true); err != nil {
		return err
	}
	if err := deniedBucketReads(ctx, s.refusalClient, s.configuration.Bucket, false); err != nil {
		return err
	}
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.configuration.Bucket)}); err != nil {
		return failure("OBJECT_BUCKET_AUTHORITY_UNVERIFIED")
	}
	return existingListOnlyCanary(ctx, s.refusalClient, s.configuration.RefusalBucket)
}
