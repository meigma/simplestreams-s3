package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/go-simplestreams/schema/incus"
)

// TestBuildCatalogMatchesV1VMContract proves the generated wire model has exactly the designed VM items.
func TestBuildCatalogMatchesV1VMContract(t *testing.T) {
	tempDir := t.TempDir()
	metadataBody := []byte("metadata")
	diskBody := []byte("disk")
	metadataPath := writeFixture(t, tempDir, "incus.tar.xz", metadataBody)
	diskPath := writeFixture(t, tempDir, "disk.qcow2", diskBody)

	built, err := buildCatalog(config{
		metadataPath: metadataPath,
		diskPath:     diskPath,
		outputPath:   filepath.Join(tempDir, "mirror"),
		os:           "alpine",
		release:      "3.22",
		variant:      "cloud",
		architecture: "arm64",
		creationDate: 1_700_000_000,
	})
	require.NoError(t, err)
	require.NoError(t, incus.ValidateRuntimeProductFile(built.productFile))

	assert.Equal(t, "alpine:3.22:cloud:arm64", built.result.Product)
	assert.Equal(t, "alpine/3.22/cloud", built.result.Alias)
	assert.Equal(t, "202311142213", built.result.Version)
	assert.Len(t, built.productFile.Products, 1)

	product := built.productFile.Products[built.result.Product]
	require.NotNil(t, product)
	version := product.Versions[built.result.Version]
	require.NotNil(t, version)
	require.Len(t, version.Items, 2)

	metadataItem := version.Items["incus.tar.xz"]
	require.NotNil(t, metadataItem)
	assert.Equal(t, "incus.tar.xz", metadataItem.FileType)
	assert.Equal(t, "images/"+digest(metadataBody)+".incus.tar.xz", metadataItem.Path.String())
	combined, ok := metadataItem.MetadataValue("combined_disk-kvm-img_sha256")
	require.True(t, ok)
	assert.Equal(t, digest(append(metadataBody, diskBody...)), combined)

	diskItem := version.Items["disk-kvm.img"]
	require.NotNil(t, diskItem)
	assert.Equal(t, "disk-kvm.img", diskItem.FileType)
	assert.Equal(t, "images/"+digest(diskBody)+".qcow2", diskItem.Path.String())
	assert.Contains(t, built.result.ProductPath, "streams/v1/images-")
	assert.NotEmpty(t, built.indexBody)
}

// writeFixture writes one test artifact and returns its path.
func writeFixture(t *testing.T, root string, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(path, body, 0o600))
	return path
}

// digest returns the lowercase SHA-256 digest of body.
func digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
