//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/adapter/s3store"
	"github.com/meigma/simplestreams-s3/internal/config"
	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/publish"
)

const (
	realAWSBucketEnvironment = "SIMPLESTREAMS_S3_REAL_AWS_BUCKET"
	realAWSRegionEnvironment = "SIMPLESTREAMS_S3_REAL_AWS_REGION"
)

// TestRealAWSConditionalIndexWrite proves the AWS-specific compare-and-swap contract.
func TestRealAWSConditionalIndexWrite(t *testing.T) {
	bucketValue := os.Getenv(realAWSBucketEnvironment)
	if bucketValue == "" {
		t.Skip("set SIMPLESTREAMS_S3_REAL_AWS_BUCKET to run the opt-in AWS conformance test")
	}
	region := os.Getenv(realAWSRegionEnvironment)
	if region == "" {
		region = "us-east-1"
	}
	bucket, err := object.NewBucketName(bucketValue)
	require.NoError(t, err)
	key, err := object.NewObjectKey(
		"conformance/" + strconv.FormatInt(time.Now().UnixNano(), 36) + "/streams/v1/index.json",
	)
	require.NoError(t, err)
	runtime := realAWSS3Runtime(bucket, region)
	store, err := s3store.New(t.Context(), runtime, config.DefaultCatalogTimeout)
	require.NoError(t, err)
	registerRealAWSCleanup(t, runtime, key)
	first := realAWSObject(t, key, []byte(`{"generation":1}`))
	second := realAWSObject(t, key, []byte(`{"generation":2}`))

	require.NoError(t, store.Commit(t.Context(), first, ""))
	observed, err := store.Open(t.Context(), key)
	require.NoError(t, err)
	firstBody, err := io.ReadAll(observed.Body)
	require.NoError(t, err)
	require.NoError(t, observed.Body.Close())
	assert.Equal(t, []byte(`{"generation":1}`), firstBody)
	require.False(t, observed.Attributes.Revision.IsZero())
	firstRevision := observed.Attributes.Revision

	err = store.Commit(t.Context(), second, "")
	require.Error(t, err)
	assert.Equal(t, failure.KindPrecondition, failure.KindOf(err))
	wrongRevision, err := object.NewCatalogRevision(`"stale-revision"`)
	require.NoError(t, err)
	err = store.Commit(t.Context(), second, wrongRevision)
	require.Error(t, err)
	assert.Equal(t, failure.KindPrecondition, failure.KindOf(err))

	require.NoError(t, store.Commit(t.Context(), second, firstRevision))
	replaced, err := store.Open(t.Context(), key)
	require.NoError(t, err)
	secondBody, err := io.ReadAll(replaced.Body)
	require.NoError(t, err)
	require.NoError(t, replaced.Body.Close())
	assert.Equal(t, []byte(`{"generation":2}`), secondBody)
	assert.NotEqual(t, firstRevision, replaced.Attributes.Revision)

	err = store.Commit(t.Context(), first, firstRevision)
	require.Error(t, err)
	assert.Equal(t, failure.KindPrecondition, failure.KindOf(err))
}

// realAWSS3Runtime returns bounded production settings for the disposable bucket.
func realAWSS3Runtime(bucket object.BucketName, region string) config.S3 {
	return config.S3{
		Bucket:                bucket,
		Region:                region,
		MaxAttempts:           config.DefaultS3MaxAttempts,
		MaxBackoff:            config.DefaultS3MaxBackoff,
		DialTimeout:           config.DefaultS3DialTimeout,
		TLSHandshakeTimeout:   config.DefaultS3TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.DefaultS3ResponseHeaderTimeout,
	}
}

// realAWSObject constructs one checksum-verified small index body.
func realAWSObject(t testing.TB, key object.ObjectKey, body []byte) publish.CreateObject {
	t.Helper()
	size, err := object.NewByteSize(int64(len(body)))
	require.NoError(t, err)
	return publish.CreateObject{
		Key:         key,
		Size:        size,
		SHA256:      object.DigestBytes(body),
		ContentType: "application/json",
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		},
	}
}

// registerRealAWSCleanup removes the exact conformance object even when an assertion fails.
func registerRealAWSCleanup(t *testing.T, runtime config.S3, key object.ObjectKey) {
	t.Helper()
	configuration, err := awsconfig.LoadDefaultConfig(t.Context(), awsconfig.WithRegion(runtime.Region))
	require.NoError(t, err)
	client := s3.NewFromConfig(configuration)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, cleanupErr := client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(runtime.Bucket.String()),
			Key:    aws.String(key.String()),
		})
		assert.NoError(t, cleanupErr)
	})
}
