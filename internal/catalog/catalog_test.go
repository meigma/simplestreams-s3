package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	simplestreams "github.com/meigma/go-simplestreams"
	incusschema "github.com/meigma/go-simplestreams/schema/incus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/image"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

// memorySource opens one immutable set of mirror-relative test documents.
type memorySource map[string][]byte

// Open implements simplestreams.Source for catalog adoption tests.
func (source memorySource) Open(_ context.Context, path simplestreams.RelativePath) (io.ReadCloser, error) {
	body, exists := source[path.String()]
	if !exists {
		return nil, simplestreams.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

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

// TestMergeAdoptsCurrentCatalogWithoutLosingState proves no-op and additive publication behavior.
func TestMergeAdoptsCurrentCatalogWithoutLosingState(t *testing.T) {
	t.Parallel()
	firstVM := inspectFixtureVM(t, testfixture.DefaultVMOptions())
	title, err := NewReleaseTitle("Alpine 3.22")
	require.NoError(t, err)
	firstUpdated := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	first, err := Render(firstVM, nil, title, firstUpdated)
	require.NoError(t, err)
	current := decodeCurrent(t, withPreservedIndexState(t, first))

	repeated, err := Merge(current, firstVM, nil, title, firstUpdated.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, repeated.Changed())
	assert.Empty(t, repeated.Snapshot())
	assert.Empty(t, repeated.Index())

	secondOptions := testfixture.DefaultVMOptions()
	secondOptions.CreationDate += int64(time.Hour / time.Second)
	secondVM := inspectFixtureVM(t, secondOptions)
	second, err := Merge(current, secondVM, nil, title, firstUpdated.Add(2*time.Minute))
	require.NoError(t, err)
	assert.True(t, second.Changed())

	var productDocument map[string]any
	require.NoError(t, json.Unmarshal(second.Snapshot(), &productDocument))
	products := requireMap(t, productDocument, "products")
	versions := requireMap(t, requireMap(t, products, second.ProductName().String()), "versions")
	assert.Len(t, versions, 2)
	var indexDocument map[string]any
	require.NoError(t, json.Unmarshal(second.Index(), &indexDocument))
	assert.Equal(t, "preserved", indexDocument["custom_index_field"])
	entries := requireMap(t, indexDocument, "index")
	assert.Contains(t, entries, "other")
	imagesEntry := requireMap(t, entries, incusschema.ContentIDImages)
	assert.Equal(t, "preserved", imagesEntry["custom_entry_field"])
}

// TestMergeRejectsIncompatibleAdoptedState proves conflicts never become replacement generations.
func TestMergeRejectsIncompatibleAdoptedState(t *testing.T) {
	t.Parallel()
	vm := inspectFixtureVM(t, testfixture.DefaultVMOptions())
	title, err := NewReleaseTitle("Alpine 3.22")
	require.NoError(t, err)
	updated := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	first, err := Render(vm, nil, title, updated)
	require.NoError(t, err)

	tests := []struct {
		name       string
		mutate     func(*Current)
		aliases    []Alias
		mergeTitle ReleaseTitle
	}{
		{
			name: "artifact path does not match its digest",
			mutate: func(current *Current) {
				product := current.ProductFile.Products[first.ProductName().String()]
				version := product.Versions[first.VersionID().String()]
				version.Items[artifactDiskName].Path = simplestreams.RelativePath("images/wrong.qcow2")
			},
			mergeTitle: title,
		},
		{
			name: "catalog timestamp is malformed",
			mutate: func(current *Current) {
				current.Index.Updated = "not-a-timestamp"
			},
			mergeTitle: title,
		},
		{
			name:       "alias mutation changes product metadata",
			mutate:     func(_ *Current) {},
			aliases:    []Alias{mustAlias(t, "alpine/latest/cloud")},
			mergeTitle: title,
		},
		{
			name:       "release title mutation changes product metadata",
			mutate:     func(_ *Current) {},
			mergeTitle: mustReleaseTitle(t, "Different title"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := decodeCurrent(t, documentsSource(first))
			test.mutate(&current)

			_, mergeErr := Merge(current, vm, test.aliases, test.mergeTitle, updated.Add(time.Minute))
			require.Error(t, mergeErr)
			assert.Equal(t, failure.KindCatalogConflict, failure.KindOf(mergeErr))
		})
	}
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

// inspectFixtureVM writes and inspects one split-VM fixture.
func inspectFixtureVM(t testing.TB, options testfixture.VMOptions) image.VMImage {
	t.Helper()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), options)
	vm, err := image.Inspect(metadataPath, diskPath)
	require.NoError(t, err)
	return vm
}

// documentsSource exposes one rendered generation through a Simple Streams source.
func documentsSource(documents Documents) memorySource {
	return memorySource{
		documents.IndexKey().String():    documents.Index(),
		documents.SnapshotKey().String(): documents.Snapshot(),
	}
}

// withPreservedIndexState adds unrelated admitted metadata and an index entry.
func withPreservedIndexState(t testing.TB, documents Documents) memorySource {
	t.Helper()
	var index map[string]any
	require.NoError(t, json.Unmarshal(documents.Index(), &index))
	index["custom_index_field"] = "preserved"
	entries := requireMap(t, index, "index")
	imagesEntry := requireMap(t, entries, incusschema.ContentIDImages)
	imagesEntry["custom_entry_field"] = "preserved"
	entries["other"] = map[string]any{
		"datatype": "other-downloads",
		"format":   simplestreams.ProductsFormat,
		"path":     "streams/v1/other.json",
		"products": []string{"other:product"},
	}
	body, err := json.Marshal(index)
	require.NoError(t, err)
	source := documentsSource(documents)
	source[documents.IndexKey().String()] = body
	return source
}

// decodeCurrent loads one catalog generation through a fresh Mirror.
func decodeCurrent(t testing.TB, source memorySource) Current {
	t.Helper()
	mirror, err := simplestreams.NewMirror(source)
	require.NoError(t, err)
	index, err := mirror.Index(context.Background())
	require.NoError(t, err)
	entry := index.Entries[incusschema.ContentIDImages]
	require.NotNil(t, entry)
	productFile, err := entry.ProductFile(context.Background())
	require.NoError(t, err)
	return Current{Index: index, ProductFile: productFile}
}

// mustAlias constructs one test alias or fails immediately.
func mustAlias(t testing.TB, value string) Alias {
	t.Helper()
	alias, err := NewAlias(value)
	require.NoError(t, err)
	return alias
}

// mustReleaseTitle constructs one test release title or fails immediately.
func mustReleaseTitle(t testing.TB, value string) ReleaseTitle {
	t.Helper()
	title, err := NewReleaseTitle(value)
	require.NoError(t, err)
	return title
}
