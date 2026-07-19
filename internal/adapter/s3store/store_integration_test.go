package s3store

import (
	"context"
	"hash/crc64"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/meigma/simplestreams-s3/internal/config"
	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/publish"
)

const (
	integrationEnabled   = "SIMPLESTREAMS_S3_INTEGRATION"
	integrationImage     = "minio/minio:RELEASE.2025-04-22T22-12-26Z"
	integrationUser      = "integration-user"
	integrationPassword  = "integration-password"
	integrationBucket    = "private-images"
	integrationCRC64Poly = 0x9a6c_9329_ac4b_c9b5
)

// TestStoreIntegrationExercisesCreateHeadAndGet proves the AWS adapter against a disposable S3-compatible service.
func TestStoreIntegrationExercisesCreateHeadAndGet(t *testing.T) {
	if os.Getenv(integrationEnabled) != "1" {
		t.Skip("set SIMPLESTREAMS_S3_INTEGRATION=1 to run the containerized S3 adapter test")
	}
	ctx := t.Context()
	container, err := tcminio.Run(
		ctx,
		integrationImage,
		tcminio.WithUsername(integrationUser),
		tcminio.WithPassword(integrationPassword),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)
	endpoint, err := container.ConnectionString(ctx)
	require.NoError(t, err)
	t.Setenv("AWS_ACCESS_KEY_ID", integrationUser)
	t.Setenv("AWS_SECRET_ACCESS_KEY", integrationPassword)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	store, err := newStore(
		ctx,
		integrationRuntime(t),
		config.DefaultCatalogTimeout,
		clientOptions{baseEndpoint: "http://" + endpoint, pathStyle: true},
	)
	require.NoError(t, err)
	_, err = store.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(integrationBucket)})
	require.NoError(t, err)

	key, err := object.NewObjectKey("streams/v1/index.json")
	require.NoError(t, err)
	body := []byte("catalog")
	input := integrationCreateObject(t, key, body)
	exists, err := store.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)
	require.NoError(t, store.Create(ctx, input))

	exists, err = store.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)
	attributes, err := store.Head(ctx, key)
	require.NoError(t, err)
	assert.EqualValues(t, len(body), attributes.Size.Int64())
	result, err := store.Get(ctx, key)
	require.NoError(t, err)
	read, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	require.NoError(t, result.Body.Close())
	assert.Equal(t, body, read)

	err = store.Create(context.Background(), input)
	require.Error(t, err)
	assert.Equal(t, failure.KindPrecondition, failure.KindOf(err))
}

// integrationRuntime constructs one validated local endpoint configuration.
func integrationRuntime(t testing.TB) config.S3 {
	t.Helper()
	bucket, err := object.NewBucketName(integrationBucket)
	require.NoError(t, err)
	return config.S3{
		Bucket:                bucket,
		Region:                "us-east-1",
		MaxAttempts:           config.DefaultS3MaxAttempts,
		MaxBackoff:            config.DefaultS3MaxBackoff,
		DialTimeout:           config.DefaultS3DialTimeout,
		TLSHandshakeTimeout:   config.DefaultS3TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.DefaultS3ResponseHeaderTimeout,
	}
}

// integrationCreateObject builds one checksum-verified create-only adapter input.
func integrationCreateObject(t testing.TB, key object.ObjectKey, body []byte) publish.CreateObject {
	t.Helper()
	size, err := object.NewByteSize(int64(len(body)))
	require.NoError(t, err)
	hasher := crc64.New(crc64.MakeTable(integrationCRC64Poly))
	_, err = hasher.Write(body)
	require.NoError(t, err)
	checksum := object.NewCRC64NVME(hasher.Sum64())
	return publish.CreateObject{
		Key:         key,
		Size:        size,
		SHA256:      object.DigestBytes(body),
		CRC64NVME:   &checksum,
		ContentType: "application/json",
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(body))), nil
		},
	}
}
