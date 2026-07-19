// Package publish implements the empty-catalog Phase 2 publication use case.
package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"hash"
	"io"
	"time"

	"github.com/meigma/simplestreams-s3/internal/catalog"
	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/image"
	"github.com/meigma/simplestreams-s3/internal/object"
)

// BodyOpener creates a fresh object body for one upload attempt.
type BodyOpener func() (io.ReadCloser, error)

// CreateObject describes one create-only S3-neutral object write.
type CreateObject struct {
	// Key is the mirror-relative object identity before configured-prefix application.
	Key object.ObjectKey
	// Size is the exact expected object size.
	Size object.ByteSize
	// SHA256 is the service metadata and local transfer-integrity identity.
	SHA256 object.SHA256Digest
	// CRC64NVME is the optional full-object service checksum.
	CRC64NVME *object.CRC64NVME
	// ContentType is the stable HTTP media type stored with the object.
	ContentType string
	// Open creates a new sequential body reader.
	Open BodyOpener
}

// Store is the consumer-owned object-storage port needed by Phase 2 publishing.
type Store interface {
	Exists(context.Context, object.ObjectKey) (bool, error)
	Create(context.Context, CreateObject) error
}

// Request contains validated command-boundary publication inputs.
type Request struct {
	// MetadataPath points to the Incus metadata tarball.
	MetadataPath string
	// DiskPath points to the QCOW2 VM disk.
	DiskPath string
	// Aliases contains additional validated Incus aliases.
	Aliases []catalog.Alias
	// ReleaseTitle optionally overrides the metadata release value.
	ReleaseTitle string
}

// Result identifies the catalog generation made visible by Publish.
type Result struct {
	// ProductName is the deterministic product identity.
	ProductName catalog.ProductName
	// VersionID is the deterministic creation-time version identity.
	VersionID catalog.VersionID
}

// Service publishes one validated VM into an empty mirror root.
type Service struct {
	store  Store
	prefix object.KeyPrefix
	now    func() time.Time
}

// NewService constructs a publisher over a consumer-owned storage port.
func NewService(store Store, prefix object.KeyPrefix) *Service {
	return &Service{store: store, prefix: prefix, now: time.Now}
}

// Publish validates all local state before creating immutable objects and the final index.
func (service *Service) Publish(ctx context.Context, request Request) (Result, error) {
	if service == nil || service.store == nil {
		return Result{}, failure.New(failure.KindInternal, "publish image", "object store is not configured")
	}
	vm, err := image.Inspect(request.MetadataPath, request.DiskPath)
	if err != nil {
		return Result{}, err
	}
	titleValue := request.ReleaseTitle
	if titleValue == "" {
		titleValue = vm.Release().String()
	}
	title, err := catalog.NewReleaseTitle(titleValue)
	if err != nil {
		return Result{}, failure.Wrap(failure.KindInvalidInput, "validate release title", err)
	}
	documents, err := catalog.Render(vm, request.Aliases, title, service.now())
	if err != nil {
		return Result{}, err
	}

	exists, err := service.store.Exists(ctx, service.prefix.Join(documents.IndexKey()))
	if err != nil {
		return Result{}, err
	}
	if exists {
		return Result{}, failure.New(
			failure.KindCatalogConflict,
			"publish image",
			"Phase 2 only publishes into an empty mirror; existing catalogs are handled in Phase 3",
		)
	}

	for _, location := range documents.Artifacts() {
		artifact, selectErr := selectArtifact(vm, location.Kind())
		if selectErr != nil {
			return Result{}, selectErr
		}
		if err := service.store.Create(ctx, artifactObject(service.prefix.Join(location.Key()), artifact)); err != nil {
			return Result{}, err
		}
	}
	if err := service.store.Create(
		ctx,
		bytesObject(service.prefix.Join(documents.SnapshotKey()), documents.Snapshot(), "application/json"),
	); err != nil {
		return Result{}, err
	}
	if err := service.store.Create(
		ctx,
		bytesObject(service.prefix.Join(documents.IndexKey()), documents.Index(), "application/json"),
	); err != nil {
		return Result{}, err
	}
	return Result{ProductName: documents.ProductName(), VersionID: documents.VersionID()}, nil
}

// selectArtifact selects one of the structurally fixed VM artifacts.
func selectArtifact(vm image.VMImage, kind image.ArtifactKind) (image.Artifact, error) {
	switch kind {
	case image.ArtifactMetadata:
		return vm.Metadata(), nil
	case image.ArtifactDisk:
		return vm.Disk(), nil
	default:
		return image.Artifact{}, failure.New(failure.KindInternal, "select artifact", "unknown artifact kind")
	}
}

// artifactObject maps a validated local artifact into a verifying create-only write.
func artifactObject(key object.ObjectKey, artifact image.Artifact) CreateObject {
	checksum := artifact.CRC64NVME()
	return CreateObject{
		Key:         key,
		Size:        artifact.Size(),
		SHA256:      artifact.SHA256(),
		CRC64NVME:   &checksum,
		ContentType: "application/octet-stream",
		Open: func() (io.ReadCloser, error) {
			file, err := artifact.Open()
			if err != nil {
				return nil, err
			}
			return newVerifyingReadCloser(file, artifact.Size(), artifact.SHA256()), nil
		},
	}
}

// bytesObject maps a rendered metadata document into a create-only write.
func bytesObject(key object.ObjectKey, body []byte, contentType string) CreateObject {
	digest := object.DigestBytes(body)
	size, err := object.NewByteSize(int64(len(body)))
	if err != nil {
		panic(err)
	}
	return CreateObject{
		Key:         key,
		Size:        size,
		SHA256:      digest,
		ContentType: contentType,
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		},
	}
}

// verifyingReadCloser detects local input mutation before S3 can accept the object.
type verifyingReadCloser struct {
	reader   io.ReadCloser
	hasher   hash.Hash
	expected object.SHA256Digest
	size     object.ByteSize
	read     int64
	checked  bool
}

// newVerifyingReadCloser wraps one sequential upload body with end-of-stream verification.
func newVerifyingReadCloser(reader io.ReadCloser, size object.ByteSize, expected object.SHA256Digest) io.ReadCloser {
	return &verifyingReadCloser{reader: reader, hasher: sha256.New(), expected: expected, size: size}
}

// Read streams bytes while calculating their current identity.
func (reader *verifyingReadCloser) Read(buffer []byte) (int, error) {
	read, err := reader.reader.Read(buffer)
	if read > 0 {
		reader.read += int64(read)
		if _, writeErr := reader.hasher.Write(buffer[:read]); writeErr != nil {
			return read, failure.Wrap(failure.KindIntegrity, "verify upload body", writeErr)
		}
	}
	if err != io.EOF || reader.checked {
		return read, err
	}
	reader.checked = true
	actual, parseErr := object.NewSHA256Digest(reader.hasher.Sum(nil))
	if parseErr != nil {
		return read, failure.Wrap(failure.KindInternal, "verify upload body", parseErr)
	}
	if reader.read != reader.size.Int64() || actual != reader.expected {
		return read, failure.New(failure.KindIntegrity, "verify upload body", "artifact changed after local validation")
	}
	return read, io.EOF
}

// Close closes the underlying local file.
func (reader *verifyingReadCloser) Close() error { return reader.reader.Close() }
