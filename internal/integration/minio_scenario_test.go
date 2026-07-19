//go:build integration

package integration_test

import (
	"encoding/json"
	"hash/crc64"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/meigma/simplestreams-s3/internal/adapter/httpserver"
	"github.com/meigma/simplestreams-s3/internal/adapter/s3store"
	"github.com/meigma/simplestreams-s3/internal/config"
	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/proxy"
	"github.com/meigma/simplestreams-s3/internal/publish"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

const (
	minIOImage                   = "minio/minio:RELEASE.2025-04-22T22-12-26Z"
	minIOUser                    = "integration-user"
	minIOPassword                = "integration-password"
	minIOBucket                  = "private-images"
	minIORegion                  = "us-east-1"
	minIOCRC64Poly               = 0x9a6c_9329_ac4b_c9b5
	minIOUploadThreshold         = "1048576"
	minIOExpectedObjectCount     = 4
)

// minIOScenario owns one container and all collaborators for a complete integration procedure.
type minIOScenario struct {
	t         *testing.T
	store     *s3store.Store
	s3        *s3.Client
	publisher *publish.Service
	proxyURL  string
}

// publishedFixture retains the input paths needed to exercise repeat publication.
type publishedFixture struct {
	metadataPath string
	diskPath     string
}

// scenarioHTTPResponse captures one observable proxy response.
type scenarioHTTPResponse struct {
	status int
	header http.Header
	body   []byte
}

// newMinIOScenario starts one disposable MinIO instance and composes the real application adapters.
func newMinIOScenario(t *testing.T) *minIOScenario {
	t.Helper()
	ctx := t.Context()
	container, err := tcminio.Run(
		ctx,
		minIOImage,
		tcminio.WithUsername(minIOUser),
		tcminio.WithPassword(minIOPassword),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)
	endpoint, err := container.ConnectionString(ctx)
	require.NoError(t, err)
	endpoint = "http://" + endpoint

	t.Setenv("AWS_ACCESS_KEY_ID", minIOUser)
	t.Setenv("AWS_SECRET_ACCESS_KEY", minIOPassword)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("SIMPLESTREAMS_S3_TEST_S3_ENDPOINT", endpoint)
	t.Setenv("SIMPLESTREAMS_S3_TEST_S3_PATH_STYLE", "true")
	t.Setenv("SIMPLESTREAMS_S3_TEST_UPLOAD_THRESHOLD_BYTES", minIOUploadThreshold)

	client := newMinIOClient(t, endpoint)
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(minIOBucket)})
	require.NoError(t, err)
	store, err := s3store.New(ctx, minIOS3Runtime(t), config.DefaultCatalogTimeout)
	require.NoError(t, err)
	publisher := publish.NewService(store, "")
	server := httptest.NewServer(httpserver.NewHandler(proxy.NewService(store, "")))
	t.Cleanup(server.Close)

	return &minIOScenario{
		t:         t,
		store:     store,
		s3:        client,
		publisher: publisher,
		proxyURL:  server.URL,
	}
}

// newMinIOClient constructs the scenario's bucket-administration client.
func newMinIOClient(t *testing.T, endpoint string) *s3.Client {
	t.Helper()
	configuration, err := awsconfig.LoadDefaultConfig(
		t.Context(),
		awsconfig.WithRegion(minIORegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			minIOUser,
			minIOPassword,
			"",
		)),
	)
	require.NoError(t, err)
	return s3.NewFromConfig(configuration, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
}

// minIOS3Runtime returns the production adapter settings used by the scenario.
func minIOS3Runtime(t *testing.T) config.S3 {
	t.Helper()
	bucket, err := object.NewBucketName(minIOBucket)
	require.NoError(t, err)
	return config.S3{
		Bucket:                bucket,
		Region:                minIORegion,
		MaxAttempts:           config.DefaultS3MaxAttempts,
		MaxBackoff:            config.DefaultS3MaxBackoff,
		DialTimeout:           config.DefaultS3DialTimeout,
		TLSHandshakeTimeout:   config.DefaultS3TLSHandshakeTimeout,
		ResponseHeaderTimeout: config.DefaultS3ResponseHeaderTimeout,
	}
}

// publishValidVM generates and publishes one complete split-VM fixture.
func (scenario *minIOScenario) publishValidVM() publishedFixture {
	scenario.t.Helper()
	metadataPath, diskPath := testfixture.WriteSplitVM(
		scenario.t,
		scenario.t.TempDir(),
		testfixture.DefaultVMOptions(),
	)
	result, err := scenario.publisher.Publish(scenario.t.Context(), publish.Request{
		MetadataPath: metadataPath,
		DiskPath:     diskPath,
	})
	require.NoError(scenario.t, err)
	assert.Equal(scenario.t, "alpinelinux:3.22:cloud:arm64", result.ProductName.String())
	assert.Equal(scenario.t, "202607181302", result.VersionID.String())
	return publishedFixture{metadataPath: metadataPath, diskPath: diskPath}
}

// requireCompleteMirror proves publication made exactly one complete mirror visible.
func (scenario *minIOScenario) requireCompleteMirror() {
	scenario.t.Helper()
	output, err := scenario.s3.ListObjectsV2(scenario.t.Context(), &s3.ListObjectsV2Input{
		Bucket: aws.String(minIOBucket),
	})
	require.NoError(scenario.t, err)
	keys := make([]string, 0, len(output.Contents))
	for _, item := range output.Contents {
		keys = append(keys, aws.ToString(item.Key))
	}
	sort.Strings(keys)
	require.Len(scenario.t, keys, minIOExpectedObjectCount)
	assert.Equal(scenario.t, "streams/v1/index.json", keys[3])
	assert.True(scenario.t, strings.HasPrefix(keys[0], "images/"))
	assert.True(scenario.t, strings.HasPrefix(keys[1], "images/"))
	assert.True(scenario.t, strings.HasPrefix(keys[2], "streams/v1/images-"))
}

// requireProxyContract proves exact reads and safe failure mapping through the HTTP adapter.
func (scenario *minIOScenario) requireProxyContract() {
	scenario.t.Helper()
	get := scenario.request(http.MethodGet, "/streams/v1/index.json")
	assert.Equal(scenario.t, http.StatusOK, get.status)
	assert.Equal(scenario.t, "application/json", get.header.Get("Content-Type"))
	var indexDocument map[string]any
	require.NoError(scenario.t, json.Unmarshal(get.body, &indexDocument))
	assert.Equal(scenario.t, "index:1.0", indexDocument["format"])

	head := scenario.request(http.MethodHead, "/streams/v1/index.json")
	assert.Equal(scenario.t, http.StatusOK, head.status)
	assert.Empty(scenario.t, head.body)
	assert.Equal(scenario.t, get.header.Get("Content-Length"), head.header.Get("Content-Length"))

	missing := scenario.request(http.MethodGet, "/streams/v1/missing.json")
	assert.Equal(scenario.t, http.StatusNotFound, missing.status)
	assert.JSONEq(scenario.t, `{"code":"not_found"}`, string(missing.body))

	unsafe := scenario.request(http.MethodGet, "/%2e%2e/streams/v1/index.json")
	assert.Equal(scenario.t, http.StatusBadRequest, unsafe.status)
	assert.JSONEq(scenario.t, `{"code":"invalid_input"}`, string(unsafe.body))
}

// requirePhaseTwoRefusal proves the process will not mutate an existing catalog.
func (scenario *minIOScenario) requirePhaseTwoRefusal(fixture publishedFixture) {
	scenario.t.Helper()
	_, err := scenario.publisher.Publish(scenario.t.Context(), publish.Request{
		MetadataPath: fixture.metadataPath,
		DiskPath:     fixture.diskPath,
	})
	require.Error(scenario.t, err)
	assert.Equal(scenario.t, failure.KindCatalogConflict, failure.KindOf(err))
}

// requireCreateOnlyCollision proves MinIO enforces the adapter's immutable-write condition.
func (scenario *minIOScenario) requireCreateOnlyCollision() {
	scenario.t.Helper()
	key, err := object.NewObjectKey("integration/create-only.json")
	require.NoError(scenario.t, err)
	input := createObject(scenario.t, key, []byte("create-only"))
	require.NoError(scenario.t, scenario.store.Create(scenario.t.Context(), input))
	err = scenario.store.Create(scenario.t.Context(), input)
	require.Error(scenario.t, err)
	assert.Equal(scenario.t, failure.KindPrecondition, failure.KindOf(err))
}

// request sends one HTTP request to the scenario proxy and consumes its response body.
func (scenario *minIOScenario) request(method string, path string) scenarioHTTPResponse {
	scenario.t.Helper()
	request, err := http.NewRequestWithContext(scenario.t.Context(), method, scenario.proxyURL+path, nil)
	require.NoError(scenario.t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(scenario.t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(scenario.t, err)
	return scenarioHTTPResponse{status: response.StatusCode, header: response.Header, body: body}
}

// createObject builds one checksum-verified create-only adapter input.
func createObject(t *testing.T, key object.ObjectKey, body []byte) publish.CreateObject {
	t.Helper()
	size, err := object.NewByteSize(int64(len(body)))
	require.NoError(t, err)
	hasher := crc64.New(crc64.MakeTable(minIOCRC64Poly))
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
