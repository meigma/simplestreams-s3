//go:build integration

package integration_test

import (
	"encoding/json"
	"hash/crc64"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/evidence"
	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/publish"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

// TestPublishEvidenceAndProxyProcess proves generic clients can discover and verify every proof.
func TestPublishEvidenceAndProxyProcess(t *testing.T) {
	scenario := newMinIOScenario(t)
	directory := t.TempDir()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, directory, testfixture.DefaultVMOptions())
	manifestPath := testfixture.WriteEvidenceManifest(
		t,
		filepath.Join(directory, "evidence"),
		metadataPath,
		diskPath,
		"pass",
	)

	result, err := scenario.publisher.Publish(t.Context(), publish.Request{
		MetadataPath: metadataPath, DiskPath: diskPath, EvidenceManifestPath: manifestPath,
	})
	require.NoError(t, err)
	indexResponse := scenario.request(http.MethodGet, "/streams/v1/index.json")
	require.Equal(t, http.StatusOK, indexResponse.status)
	var index map[string]any
	require.NoError(t, json.Unmarshal(indexResponse.body, &index))
	productPath := index["index"].(map[string]any)["images"].(map[string]any)["path"].(string)
	productResponse := scenario.request(http.MethodGet, "/"+productPath)
	require.Equal(t, http.StatusOK, productResponse.status)
	var products map[string]any
	require.NoError(t, json.Unmarshal(productResponse.body, &products))
	product := products["products"].(map[string]any)[result.ProductName.String()].(map[string]any)
	version := product["versions"].(map[string]any)[result.VersionID.String()].(map[string]any)
	manifestItem := version["items"].(map[string]any)[evidence.ManifestItemName].(map[string]any)
	manifestMirrorPath := manifestItem["path"].(string)

	manifestResponse := scenario.request(http.MethodGet, "/"+manifestMirrorPath)
	require.Equal(t, http.StatusOK, manifestResponse.status)
	assert.Equal(t, evidence.ManifestMediaType, manifestResponse.header.Get("Content-Type"))
	assert.Equal(t, manifestItem["sha256"], object.DigestBytes(manifestResponse.body).String())
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(manifestResponse.body, &manifest))
	for _, value := range manifest["evidence"].([]any) {
		entry := value.(map[string]any)
		proof := scenario.request(http.MethodGet, "/"+entry["path"].(string))
		require.Equal(t, http.StatusOK, proof.status)
		assert.Equal(t, entry["mediaType"], proof.header.Get("Content-Type"))
		assert.Equal(t, entry["sha256"], object.DigestBytes(proof.body).String())
	}
}

const (
	minIOCRC64Poly                  = 0x9a6c_9329_ac4b_c9b5
	minIOInitialObjectCount         = 4
	minIOExistingCatalogObjectCount = 6
)

// TestPublishAndProxyProcess proves the complete Phase 2 procedure against one disposable MinIO instance.
func TestPublishAndProxyProcess(t *testing.T) {
	scenario := newMinIOScenario(t)
	metadataPath, diskPath := testfixture.WriteSplitVM(
		t,
		t.TempDir(),
		testfixture.DefaultVMOptions(),
	)

	result, err := scenario.publisher.Publish(t.Context(), publish.Request{
		MetadataPath: metadataPath,
		DiskPath:     diskPath,
	})
	require.NoError(t, err)
	assert.Equal(t, "alpinelinux:3.22:cloud:arm64", result.ProductName.String())
	assert.Equal(t, "202607181302", result.VersionID.String())

	keys, err := scenario.objectKeys()
	require.NoError(t, err)
	require.Len(t, keys, minIOInitialObjectCount)
	assert.Equal(t, "streams/v1/index.json", keys[3])
	assert.True(t, strings.HasPrefix(keys[0], "images/"))
	assert.True(t, strings.HasPrefix(keys[1], "images/"))
	assert.True(t, strings.HasPrefix(keys[2], "streams/v1/images-"))

	get := scenario.request(http.MethodGet, "/streams/v1/index.json")
	assert.Equal(t, http.StatusOK, get.status)
	assert.Equal(t, "application/json", get.header.Get("Content-Type"))
	var indexDocument map[string]any
	require.NoError(t, json.Unmarshal(get.body, &indexDocument))
	assert.Equal(t, "index:1.0", indexDocument["format"])

	head := scenario.request(http.MethodHead, "/streams/v1/index.json")
	assert.Equal(t, http.StatusOK, head.status)
	assert.Empty(t, head.body)
	assert.Equal(t, get.header.Get("Content-Length"), head.header.Get("Content-Length"))

	missing := scenario.request(http.MethodGet, "/streams/v1/missing.json")
	assert.Equal(t, http.StatusNotFound, missing.status)
	assertErrorCode(t, missing.body, "not_found")

	unsafe := scenario.request(http.MethodGet, "/%2e%2e/streams/v1/index.json")
	assert.Equal(t, http.StatusBadRequest, unsafe.status)
	assertErrorCode(t, unsafe.body, "invalid_input")

	_, err = scenario.publisher.Publish(t.Context(), publish.Request{
		MetadataPath: metadataPath,
		DiskPath:     diskPath,
	})
	require.NoError(t, err)
	repeatedKeys, err := scenario.objectKeys()
	require.NoError(t, err)
	assert.Equal(t, keys, repeatedKeys)

	secondOptions := testfixture.DefaultVMOptions()
	secondOptions.CreationDate += 3600
	secondMetadata, secondDisk := testfixture.WriteSplitVM(t, t.TempDir(), secondOptions)
	secondResult, err := scenario.publisher.Publish(t.Context(), publish.Request{
		MetadataPath: secondMetadata,
		DiskPath:     secondDisk,
	})
	require.NoError(t, err)
	assert.Equal(t, "202607181402", secondResult.VersionID.String())
	updatedKeys, err := scenario.objectKeys()
	require.NoError(t, err)
	assert.Len(t, updatedKeys, minIOExistingCatalogObjectCount)

	key, err := object.NewObjectKey("integration/create-only.json")
	require.NoError(t, err)
	input := createObject(t, key, []byte("create-only"))
	require.NoError(t, scenario.store.Create(t.Context(), input))
	err = scenario.store.Create(t.Context(), input)
	require.Error(t, err)
	assert.Equal(t, failure.KindPrecondition, failure.KindOf(err))
}

// assertErrorCode proves a sanitized proxy error includes a stable code and correlation ID.
func assertErrorCode(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var response struct {
		Code      string `json:"code"`
		RequestID string `json:"request_id"`
	}
	require.NoError(t, json.Unmarshal(body, &response))
	assert.Equal(t, wantCode, response.Code)
	assert.NotEmpty(t, response.RequestID)
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
