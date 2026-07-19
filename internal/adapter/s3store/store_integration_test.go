package s3store

import (
	"encoding/json"
	"hash/crc64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/meigma/simplestreams-s3/internal/adapter/httpserver"
	"github.com/meigma/simplestreams-s3/internal/config"
	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/proxy"
	"github.com/meigma/simplestreams-s3/internal/publish"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

const (
	integrationEnabled             = "SIMPLESTREAMS_S3_INTEGRATION"
	integrationImage               = "minio/minio:RELEASE.2025-04-22T22-12-26Z"
	integrationUser                = "integration-user"
	integrationPassword            = "integration-password"
	integrationBucket              = "private-images"
	integrationCRC64Poly           = 0x9a6c_9329_ac4b_c9b5
	integrationUploadThreshold     = int64(1 << 20)
	integrationExpectedObjectCount = 4
)

// minIOTestContext groups the disposable service and configured adapter.
type minIOTestContext struct {
	store *Store
}

// integrationHTTPResponse captures one observable proxy response.
type integrationHTTPResponse struct {
	status int
	header http.Header
	body   []byte
}

// TestMinIOIntegrationPublishesAndProxiesCatalog proves the full test-only storage contract.
func TestMinIOIntegrationPublishesAndProxiesCatalog(t *testing.T) {
	if os.Getenv(integrationEnabled) != "1" {
		t.Skip("set SIMPLESTREAMS_S3_INTEGRATION=1 to run the containerized S3 integration test")
	}
	testContext := newMinIOTestContext(t)
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), testfixture.DefaultVMOptions())
	publisher := publish.NewService(testContext.store, "")

	result, err := publisher.Publish(t.Context(), publish.Request{
		MetadataPath: metadataPath,
		DiskPath:     diskPath,
	})
	require.NoError(t, err)
	assert.Equal(t, "alpinelinux:3.22:cloud:arm64", result.ProductName.String())
	assert.Equal(t, "202607181302", result.VersionID.String())
	assertPublishedObjectSet(t, testContext)

	server := httptest.NewServer(httpserver.NewHandler(proxy.NewService(testContext.store, "")))
	t.Cleanup(server.Close)
	get := performIntegrationRequest(t, http.MethodGet, server.URL+"/streams/v1/index.json")
	assert.Equal(t, http.StatusOK, get.status)
	assert.Equal(t, "application/json", get.header.Get("Content-Type"))
	assert.True(t, json.Valid(get.body))
	var indexDocument map[string]any
	require.NoError(t, json.Unmarshal(get.body, &indexDocument))
	assert.Equal(t, "index:1.0", indexDocument["format"])

	head := performIntegrationRequest(t, http.MethodHead, server.URL+"/streams/v1/index.json")
	assert.Equal(t, http.StatusOK, head.status)
	assert.Empty(t, head.body)
	assert.Equal(t, get.header.Get("Content-Length"), head.header.Get("Content-Length"))

	missing := performIntegrationRequest(t, http.MethodGet, server.URL+"/streams/v1/missing.json")
	assert.Equal(t, http.StatusNotFound, missing.status)
	assert.JSONEq(t, `{"code":"not_found"}`, string(missing.body))

	unsafe := performIntegrationRequest(t, http.MethodGet, server.URL+"/%2e%2e/streams/v1/index.json")
	assert.Equal(t, http.StatusBadRequest, unsafe.status)
	assert.JSONEq(t, `{"code":"invalid_input"}`, string(unsafe.body))

	_, err = publisher.Publish(t.Context(), publish.Request{
		MetadataPath: metadataPath,
		DiskPath:     diskPath,
	})
	require.Error(t, err)
	assert.Equal(t, failure.KindCatalogConflict, failure.KindOf(err))

	key, err := object.NewObjectKey("integration/create-only.json")
	require.NoError(t, err)
	input := integrationCreateObject(t, key, []byte("create-only"))
	require.NoError(t, testContext.store.Create(t.Context(), input))
	err = testContext.store.Create(t.Context(), input)
	require.Error(t, err)
	assert.Equal(t, failure.KindPrecondition, failure.KindOf(err))
}

// newMinIOTestContext starts MinIO, creates one bucket, and configures the test-only adapter profile.
func newMinIOTestContext(t *testing.T) *minIOTestContext {
	t.Helper()
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
		clientOptions{
			baseEndpoint:    "http://" + endpoint,
			pathStyle:       true,
			uploadThreshold: integrationUploadThreshold,
		},
	)
	require.NoError(t, err)
	_, err = store.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(integrationBucket)})
	require.NoError(t, err)
	return &minIOTestContext{store: store}
}

// assertPublishedObjectSet proves publication made exactly one complete mirror visible.
func assertPublishedObjectSet(t *testing.T, testContext *minIOTestContext) {
	t.Helper()
	output, err := testContext.store.client.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{
		Bucket: aws.String(integrationBucket),
	})
	require.NoError(t, err)
	keys := make([]string, 0, len(output.Contents))
	for _, item := range output.Contents {
		keys = append(keys, aws.ToString(item.Key))
	}
	sort.Strings(keys)
	require.Len(t, keys, integrationExpectedObjectCount)
	assert.Equal(t, "streams/v1/index.json", keys[3])
	assert.True(t, strings.HasPrefix(keys[0], "images/"))
	assert.True(t, strings.HasPrefix(keys[1], "images/"))
	assert.True(t, strings.HasPrefix(keys[2], "streams/v1/images-"))
}

// performIntegrationRequest sends one HTTP request and consumes its response body.
func performIntegrationRequest(t *testing.T, method string, url string) integrationHTTPResponse {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return integrationHTTPResponse{status: response.StatusCode, header: response.Header, body: body}
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
