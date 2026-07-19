package catalog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/image"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

// TestRenderCreatesDeterministicTwoItemIncusCatalog proves the Phase 2 wire projection.
func TestRenderCreatesDeterministicTwoItemIncusCatalog(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), testfixture.DefaultVMOptions())
	vm, err := image.Inspect(metadataPath, diskPath)
	require.NoError(t, err)
	alias, err := NewAlias("alpine/latest/cloud")
	require.NoError(t, err)
	title, err := NewReleaseTitle("Alpine 3.22")
	require.NoError(t, err)
	updated := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	documents, err := Render(vm, []Alias{alias}, title, updated)
	require.NoError(t, err)
	repeated, err := Render(vm, []Alias{alias}, title, updated)
	require.NoError(t, err)
	assert.Equal(t, documents.Snapshot(), repeated.Snapshot())
	assert.Equal(t, documents.Index(), repeated.Index())
	assert.Equal(t, "alpinelinux:3.22:cloud:arm64", documents.ProductName().String())
	assert.Equal(t, "202607181302", documents.VersionID().String())
	assert.True(t, strings.HasPrefix(documents.SnapshotKey().String(), "streams/v1/images-"))
	assert.Equal(t, "streams/v1/index.json", documents.IndexKey().String())

	var productDocument map[string]any
	require.NoError(t, json.Unmarshal(documents.Snapshot(), &productDocument))
	products := requireMap(t, productDocument, "products")
	product := requireMap(t, products, documents.ProductName().String())
	assert.Equal(t, "alpine/latest/cloud,alpinelinux/3.22/cloud", product["aliases"])
	version := requireMap(t, requireMap(t, product, "versions"), documents.VersionID().String())
	items := requireMap(t, version, "items")
	assert.Len(t, items, 2)
	metadataItem := requireMap(t, items, "incus.tar.xz")
	diskItem := requireMap(t, items, "disk-kvm.img")
	assert.Equal(t, "incus.tar.xz", metadataItem["ftype"])
	assert.Equal(t, "disk-kvm.img", diskItem["ftype"])
	assert.NotEmpty(t, metadataItem["combined_disk-kvm-img_sha256"])
	assert.True(t, strings.HasPrefix(metadataItem["path"].(string), "images/"))
	assert.True(t, strings.HasSuffix(diskItem["path"].(string), ".qcow2"))

	var indexDocument map[string]any
	require.NoError(t, json.Unmarshal(documents.Index(), &indexDocument))
	imagesEntry := requireMap(t, requireMap(t, indexDocument, "index"), "images")
	assert.Equal(t, documents.SnapshotKey().String(), imagesEntry["path"])
}

// TestNewAliasRejectsUnsafeValues proves aliases are rejected rather than normalized.
func TestNewAliasRejectsUnsafeValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "/alpine", "alpine/", "alpine//cloud", "alpine/../cloud", `alpine\cloud`} {
		_, err := NewAlias(value)
		require.Error(t, err, value)
	}
}

// requireMap extracts a JSON object value or fails the test at the caller.
func requireMap(t testing.TB, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, exists := parent[key]
	require.True(t, exists, "expected key %q", key)
	result, ok := value.(map[string]any)
	require.True(t, ok, "expected %q to contain an object", key)
	return result
}
