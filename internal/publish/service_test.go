package publish

import (
	"context"
	"io"
	"os"
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

// fakeStore records publication visibility and create-only write order.
type fakeStore struct {
	exists       bool
	existsCalls  int
	created      []CreateObject
	bodies       map[string][]byte
	beforeCreate func(int, CreateObject)
}

// Exists records the exact index probe and returns configured mirror state.
func (store *fakeStore) Exists(_ context.Context, _ object.ObjectKey) (bool, error) {
	store.existsCalls++
	return store.exists, nil
}

// Create consumes and records one upload body like a storage adapter.
func (store *fakeStore) Create(_ context.Context, input CreateObject) error {
	if store.beforeCreate != nil {
		store.beforeCreate(len(store.created), input)
	}
	body, err := input.Open()
	if err != nil {
		return err
	}
	data, err := io.ReadAll(body)
	if err != nil {
		_ = body.Close()
		return err
	}
	if err := body.Close(); err != nil {
		return err
	}
	store.created = append(store.created, input)
	if store.bodies == nil {
		store.bodies = map[string][]byte{}
	}
	store.bodies[input.Key.String()] = data
	return nil
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
	service := NewService(store, "")

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
	service := NewService(store, prefix)
	service.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

	result, err := service.Publish(context.Background(), Request{
		MetadataPath: metadataPath,
		DiskPath:     diskPath,
	})
	require.NoError(t, err)
	assert.Equal(t, "alpinelinux:3.22:cloud:arm64", result.ProductName.String())
	require.Len(t, store.created, 4)
	assert.True(t, strings.HasSuffix(store.created[0].Key.String(), ".incus.tar.xz"))
	assert.True(t, strings.HasSuffix(store.created[1].Key.String(), ".qcow2"))
	assert.Contains(t, store.created[2].Key.String(), "/streams/v1/images-")
	assert.Equal(t, "private/incus/streams/v1/index.json", store.created[3].Key.String())
	for _, created := range store.created {
		body := store.bodies[created.Key.String()]
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
	service := NewService(store, "")

	_, err := service.Publish(context.Background(), Request{MetadataPath: metadataPath, DiskPath: diskPath})
	require.Error(t, err)
	assert.Equal(t, failure.KindUnsupportedImage, failure.KindOf(err))
	assert.Zero(t, store.existsCalls)
	assert.Empty(t, store.created)
}

// TestPublishDefersExistingCatalogsToPhaseThree proves Phase 2 refuses to overwrite observed state.
func TestPublishDefersExistingCatalogsToPhaseThree(t *testing.T) {
	t.Parallel()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), testfixture.DefaultVMOptions())
	store := &fakeStore{exists: true}
	service := NewService(store, "")

	_, err := service.Publish(context.Background(), Request{MetadataPath: metadataPath, DiskPath: diskPath})
	require.Error(t, err)
	assert.Equal(t, failure.KindCatalogConflict, failure.KindOf(err))
	assert.Empty(t, store.created)
}
