//go:build integration

package integration_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/meigma/simplestreams-s3/internal/adapter/httpserver"
	"github.com/meigma/simplestreams-s3/internal/adapter/s3store"
	"github.com/meigma/simplestreams-s3/internal/config"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/proxy"
	"github.com/meigma/simplestreams-s3/internal/publish"
)

const (
	minIOImage           = "minio/minio:RELEASE.2025-04-22T22-12-26Z"
	minIOUser            = "integration-user"
	minIOPassword        = "integration-password"
	minIOBucket          = "private-images"
	minIORegion          = "us-east-1"
	minIOUploadThreshold = "1048576"
)

// minIOScenario owns one container and the real collaborators used by integration procedures.
type minIOScenario struct {
	t         *testing.T
	store     *s3store.Store
	s3        *s3.Client
	publisher *publish.Service
	proxyURL  string
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

// objectKeys returns the complete sorted bucket contents for observable state assertions.
func (scenario *minIOScenario) objectKeys() ([]string, error) {
	output, err := scenario.s3.ListObjectsV2(scenario.t.Context(), &s3.ListObjectsV2Input{
		Bucket: aws.String(minIOBucket),
	})
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(output.Contents))
	for _, item := range output.Contents {
		keys = append(keys, aws.ToString(item.Key))
	}
	sort.Strings(keys)
	return keys, nil
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
