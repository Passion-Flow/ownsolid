package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type S3 struct {
	closed        atomic.Bool
	client        *s3.Client
	refusalClient *s3.Client
	http          *http.Transport
	configuration config.ObjectStorageConfiguration
	slots         chan struct{}
}
type staticCredentials struct{ access, secret string }

func (c staticCredentials) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: c.access, SecretAccessKey: c.secret, Source: "Project-one protected static S3 files"}, nil
}

// pinnedHTTP prevents SDK endpoint corrections or redirects from leaking signed
// requests to any origin other than the explicitly selected private endpoint.
type pinnedHTTP struct {
	client       *http.Client
	scheme, host string
}

func (p pinnedHTTP) Do(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != p.scheme || r.URL.Host != p.host || r.URL.User != nil {
		return nil, failure("OBJECT_ENDPOINT_CHANGED")
	}
	return p.client.Do(r)
}
func NewS3(c config.ObjectStorageConfiguration) (*S3, error) {
	if !c.Enabled || c.Validate() != nil {
		return nil, failure("OBJECT_CONFIGURATION_INVALID")
	}
	c.DialAddresses = append([]string(nil), c.DialAddresses...)
	c.Management = append([]config.ObjectStorageManagementProbe(nil), c.Management...)
	for i := range c.Management {
		c.Management[i].DialAddresses = append([]string(nil), c.Management[i].DialAddresses...)
	}
	access, secret, e := c.ObjectStorageSecrets()
	if e != nil {
		return nil, e
	}
	refusalAccess, refusalSecret, e := c.ObjectStorageRefusalSecrets()
	if e != nil {
		return nil, e
	}
	if refusalAccess == access || refusalSecret == secret {
		return nil, failure("OBJECT_BUCKET_AUTHORITY_INVALID")
	}
	ca, e := c.ObjectStorageCA()
	if e != nil {
		return nil, e
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(ca) > 0 {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return nil, failure("OBJECT_CA_INVALID")
		}
		tlsConfig.RootCAs = roots
	}
	endpoint, e := url.Parse(c.Endpoint)
	if e != nil {
		return nil, failure("OBJECT_CONFIGURATION_INVALID")
	}
	port := endpoint.Port()
	if port == "" {
		port = "443"
		if endpoint.Scheme == "http" {
			port = "80"
		}
	}
	dialer := &net.Dialer{Timeout: time.Duration(c.OperationSeconds) * time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, TLSHandshakeTimeout: time.Duration(c.OperationSeconds) * time.Second, ResponseHeaderTimeout: time.Duration(c.OperationSeconds) * time.Second, MaxConnsPerHost: c.MaximumConcurrentTransfers, MaxIdleConnsPerHost: c.MaximumConcurrentTransfers, IdleConnTimeout: time.Duration(c.OperationSeconds) * time.Second, ForceAttemptHTTP2: false}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, p, e := net.SplitHostPort(address)
		if e != nil || host != endpoint.Hostname() || p != port {
			return nil, failure("OBJECT_ENDPOINT_CHANGED")
		}
		for _, ip := range c.DialAddresses {
			conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
			if e == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, failure("OBJECT_STORAGE_UNAVAILABLE")
	}
	httpClient := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	client := s3.New(s3.Options{Region: c.Region, BaseEndpoint: aws.String(c.Endpoint), UsePathStyle: true, Credentials: staticCredentials{access, secret}, HTTPClient: pinnedHTTP{httpClient, endpoint.Scheme, endpoint.Host}, Retryer: aws.NopRetryer{}, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired, DisableMultiRegionAccessPoints: true})
	refusalOptions := client.Options()
	refusalOptions.Credentials = staticCredentials{refusalAccess, refusalSecret}
	refusalClient := s3.New(refusalOptions)
	c.DialAddresses = append([]string(nil), c.DialAddresses...)
	return &S3{client: client, refusalClient: refusalClient, http: transport, configuration: c, slots: make(chan struct{}, c.MaximumConcurrentTransfers)}, nil
}
func (s *S3) Identity() string { return backendDigest(s.configuration) }
func (s *S3) Close() error {
	s.closed.Store(true)
	s.http.CloseIdleConnections()
	return nil
}
func safe(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, context.Canceled) {
		return &contextFailure{code: "OBJECT_CANCELLED", cause: context.Canceled}
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return &contextFailure{code: "OBJECT_TIMEOUT", cause: context.DeadlineExceeded}
	}
	var api smithy.APIError
	if errors.As(e, &api) {
		switch api.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return failure("OBJECT_NOT_FOUND")
		case "NoSuchUpload":
			return failure("OBJECT_UPLOAD_NOT_FOUND")
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return failure("OBJECT_ACCESS_DENIED")
		case "PreconditionFailed", "ConditionalRequestConflict":
			return failure("OBJECT_CONFLICT")
		case "NotImplemented", "InvalidRequest", "UnsupportedOperation":
			return failure("OBJECT_CAPABILITY_UNSUPPORTED")
		}
	}
	return failure("OBJECT_STORAGE_UNAVAILABLE")
}

// Unknown mutation outcomes are deliberately distinguishable from definite
// authorization/precondition refusals. The owner retains intent and reconciles.
func mutationError(e error) error {
	if e == nil {
		return nil
	}
	mapped := safe(e)
	switch mapped.Error() {
	case "OBJECT_ACCESS_DENIED", "OBJECT_CONFLICT", "OBJECT_CAPABILITY_UNSUPPORTED", "OBJECT_UPLOAD_NOT_FOUND", "OBJECT_NOT_FOUND":
		return mapped
	}
	return failure("OBJECT_REMOTE_OUTCOME_UNKNOWN")
}
func (s *S3) operation(ctx context.Context) (context.Context, func(), error) {
	if s.closed.Load() {
		return nil, func() {}, failure("OBJECT_STORAGE_CLOSED")
	}
	if ctx == nil {
		return nil, func() {}, failure("OBJECT_REQUEST_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.OperationSeconds)*time.Second)
	select {
	case s.slots <- struct{}{}:
		return ctx, func() { <-s.slots; cancel() }, nil
	case <-ctx.Done():
		cancel()
		return nil, func() {}, safe(ctx.Err())
	}
}
func (s *S3) VerifyProfile(ctx context.Context) error {
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return e
	}
	defer done()
	bucket := aws.String(s.configuration.Bucket)
	if _, e = s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: bucket}); e != nil {
		return safe(e)
	}
	v, e := s.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: bucket})
	if e != nil {
		return safe(e)
	}
	if v.Status != "" {
		return failure("OBJECT_CAPABILITY_UNSUPPORTED")
	}
	lock, e := s.client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: bucket})
	if e == nil {
		if lock.ObjectLockConfiguration != nil && lock.ObjectLockConfiguration.ObjectLockEnabled != "" {
			return failure("OBJECT_CAPABILITY_UNSUPPORTED")
		}
	} else {
		var api smithy.APIError
		if !errors.As(e, &api) || api.ErrorCode() != "ObjectLockConfigurationNotFoundError" && api.ErrorCode() != "ObjectLockConfigurationNotFound" {
			return safe(e)
		}
	}
	encryption, e := s.client.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: bucket})
	if e != nil {
		var api smithy.APIError
		if !errors.As(e, &api) || api.ErrorCode() != "ServerSideEncryptionConfigurationNotFoundError" {
			return safe(e)
		}
	} else if encryption.ServerSideEncryptionConfiguration != nil {
		for _, r := range encryption.ServerSideEncryptionConfiguration.Rules {
			if r.ApplyServerSideEncryptionByDefault != nil && r.ApplyServerSideEncryptionByDefault.SSEAlgorithm != types.ServerSideEncryptionAes256 {
				return failure("OBJECT_CAPABILITY_UNSUPPORTED")
			}
		}
	}
	if e = s.verifyBucketAuthority(ctx); e != nil {
		return e
	}
	if e = s.privateBucket(ctx); e != nil {
		return e
	}
	if e = s.verifyManagement(ctx); e != nil {
		return e
	}
	return nil
}
func (s *S3) Create(ctx context.Context, r Ref, metadata map[string]string) (Upload, error) {
	key, e := r.Key()
	if e != nil {
		return Upload{}, e
	}
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return Upload{}, e
	}
	defer done()
	if len(metadata) != 3 || !ValidDigest(metadata["sha256"]) || metadata["object-id"] != r.Object {
		return Upload{}, failure("OBJECT_REQUEST_INVALID")
	}
	n, e := strconv.ParseInt(metadata["bytes"], 10, 64)
	if e != nil || n < 1 || n > s.configuration.MaximumObjectBytes {
		return Upload{}, failure("OBJECT_REQUEST_INVALID")
	}
	out, e := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(s.configuration.Bucket), Key: aws.String(key), ContentType: aws.String("application/octet-stream"), Metadata: metadata, ChecksumAlgorithm: types.ChecksumAlgorithmSha256})
	if e != nil {
		return Upload{}, mutationError(e)
	}
	if aws.ToString(out.Bucket) != s.configuration.Bucket || aws.ToString(out.Key) != key || !validUploadID(aws.ToString(out.UploadId)) {
		return Upload{}, failure("OBJECT_REMOTE_OUTCOME_UNKNOWN")
	}
	return Upload{aws.ToString(out.UploadId)}, nil
}
func (s *S3) FindUploads(ctx context.Context, r Ref) ([]Upload, error) {
	key, e := r.Key()
	if e != nil {
		return nil, e
	}
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	out := []Upload{}
	seenUploads := map[string]bool{}
	type cursor struct{ key, upload string }
	seenCursors := map[cursor]bool{{}: true}
	var marker, id *string
	for range s.configuration.MaximumListPages {
		page, e := s.client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(s.configuration.Bucket), Prefix: aws.String(key), KeyMarker: marker, UploadIdMarker: id, MaxUploads: aws.Int32(1000)})
		if e != nil {
			return nil, safe(e)
		}
		if aws.ToString(page.Bucket) != s.configuration.Bucket {
			return nil, failure("OBJECT_RESPONSE_INVALID")
		}
		if page.IsTruncated == nil {
			return nil, failure("OBJECT_LIST_INCOMPLETE")
		}
		for _, u := range page.Uploads {
			if aws.ToString(u.Key) != key {
				continue
			}
			upload := aws.ToString(u.UploadId)
			if !validUploadID(upload) || seenUploads[upload] || len(out) >= MaximumMultipartParts {
				return nil, failure("OBJECT_RESPONSE_INVALID")
			}
			seenUploads[upload] = true
			out = append(out, Upload{upload})
		}
		if !aws.ToBool(page.IsTruncated) {
			return out, nil
		}
		next := cursor{aws.ToString(page.NextKeyMarker), aws.ToString(page.NextUploadIdMarker)}
		if seenCursors[next] {
			return nil, failure("OBJECT_LIST_INCOMPLETE")
		}
		seenCursors[next] = true
		marker, id = page.NextKeyMarker, page.NextUploadIdMarker
	}
	return nil, failure("OBJECT_LIST_INCOMPLETE")
}
func (s *S3) PutPart(ctx context.Context, r Ref, id string, part Part, source io.ReadSeeker) (Part, error) {
	key, e := r.Key()
	if e != nil {
		return Part{}, e
	}
	if !validUploadID(id) || part.Number < 1 || part.Number > MaximumMultipartParts || part.Bytes < 1 || part.Bytes > s.configuration.PartBytes || !ValidDigest(part.SHA256) || source == nil {
		return Part{}, failure("OBJECT_PART_INVALID")
	}
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return Part{}, e
	}
	defer done()
	position, e := source.Seek(0, io.SeekCurrent)
	if e != nil {
		return Part{}, failure("OBJECT_PART_INVALID")
	}
	hash := sha256.New()
	count, e := io.Copy(hash, io.LimitReader(contextReader{ctx, source}, part.Bytes+1))
	if e != nil || count != part.Bytes || hex.EncodeToString(hash.Sum(nil)) != part.SHA256 {
		return Part{}, failure("OBJECT_PART_INVALID")
	}
	if _, e = source.Seek(position, io.SeekStart); e != nil {
		return Part{}, failure("OBJECT_PART_INVALID")
	}
	checksum := base64.StdEncoding.EncodeToString(hash.Sum(nil))
	out, e := s.client.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String(s.configuration.Bucket), Key: aws.String(key), UploadId: aws.String(id), PartNumber: aws.Int32(part.Number), ContentLength: aws.Int64(part.Bytes), ChecksumSHA256: aws.String(checksum), Body: source})
	if e != nil {
		return Part{}, mutationError(e)
	}
	if !validETag(aws.ToString(out.ETag)) {
		return Part{}, failure("OBJECT_REMOTE_OUTCOME_UNKNOWN")
	}
	if v := aws.ToString(out.ChecksumSHA256); v != "" && v != checksum {
		return Part{}, failure("OBJECT_DIGEST_MISMATCH")
	}
	part.ETag = aws.ToString(out.ETag)
	return part, nil
}
func (s *S3) Parts(ctx context.Context, r Ref, id string) ([]Part, error) {
	key, e := r.Key()
	if e != nil {
		return nil, e
	}
	if !validUploadID(id) {
		return nil, failure("OBJECT_UPLOAD_INVALID")
	}
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	out := []Part{}
	seenMarkers := map[string]bool{"": true}
	var marker *string
	for range s.configuration.MaximumListPages {
		page, e := s.client.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String(s.configuration.Bucket), Key: aws.String(key), UploadId: aws.String(id), PartNumberMarker: marker, MaxParts: aws.Int32(1000)})
		if e != nil {
			return nil, safe(e)
		}
		if aws.ToString(page.Bucket) != s.configuration.Bucket || aws.ToString(page.Key) != key || aws.ToString(page.UploadId) != id {
			return nil, failure("OBJECT_RESPONSE_INVALID")
		}
		if page.IsTruncated == nil {
			return nil, failure("OBJECT_LIST_INCOMPLETE")
		}
		for _, p := range page.Parts {
			hash := ""
			if raw, e := base64.StdEncoding.DecodeString(aws.ToString(p.ChecksumSHA256)); e == nil && len(raw) == 32 {
				hash = hex.EncodeToString(raw)
			}
			number := aws.ToInt32(p.PartNumber)
			size := aws.ToInt64(p.Size)
			if number < 1 || number > MaximumMultipartParts || size < 1 || size > s.configuration.PartBytes || !validETag(aws.ToString(p.ETag)) || len(out) >= MaximumMultipartParts || len(out) > 0 && out[len(out)-1].Number >= number {
				return nil, failure("OBJECT_RESPONSE_INVALID")
			}
			out = append(out, Part{Number: aws.ToInt32(p.PartNumber), Bytes: aws.ToInt64(p.Size), SHA256: hash, ETag: aws.ToString(p.ETag)})
		}
		if !aws.ToBool(page.IsTruncated) {
			return out, nil
		}
		next := aws.ToString(page.NextPartNumberMarker)
		if seenMarkers[next] {
			return nil, failure("OBJECT_LIST_INCOMPLETE")
		}
		seenMarkers[next] = true
		marker = page.NextPartNumberMarker
	}
	return nil, failure("OBJECT_LIST_INCOMPLETE")
}
func (s *S3) Complete(ctx context.Context, r Ref, id string, parts []Part) (Info, error) {
	key, e := r.Key()
	if e != nil {
		return Info{}, e
	}
	if !validUploadID(id) || len(parts) < 1 || len(parts) > MaximumMultipartParts {
		return Info{}, failure("OBJECT_PART_INVALID")
	}
	completed := make([]types.CompletedPart, len(parts))
	remaining := s.configuration.MaximumObjectBytes
	for i, p := range parts {
		if p.Number != int32(i+1) || !validETag(p.ETag) || !ValidDigest(p.SHA256) {
			return Info{}, failure("OBJECT_PART_INVALID")
		}
		if p.Bytes < 1 || p.Bytes > s.configuration.PartBytes || i < len(parts)-1 && p.Bytes != s.configuration.PartBytes || p.Bytes > remaining {
			return Info{}, failure("OBJECT_PART_INVALID")
		}
		remaining -= p.Bytes
		raw, _ := hex.DecodeString(p.SHA256)
		completed[i] = types.CompletedPart{PartNumber: aws.Int32(p.Number), ETag: aws.String(p.ETag), ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(raw))}
	}
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return Info{}, e
	}
	defer done()
	out, e := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String(s.configuration.Bucket), Key: aws.String(key), UploadId: aws.String(id), MultipartUpload: &types.CompletedMultipartUpload{Parts: completed}, IfNoneMatch: aws.String("*")})
	if e != nil {
		return Info{}, mutationError(e)
	}
	if aws.ToString(out.Bucket) != s.configuration.Bucket || aws.ToString(out.Key) != key || !validETag(aws.ToString(out.ETag)) {
		return Info{}, failure("OBJECT_REMOTE_OUTCOME_UNKNOWN")
	}
	return Info{ETag: aws.ToString(out.ETag)}, nil
}
func (s *S3) Abort(ctx context.Context, r Ref, id string) error {
	key, e := r.Key()
	if e != nil {
		return e
	}
	if !validUploadID(id) {
		return failure("OBJECT_UPLOAD_INVALID")
	}
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return e
	}
	defer done()
	_, e = s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(s.configuration.Bucket), Key: aws.String(key), UploadId: aws.String(id)})
	if e != nil {
		mapped := mutationError(e)
		if mapped.Error() != "OBJECT_UPLOAD_NOT_FOUND" {
			return mapped
		}
	}
	return nil
}
func (s *S3) Head(ctx context.Context, r Ref) (Info, error) {
	key, e := r.Key()
	if e != nil {
		return Info{}, e
	}
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return Info{}, e
	}
	defer done()
	out, e := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.configuration.Bucket), Key: aws.String(key)})
	if e != nil {
		return Info{}, safe(e)
	}
	if !validETag(aws.ToString(out.ETag)) || aws.ToInt64(out.ContentLength) < 1 {
		return Info{}, failure("OBJECT_RESPONSE_INVALID")
	}
	return Info{Bytes: aws.ToInt64(out.ContentLength), ETag: aws.ToString(out.ETag), Metadata: out.Metadata}, nil
}
func (s *S3) Open(ctx context.Context, r Ref, etag string) (Download, error) {
	if s.closed.Load() {
		return Download{}, failure("OBJECT_STORAGE_CLOSED")
	}
	key, e := r.Key()
	if e != nil {
		return Download{}, e
	}
	if !validETag(etag) {
		return Download{}, failure("OBJECT_REQUEST_INVALID")
	}
	if ctx == nil {
		return Download{}, failure("OBJECT_REQUEST_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.TransferSeconds)*time.Second)
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		cancel()
		return Download{}, safe(ctx.Err())
	}
	release := func() { <-s.slots; cancel() }
	out, e := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.configuration.Bucket), Key: aws.String(key), IfMatch: aws.String(etag)})
	if e != nil {
		release()
		return Download{}, safe(e)
	}
	if out.Body == nil || aws.ToString(out.ETag) != etag {
		if out.Body != nil {
			out.Body.Close()
		}
		release()
		return Download{}, failure("OBJECT_CHANGED")
	}
	return Download{Body: &ownedBody{body: out.Body, release: release}, Info: Info{Bytes: aws.ToInt64(out.ContentLength), ETag: aws.ToString(out.ETag), Metadata: out.Metadata}}, nil
}
func (s *S3) Delete(ctx context.Context, r Ref, etag string) error {
	key, e := r.Key()
	if e != nil {
		return e
	}
	if !validETag(etag) {
		return failure("OBJECT_REQUEST_INVALID")
	}
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return e
	}
	defer done()
	_, e = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.configuration.Bucket), Key: aws.String(key), IfMatch: aws.String(etag)})
	return mutationError(e)
}

type ownedBody struct {
	body    io.ReadCloser
	release func()
	once    sync.Once
}

func (b *ownedBody) Read(p []byte) (int, error) { return b.body.Read(p) }
func (b *ownedBody) Close() error               { e := b.body.Close(); b.once.Do(b.release); return e }

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.reader.Read(p)
}

var _ Transport = (*S3)(nil)

func (s *S3) MatchesConfiguration(c config.ObjectStorageConfiguration) bool {
	a, _ := json.Marshal(s.configuration)
	b, _ := json.Marshal(c)
	return bytes.Equal(a, b)
}
func validETag(v string) bool {
	if len(v) < 2 || len(v) > 1024 || v[0] != '"' || v[len(v)-1] != '"' {
		return false
	}
	for _, r := range v[1 : len(v)-1] {
		if r < 0x21 || r > 0x7e || r == '"' {
			return false
		}
	}
	return true
}
func validUploadID(v string) bool {
	return len(v) > 0 && len(v) <= 4096 && utf8.ValidString(v) && !strings.ContainsFunc(v, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) })
}

// Only fixed context sentinels survive; vendor errors never escape this boundary.
type contextFailure struct {
	code  string
	cause error
}

func (e *contextFailure) Error() string        { return e.code }
func (e *contextFailure) Is(target error) bool { return errors.Is(e.cause, target) }
