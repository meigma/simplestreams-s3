// Package s3store adapts AWS SDK for Go v2 operations to application object ports.
package s3store

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	transfertypes "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/meigma/simplestreams-s3/internal/config"
	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/proxy"
	"github.com/meigma/simplestreams-s3/internal/publish"
)

const (
	uploadPartSize           = int64(8 << 20)
	uploadThreshold          = int64(16 << 20)
	uploadWorkers            = 4
	testEndpointEnvironment  = "SIMPLESTREAMS_S3_TEST_S3_ENDPOINT"
	testPathStyleEnvironment = "SIMPLESTREAMS_S3_TEST_S3_PATH_STYLE"
	testThresholdEnvironment = "SIMPLESTREAMS_S3_TEST_UPLOAD_THRESHOLD_BYTES"
)

// Store implements publish and proxy ports with authenticated AWS S3 calls.
type Store struct {
	client              *s3.Client
	uploader            *transfermanager.Client
	bucket              object.BucketName
	expectedBucketOwner *string
}

// clientOptions are hidden integration hooks and do not extend production compatibility.
type clientOptions struct {
	baseEndpoint    string
	pathStyle       bool
	uploadThreshold int64
}

// New constructs an AWS S3 adapter through the default credential and region chain.
func New(ctx context.Context, runtime config.S3, catalogTimeout time.Duration) (*Store, error) {
	testOptions, err := clientOptionsFromEnvironment()
	if err != nil {
		return nil, err
	}
	return newStore(ctx, runtime, catalogTimeout, testOptions)
}

// clientOptionsFromEnvironment reads deliberately unsupported local integration hooks.
func clientOptionsFromEnvironment() (clientOptions, error) {
	endpoint := strings.TrimSpace(os.Getenv(testEndpointEnvironment))
	pathStyleValue := strings.TrimSpace(os.Getenv(testPathStyleEnvironment))
	thresholdValue := strings.TrimSpace(os.Getenv(testThresholdEnvironment))
	if endpoint == "" && pathStyleValue == "" && thresholdValue == "" {
		return clientOptions{}, nil
	}
	if endpoint == "" {
		return clientOptions{}, failure.New(
			failure.KindInvalidInput,
			"configure S3 test endpoint",
			"path-style test access requires a test endpoint",
		)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return clientOptions{}, failure.New(
			failure.KindInvalidInput,
			"configure S3 test endpoint",
			"test endpoint must be an absolute HTTP or HTTPS URL",
		)
	}
	pathStyle := false
	if pathStyleValue != "" {
		pathStyle, err = strconv.ParseBool(pathStyleValue)
		if err != nil {
			return clientOptions{}, failure.Wrap(
				failure.KindInvalidInput,
				"configure S3 test endpoint",
				fmt.Errorf("parse path-style test setting: %w", err),
			)
		}
	}
	threshold := int64(0)
	if thresholdValue != "" {
		threshold, err = strconv.ParseInt(thresholdValue, 10, 64)
		if err != nil || threshold <= 0 {
			return clientOptions{}, failure.New(
				failure.KindInvalidInput,
				"configure S3 test endpoint",
				"test upload threshold must be a positive byte count",
			)
		}
	}
	return clientOptions{
		baseEndpoint:    endpoint,
		pathStyle:       pathStyle,
		uploadThreshold: threshold,
	}, nil
}

// newStore constructs an adapter with optional local integration endpoint hooks.
func newStore(
	ctx context.Context,
	runtime config.S3,
	catalogTimeout time.Duration,
	testOptions clientOptions,
) (*Store, error) {
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, failure.New(failure.KindInternal, "configure S3 transport", "default transport is not HTTP")
	}
	transport := baseTransport.Clone()
	transport.DialContext = (&net.Dialer{Timeout: runtime.DialTimeout}).DialContext
	transport.TLSHandshakeTimeout = runtime.TLSHandshakeTimeout
	transport.ResponseHeaderTimeout = runtime.ResponseHeaderTimeout

	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithHTTPClient(&http.Client{Transport: transport}),
		awsconfig.WithRetryer(func() aws.Retryer {
			return awsretry.NewStandard(func(options *awsretry.StandardOptions) {
				options.MaxAttempts = runtime.MaxAttempts
				options.MaxBackoff = runtime.MaxBackoff
			})
		}),
	}
	if runtime.Region != "" {
		loadOptions = append(loadOptions, awsconfig.WithRegion(runtime.Region))
	}
	if runtime.Profile != "" {
		loadOptions = append(loadOptions, awsconfig.WithSharedConfigProfile(runtime.Profile))
	}
	awsConfiguration, err := awsconfig.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, classify("load AWS configuration", err)
	}
	client := s3.NewFromConfig(awsConfiguration, func(options *s3.Options) {
		options.BaseEndpoint = optionalString(testOptions.baseEndpoint)
		options.UsePathStyle = testOptions.pathStyle
	})
	uploader := transfermanager.New(client, func(options *transfermanager.Options) {
		options.PartSizeBytes = uploadPartSize
		options.MultipartUploadThreshold = uploadThreshold
		if testOptions.uploadThreshold > 0 {
			options.MultipartUploadThreshold = testOptions.uploadThreshold
		}
		options.Concurrency = uploadWorkers
		options.FailTimeout = catalogTimeout
		options.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	return &Store{
		client:              client,
		uploader:            uploader,
		bucket:              runtime.Bucket,
		expectedBucketOwner: optionalString(runtime.ExpectedBucketOwner),
	}, nil
}

// Exists reports whether an exact object key is visible through HeadObject.
func (store *Store) Exists(ctx context.Context, key object.ObjectKey) (bool, error) {
	_, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:              aws.String(store.bucket.String()),
		Key:                 aws.String(key.String()),
		ExpectedBucketOwner: store.expectedBucketOwner,
	})
	if err == nil {
		return true, nil
	}
	if isNotFound(err) {
		return false, nil
	}
	return false, classify("head object", err)
}

// Create performs a create-only upload with expected checksums and bounded buffering.
func (store *Store) Create(ctx context.Context, input publish.CreateObject) error {
	body, err := input.Open()
	if err != nil {
		return err
	}
	defer body.Close()

	uploadInput := &transfermanager.UploadObjectInput{
		Bucket:              aws.String(store.bucket.String()),
		Key:                 aws.String(input.Key.String()),
		Body:                body,
		ContentLength:       aws.Int64(input.Size.Int64()),
		MpuObjectSize:       aws.Int64(input.Size.Int64()),
		ContentType:         optionalString(input.ContentType),
		ExpectedBucketOwner: store.expectedBucketOwner,
		IfNoneMatch:         aws.String("*"),
		Metadata:            map[string]string{"sha256": input.SHA256.String()},
	}
	if input.CRC64NVME != nil {
		uploadInput.ChecksumAlgorithm = transfertypes.ChecksumAlgorithm("CRC64NVME")
		uploadInput.ChecksumCRC64NVME = aws.String(encodeCRC64NVME(*input.CRC64NVME))
	} else {
		uploadInput.ChecksumAlgorithm = transfertypes.ChecksumAlgorithmSha256
		uploadInput.ChecksumSHA256 = aws.String(encodeSHA256(input.SHA256))
	}
	if _, err := store.uploader.UploadObject(ctx, uploadInput); err != nil {
		return classify("create object", err)
	}
	return nil
}

// Head performs an authenticated exact-key metadata read.
func (store *Store) Head(ctx context.Context, key object.ObjectKey) (proxy.Attributes, error) {
	output, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:              aws.String(store.bucket.String()),
		Key:                 aws.String(key.String()),
		ExpectedBucketOwner: store.expectedBucketOwner,
	})
	if err != nil {
		return proxy.Attributes{}, classify("head object", err)
	}
	return attributes(output.ContentLength, output.ContentType, output.ETag, output.LastModified), nil
}

// Get performs an authenticated exact-key streaming read.
func (store *Store) Get(ctx context.Context, key object.ObjectKey) (proxy.Object, error) {
	output, err := store.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:              aws.String(store.bucket.String()),
		Key:                 aws.String(key.String()),
		ExpectedBucketOwner: store.expectedBucketOwner,
	})
	if err != nil {
		return proxy.Object{}, classify("get object", err)
	}
	return proxy.Object{
		Attributes: attributes(output.ContentLength, output.ContentType, output.ETag, output.LastModified),
		Body:       output.Body,
	}, nil
}

// attributes maps AWS output fields into the proxy port model.
func attributes(size *int64, contentType *string, etag *string, modified *time.Time) proxy.Attributes {
	byteSize, err := object.NewByteSize(aws.ToInt64(size))
	if err != nil {
		byteSize = 0
	}
	return proxy.Attributes{
		Size:         byteSize,
		ContentType:  aws.ToString(contentType),
		ETag:         aws.ToString(etag),
		LastModified: aws.ToTime(modified),
	}
}

// encodeCRC64NVME returns the S3 base64 big-endian full-object checksum.
func encodeCRC64NVME(checksum object.CRC64NVME) string {
	encoded := make([]byte, binary.Size(checksum.Uint64()))
	binary.BigEndian.PutUint64(encoded, checksum.Uint64())
	return base64.StdEncoding.EncodeToString(encoded)
}

// encodeSHA256 returns the S3 base64 binary SHA-256 checksum.
func encodeSHA256(digest object.SHA256Digest) string {
	return base64.StdEncoding.EncodeToString(digest.Bytes())
}

// optionalString returns nil for an absent optional AWS string.
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return aws.String(value)
}

// isNotFound identifies S3's missing-key responses without exposing AWS errors inward.
func isNotFound(err error) bool {
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var apiError smithy.APIError
	return errors.As(err, &apiError) && (apiError.ErrorCode() == "NotFound" || apiError.ErrorCode() == "NoSuchKey")
}

// classify translates AWS, Smithy, network, and context errors once at the adapter boundary.
func classify(operation string, err error) error {
	if errors.Is(err, context.Canceled) {
		return failure.Wrap(failure.KindCanceled, operation, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failure.Wrap(failure.KindDeadline, operation, err)
	}
	if isNotFound(err) {
		return failure.Wrap(failure.KindNotFound, operation, err)
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return failure.Wrap(failure.KindUnauthorized, operation, err)
		case "PreconditionFailed", "ConditionalRequestConflict":
			return failure.Wrap(failure.KindPrecondition, operation, err)
		case "RequestTimeout", "RequestTimeoutException":
			return failure.Wrap(failure.KindDeadline, operation, err)
		case "SlowDown", "ServiceUnavailable", "InternalError":
			return failure.Wrap(failure.KindUnavailable, operation, err)
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return failure.Wrap(failure.KindDeadline, operation, err)
		}
		return failure.Wrap(failure.KindUnavailable, operation, err)
	}
	return failure.Wrap(failure.KindInternal, operation, fmt.Errorf("S3 operation failed: %w", err))
}

var _ publish.Store = (*Store)(nil)

var _ proxy.Reader = (*Store)(nil)
