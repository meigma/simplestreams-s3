package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

const testFileMode = 0o600

// fakeObject stores one exact in-memory object generation.
type fakeObject struct {
	body       []byte
	attributes ObjectAttributes
}

// fakeStore records revision-aware publication behavior in memory.
type fakeStore struct {
	objects       map[string]fakeObject
	openCalls     int
	created       []CreateObject
	committed     []CreateObject
	writes        []string
	commitCalls   int
	beforeCreate  func(int, CreateObject)
	createFailure func(int, CreateObject) error
	beforeCommit  func(*fakeStore, int, CreateObject, object.CatalogRevision)
	afterCommit   func(int) error
}

// Open reads one exact object generation or reports a typed miss.
func (store *fakeStore) Open(_ context.Context, key object.ObjectKey) (ReadObject, error) {
	store.openCalls++
	stored, exists := store.objects[key.String()]
	if !exists {
		return ReadObject{}, failure.New(failure.KindNotFound, "open object", "object is missing")
	}
	return ReadObject{
		Body:       io.NopCloser(bytes.NewReader(stored.body)),
		Attributes: stored.attributes,
	}, nil
}

// Stat returns the verification attributes for one exact object.
func (store *fakeStore) Stat(_ context.Context, key object.ObjectKey) (ObjectAttributes, error) {
	stored, exists := store.objects[key.String()]
	if !exists {
		return ObjectAttributes{}, failure.New(failure.KindNotFound, "stat object", "object is missing")
	}
	return stored.attributes, nil
}

// Create consumes one create-only upload and records its trusted attributes.
func (store *fakeStore) Create(_ context.Context, input CreateObject) error {
	if store.beforeCreate != nil {
		store.beforeCreate(len(store.created), input)
	}
	if store.createFailure != nil {
		if err := store.createFailure(len(store.created), input); err != nil {
			return err
		}
	}
	store.initialize()
	if _, exists := store.objects[input.Key.String()]; exists {
		return failure.New(failure.KindPrecondition, "create object", "object already exists")
	}
	data, err := consumeCreateObject(input)
	if err != nil {
		return err
	}
	store.created = append(store.created, input)
	store.writes = append(store.writes, input.Key.String())
	store.objects[input.Key.String()] = fakeObject{
		body:       data,
		attributes: fakeAttributes(input, fakeRevision(data)),
	}
	return nil
}

// Commit applies the absent-or-matches precondition to the mutable root index.
func (store *fakeStore) Commit(
	_ context.Context,
	input CreateObject,
	revision object.CatalogRevision,
) error {
	store.initialize()
	store.commitCalls++
	call := store.commitCalls
	if store.beforeCommit != nil {
		store.beforeCommit(store, call, input, revision)
	}
	current, exists := store.objects[input.Key.String()]
	if revision.IsZero() && exists {
		return failure.New(failure.KindPrecondition, "commit index", "index already exists")
	}
	if !revision.IsZero() && (!exists || current.attributes.Revision != revision) {
		return failure.New(failure.KindPrecondition, "commit index", "index revision changed")
	}
	data, err := consumeCreateObject(input)
	if err != nil {
		return err
	}
	store.committed = append(store.committed, input)
	store.writes = append(store.writes, input.Key.String())
	store.objects[input.Key.String()] = fakeObject{
		body:       data,
		attributes: fakeAttributes(input, fakeRevision(data)),
	}
	if store.afterCommit != nil {
		return store.afterCommit(call)
	}
	return nil
}

// initialize lazily creates the fake object namespace.
func (store *fakeStore) initialize() {
	if store.objects == nil {
		store.objects = map[string]fakeObject{}
	}
}

// consumeCreateObject reads and closes one complete write body.
func consumeCreateObject(input CreateObject) ([]byte, error) {
	body, err := input.Open()
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return data, nil
}

// fakeAttributes records exactly the checksum classes expected for input.
func fakeAttributes(input CreateObject, revision object.CatalogRevision) ObjectAttributes {
	metadataDigest := input.SHA256
	attributes := ObjectAttributes{
		Size:           input.Size,
		MetadataSHA256: &metadataDigest,
		Revision:       revision,
	}
	if input.CRC64NVME == nil {
		checksum := input.SHA256
		attributes.ChecksumSHA256 = &checksum
	} else {
		checksum := *input.CRC64NVME
		attributes.ChecksumCRC64NVME = &checksum
	}
	return attributes
}

// fakeRevision derives one stable opaque revision from object bytes.
func fakeRevision(body []byte) object.CatalogRevision {
	revision, err := object.NewCatalogRevision(`"` + object.DigestBytes(body).String() + `"`)
	if err != nil {
		panic(err)
	}
	return revision
}

// newTestService constructs the bounded publisher used by unit tests.
func newTestService(store Store, prefix object.KeyPrefix) *Service {
	return NewService(store, prefix, Options{CatalogAttempts: 4, CatalogTimeout: time.Second})
}

// TestPublishDetectsInputMutationDuringUpload proves stale object identities never reach the catalog commit.
func TestPublishDetectsInputMutationDuringUpload(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), testfixture.DefaultVMOptions())
	store := &fakeStore{}
	store.beforeCreate = func(index int, _ CreateObject) {
		if index == 0 {
			require.NoError(t, os.WriteFile(metadataPath, []byte("changed-after-validation"), testFileMode))
		}
	}
	service := newTestService(store, "")

	_, err := service.Publish(context.Background(), Request{MetadataPath: metadataPath, DiskPath: diskPath})
	require.Error(t, err)
	assert.Equal(t, failure.KindIntegrity, failure.KindOf(err))
	assert.Empty(t, store.created)
}

// TestPublishMakesIndexVisibleOnlyAfterEveryReferencedObject proves publication ordering.
func TestPublishMakesIndexVisibleOnlyAfterEveryReferencedObject(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), testfixture.DefaultVMOptions())
	store := &fakeStore{}
	prefix, err := object.ParseKeyPrefix("private/incus")
	require.NoError(t, err)
	service := newTestService(store, prefix)
	service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

	result, err := service.Publish(context.Background(), Request{
		MetadataPath: metadataPath,
		DiskPath:     diskPath,
	})
	require.NoError(t, err)
	assert.Equal(t, "alpinelinux:3.22:cloud:arm64", result.ProductName.String())
	require.Len(t, store.writes, 4)
	assert.True(t, strings.HasSuffix(store.writes[0], ".incus.tar.xz"))
	assert.True(t, strings.HasSuffix(store.writes[1], ".qcow2"))
	assert.Contains(t, store.writes[2], "/streams/v1/images-")
	assert.Equal(t, "private/incus/streams/v1/index.json", store.writes[3])
	for _, created := range append(store.created, store.committed...) {
		body := store.objects[created.Key.String()].body
		assert.Len(t, body, int(created.Size.Int64()))
		assert.Equal(t, created.SHA256, object.DigestBytes(body))
	}
}

// TestPublishFinishesLocalValidationBeforeContactingStorage proves invalid input causes no mutation or probe.
func TestPublishFinishesLocalValidationBeforeContactingStorage(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), testfixture.DefaultVMOptions())
	require.NoError(t, os.WriteFile(diskPath, []byte("invalid"), testFileMode))
	store := &fakeStore{}
	service := newTestService(store, "")

	_, err := service.Publish(context.Background(), Request{MetadataPath: metadataPath, DiskPath: diskPath})
	require.Error(t, err)
	assert.Equal(t, failure.KindUnsupportedImage, failure.KindOf(err))
	assert.Zero(t, store.openCalls)
	assert.Empty(t, store.created)
}

// TestPublishRepeatedImageIsIdempotent proves immutable repair checks avoid an index rewrite.
func TestPublishRepeatedImageIsIdempotent(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), testfixture.DefaultVMOptions())
	store := &fakeStore{}
	service := newTestService(store, "")
	service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

	_, err := service.Publish(context.Background(), Request{MetadataPath: metadataPath, DiskPath: diskPath})
	require.NoError(t, err)
	indexBefore := append([]byte(nil), store.objects["streams/v1/index.json"].body...)
	writesBefore := len(store.writes)

	_, err = service.Publish(context.Background(), Request{MetadataPath: metadataPath, DiskPath: diskPath})
	require.NoError(t, err)
	assert.Len(t, store.writes, writesBefore)
	assert.Equal(t, indexBefore, store.objects["streams/v1/index.json"].body)
}

// TestPublishRepairsMissingImmutableArtifact proves repeat publication restores referenced content.
func TestPublishRepairsMissingImmutableArtifact(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := writeVMAt(t, 0)
	store := &fakeStore{}
	service := newTestService(store, "")
	service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, publishOnly(service, metadataPath, diskPath))
	require.NotEmpty(t, store.created)
	missingKey := store.created[0].Key.String()
	delete(store.objects, missingKey)
	commitsBefore := store.commitCalls

	require.NoError(t, publishOnly(service, metadataPath, diskPath))
	assert.Contains(t, store.objects, missingKey)
	assert.Equal(t, commitsBefore, store.commitCalls)
}

// TestPublishRejectsMismatchedImmutableObject proves stored checksum records are mandatory.
func TestPublishRejectsMismatchedImmutableObject(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := writeVMAt(t, 0)
	store := &fakeStore{}
	service := newTestService(store, "")
	service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, publishOnly(service, metadataPath, diskPath))
	artifactKey := store.created[0].Key.String()
	stored := store.objects[artifactKey]
	stored.attributes.ChecksumCRC64NVME = nil
	store.objects[artifactKey] = stored

	err := publishOnly(service, metadataPath, diskPath)
	require.Error(t, err)
	assert.Equal(t, failure.KindContentConflict, failure.KindOf(err))
}

// TestPublishRetriesConcurrentCompatibleUpdate proves a fresh Mirror preserves both writers.
func TestPublishRetriesConcurrentCompatibleUpdate(t *testing.T) {
	t.Parallel()
	firstMetadata, firstDisk := writeVMAt(t, 0)
	secondMetadata, secondDisk := writeVMAt(t, time.Hour)
	thirdMetadata, thirdDisk := writeVMAt(t, 2*time.Hour)
	store := &fakeStore{}
	service := newTestService(store, "")
	service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, publishOnly(service, firstMetadata, firstDisk))

	store.beforeCommit = func(store *fakeStore, call int, _ CreateObject, _ object.CatalogRevision) {
		if call != 2 {
			return
		}
		store.beforeCommit = nil
		concurrent := newTestService(store, "")
		concurrent.now = func() time.Time { return time.Date(2026, 7, 19, 12, 3, 0, 0, time.UTC) }
		require.NoError(t, publishOnly(concurrent, thirdMetadata, thirdDisk))
	}

	require.NoError(t, publishOnly(service, secondMetadata, secondDisk))
	assert.Len(t, publishedVersions(t, store), 3)
	assert.Equal(t, 4, store.commitCalls)
}

// TestPublishRejectsConcurrentIncompatibleUpdate proves a winning conflicting version is never overwritten.
func TestPublishRejectsConcurrentIncompatibleUpdate(t *testing.T) {
	t.Parallel()
	outerMetadata, outerDisk := writeVMAt(t, 0)
	winningMetadata, winningDisk := writeVMAt(t, 0)
	data, err := os.ReadFile(winningDisk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(winningDisk, append(data, []byte("different")...), testFileMode))
	store := &fakeStore{}
	outer := newTestService(store, "")
	outer.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	store.beforeCommit = func(store *fakeStore, call int, _ CreateObject, _ object.CatalogRevision) {
		if call != 1 {
			return
		}
		store.beforeCommit = nil
		winner := newTestService(store, "")
		winner.now = func() time.Time { return time.Date(2026, 7, 19, 12, 1, 0, 0, time.UTC) }
		require.NoError(t, publishOnly(winner, winningMetadata, winningDisk))
	}

	err = publishOnly(outer, outerMetadata, outerDisk)
	require.Error(t, err)
	assert.Equal(t, failure.KindCatalogConflict, failure.KindOf(err))
	assert.Len(t, publishedVersions(t, store), 1)
}

// TestPublishBoundsCompareAndSwapRetries proves only preconditions receive application retries.
func TestPublishBoundsCompareAndSwapRetries(t *testing.T) {
	t.Parallel()
	firstMetadata, firstDisk := writeVMAt(t, 0)
	secondMetadata, secondDisk := writeVMAt(t, time.Hour)
	store := &fakeStore{}
	base := newTestService(store, "")
	base.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	require.NoError(t, publishOnly(base, firstMetadata, firstDisk))
	commitsBefore := store.commitCalls
	service := NewService(store, "", Options{CatalogAttempts: 2, CatalogTimeout: time.Second})
	service.now = base.now
	store.beforeCommit = func(store *fakeStore, call int, input CreateObject, _ object.CatalogRevision) {
		current := store.objects[input.Key.String()]
		current.attributes.Revision = fakeRevision([]byte(strconv.Itoa(call)))
		store.objects[input.Key.String()] = current
	}

	err := publishOnly(service, secondMetadata, secondDisk)
	require.Error(t, err)
	assert.Equal(t, failure.KindPrecondition, failure.KindOf(err))
	assert.Equal(t, 2, store.commitCalls-commitsBefore)
}

// TestPublishLostCommitResponseConvergesOnRerun proves accepted unknown outcomes need no rollback.
func TestPublishLostCommitResponseConvergesOnRerun(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := writeVMAt(t, 0)
	store := &fakeStore{}
	store.afterCommit = func(call int) error {
		if call == 1 {
			return failure.New(failure.KindUnavailable, "commit index", "response was lost")
		}
		return nil
	}
	service := newTestService(store, "")
	service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

	err := publishOnly(service, metadataPath, diskPath)
	require.Error(t, err)
	assert.Equal(t, failure.KindUnavailable, failure.KindOf(err))
	assert.Contains(t, store.objects, "streams/v1/index.json")
	commitsBefore := store.commitCalls
	store.afterCommit = nil

	require.NoError(t, publishOnly(service, metadataPath, diskPath))
	assert.Equal(t, commitsBefore, store.commitCalls)
}

// TestPublishCancellationBeforeCommitLeavesARepairableGeneration proves signals never expose partial state.
func TestPublishCancellationBeforeCommitLeavesARepairableGeneration(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := writeVMAt(t, 0)
	store := &fakeStore{}
	service := newTestService(store, "")
	service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }
	ctx, cancel := context.WithCancel(context.Background())
	store.beforeCreate = func(index int, _ CreateObject) {
		if index == 2 {
			cancel()
		}
	}

	_, err := service.Publish(ctx, Request{MetadataPath: metadataPath, DiskPath: diskPath})
	require.Error(t, err)
	assert.Equal(t, failure.KindCanceled, failure.KindOf(err))
	assert.NotContains(t, store.objects, "streams/v1/index.json")
	store.beforeCreate = nil

	require.NoError(t, publishOnly(service, metadataPath, diskPath))
	assert.Contains(t, store.objects, "streams/v1/index.json")
}

// TestPublishStorageFailureLeavesOldIndexAndReruns proves non-CAS failures are not retried in application code.
func TestPublishStorageFailureLeavesOldIndexAndReruns(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := writeVMAt(t, 0)
	store := &fakeStore{}
	store.createFailure = func(index int, _ CreateObject) error {
		if index == 2 {
			return failure.New(failure.KindUnavailable, "create snapshot", "S3 unavailable")
		}
		return nil
	}
	service := newTestService(store, "")
	service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

	err := publishOnly(service, metadataPath, diskPath)
	require.Error(t, err)
	assert.Equal(t, failure.KindUnavailable, failure.KindOf(err))
	assert.NotContains(t, store.objects, "streams/v1/index.json")
	assert.Zero(t, store.commitCalls)
	store.createFailure = nil

	require.NoError(t, publishOnly(service, metadataPath, diskPath))
	assert.Contains(t, store.objects, "streams/v1/index.json")
}

// publishOnly runs one request and returns only its error for concise behavior tests.
func publishOnly(service *Service, metadataPath string, diskPath string) error {
	_, err := service.Publish(context.Background(), Request{MetadataPath: metadataPath, DiskPath: diskPath})
	return err
}

// writeVMAt writes one product version offset from the default fixture creation time.
func writeVMAt(t testing.TB, offset time.Duration) (string, string) {
	t.Helper()
	options := testfixture.DefaultVMOptions()
	options.CreationDate += int64(offset / time.Second)
	return testfixture.WriteSplitVM(t, t.TempDir(), options)
}

// publishedVersions returns the final owned product's rendered version map.
func publishedVersions(t testing.TB, store *fakeStore) map[string]any {
	t.Helper()
	var index map[string]any
	require.NoError(t, json.Unmarshal(store.objects["streams/v1/index.json"].body, &index))
	entries := index["index"].(map[string]any)
	imagesEntry := entries["images"].(map[string]any)
	snapshotKey := imagesEntry["path"].(string)
	var productsDocument map[string]any
	require.NoError(t, json.Unmarshal(store.objects[snapshotKey].body, &productsDocument))
	products := productsDocument["products"].(map[string]any)
	product := products["alpinelinux:3.22:cloud:arm64"].(map[string]any)
	return product["versions"].(map[string]any)
}
