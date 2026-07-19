//go:build integration

package integration_test

import (
	"encoding/json"
	"hash/crc64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/publish"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

const (
	minIOCRC64Poly           = 0x9a6c_9329_ac4b_c9b5
	minIOExpectedObjectCount = 4
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
	require.Len(t, keys, minIOExpectedObjectCount)
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
	assert.JSONEq(t, `{"code":"not_found"}`, string(missing.body))

	unsafe := scenario.request(http.MethodGet, "/%2e%2e/streams/v1/index.json")
	assert.Equal(t, http.StatusBadRequest, unsafe.status)
	assert.JSONEq(t, `{"code":"invalid_input"}`, string(unsafe.body))

	_, err = scenario.publisher.Publish(t.Context(), publish.Request{
		MetadataPath: metadataPath,
		DiskPath:     diskPath,
	})
	require.Error(t, err)
	assert.Equal(t, failure.KindCatalogConflict, failure.KindOf(err))

	key, err := object.NewObjectKey("integration/create-only.json")
	require.NoError(t, err)
	input := createObject(t, key, []byte("create-only"))
	require.NoError(t, scenario.store.Create(t.Context(), input))
	err = scenario.store.Create(t.Context(), input)
	require.Error(t, err)
	assert.Equal(t, failure.KindPrecondition, failure.KindOf(err))
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
