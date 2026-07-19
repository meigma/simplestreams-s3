// Package publish implements optimistic, interruption-safe catalog publication.
package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"time"

	simplestreams "github.com/meigma/go-simplestreams"
	incusschema "github.com/meigma/go-simplestreams/schema/incus"

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

// ObjectAttributes contains the immutable identities and revision recorded by storage.
type ObjectAttributes struct {
	// Size is the stored object size.
	Size object.ByteSize
	// MetadataSHA256 is the publisher-recorded SHA-256 service metadata when present.
	MetadataSHA256 *object.SHA256Digest
	// ChecksumSHA256 is the S3-validated full-object SHA-256 when present.
	ChecksumSHA256 *object.SHA256Digest
	// ChecksumCRC64NVME is the S3-validated full-object CRC-64/NVME when present.
	ChecksumCRC64NVME *object.CRC64NVME
	// Revision is the opaque compare-and-swap identity when present.
	Revision object.CatalogRevision
}

// ReadObject is one streaming object read with its storage attributes.
type ReadObject struct {
	// Body streams the exact stored bytes.
	Body io.ReadCloser
	// Attributes describes the observed object generation.
	Attributes ObjectAttributes
}

// Store is the consumer-owned object-storage port needed by safe publishing.
type Store interface {
	Open(context.Context, object.ObjectKey) (ReadObject, error)
	Stat(context.Context, object.ObjectKey) (ObjectAttributes, error)
	Create(context.Context, CreateObject) error
	Commit(context.Context, CreateObject, object.CatalogRevision) error
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

// Options contains bounded publication orchestration settings.
type Options struct {
	// CatalogAttempts bounds compare-and-swap publication attempts.
	CatalogAttempts int
	// CatalogTimeout bounds each catalog load or write operation.
	CatalogTimeout time.Duration
}

// preparedPublication contains locally validated inputs reused across CAS attempts.
type preparedPublication struct {
	vm      image.VMImage
	aliases []catalog.Alias
	title   catalog.ReleaseTitle
	updated time.Time
}

// Service publishes one validated VM through an optimistic catalog transaction.
type Service struct {
	store   Store
	prefix  object.KeyPrefix
	options Options
	now     func() time.Time
}

// NewService constructs a publisher over a consumer-owned storage port.
func NewService(store Store, prefix object.KeyPrefix, options Options) *Service {
	return &Service{store: store, prefix: prefix, options: options, now: time.Now}
}

// Publish validates local state and conditionally commits one complete catalog generation.
func (service *Service) Publish(ctx context.Context, request Request) (Result, error) {
	prepared, err := service.prepare(request)
	if err != nil {
		return Result{}, err
	}
	for attempt := 1; attempt <= service.options.CatalogAttempts; attempt++ {
		result, retry, publishErr := service.publishAttempt(ctx, prepared)
		if publishErr == nil || !retry || attempt == service.options.CatalogAttempts {
			return result, publishErr
		}
	}
	return Result{}, failure.New(failure.KindInternal, "publish image", "catalog attempt loop exhausted")
}

// prepare validates local state once before any remote operation.
func (service *Service) prepare(request Request) (preparedPublication, error) {
	if service == nil || service.store == nil {
		return preparedPublication{}, failure.New(
			failure.KindInternal,
			"publish image",
			"object store is not configured",
		)
	}
	if service.options.CatalogAttempts < 1 || service.options.CatalogTimeout <= 0 {
		return preparedPublication{}, failure.New(
			failure.KindInternal,
			"publish image",
			"catalog bounds are not configured",
		)
	}
	vm, err := image.Inspect(request.MetadataPath, request.DiskPath)
	if err != nil {
		return preparedPublication{}, err
	}
	titleValue := request.ReleaseTitle
	if titleValue == "" {
		titleValue = vm.Release().String()
	}
	title, err := catalog.NewReleaseTitle(titleValue)
	if err != nil {
		return preparedPublication{}, failure.Wrap(failure.KindInvalidInput, "validate release title", err)
	}
	return preparedPublication{
		vm:      vm,
		aliases: request.Aliases,
		title:   title,
		updated: service.now().UTC(),
	}, nil
}

// publishAttempt tries one fresh read, merge, immutable preparation, and conditional commit.
func (service *Service) publishAttempt(
	ctx context.Context,
	prepared preparedPublication,
) (Result, bool, error) {
	current, revision, err := service.loadCurrent(ctx)
	if err != nil {
		return Result{}, false, err
	}
	documents, err := catalog.Merge(
		current,
		prepared.vm,
		prepared.aliases,
		prepared.title,
		prepared.updated,
	)
	if err != nil {
		return Result{}, false, err
	}
	if err = service.ensureArtifacts(ctx, prepared.vm, documents); err != nil {
		return Result{}, false, err
	}
	result := publishResult(documents)
	if !documents.Changed() {
		return result, false, nil
	}
	if err = service.ensureSnapshot(ctx, documents); err != nil {
		return Result{}, false, err
	}
	if err = ctx.Err(); err != nil {
		return Result{}, false, classifyContext("commit catalog", err)
	}
	err = service.commitIndex(ctx, documents, revision)
	if err == nil {
		return result, false, nil
	}
	return Result{}, failure.IsKind(err, failure.KindPrecondition), err
}

// publishResult extracts the stable product and version identities from documents.
func publishResult(documents catalog.Documents) Result {
	return Result{ProductName: documents.ProductName(), VersionID: documents.VersionID()}
}

// loadCurrent reads one fresh Mirror view and captures the root index revision.
func (service *Service) loadCurrent(ctx context.Context) (catalog.Current, object.CatalogRevision, error) {
	operationContext, cancel := context.WithTimeout(ctx, service.options.CatalogTimeout)
	defer cancel()
	source := &mirrorSource{store: service.store, prefix: service.prefix}
	mirror, err := simplestreams.NewMirror(source)
	if err != nil {
		return catalog.Current{}, "", failure.Wrap(failure.KindInternal, "construct catalog mirror", err)
	}
	index, err := mirror.Index(operationContext)
	if errors.Is(err, simplestreams.ErrNotFound) {
		return catalog.Current{}, "", nil
	}
	if err != nil {
		return catalog.Current{}, "", classifyCatalogRead("read catalog index", err)
	}
	if source.revision.IsZero() {
		return catalog.Current{}, "", failure.New(
			failure.KindCatalogConflict,
			"read catalog index",
			"storage did not return an index revision",
		)
	}
	current := catalog.Current{Index: index}
	if entry := index.Entries[incusschema.ContentIDImages]; entry != nil {
		productFile, productErr := entry.ProductFile(operationContext)
		if productErr != nil {
			return catalog.Current{}, "", classifyCatalogRead("read catalog product document", productErr)
		}
		current.ProductFile = productFile
	}
	return current, source.revision, nil
}

// classifyCatalogRead preserves operational failures and rejects malformed catalog bytes as conflicts.
func classifyCatalogRead(operation string, err error) error {
	switch failure.KindOf(err) {
	case failure.KindUnavailable, failure.KindDeadline, failure.KindUnauthorized, failure.KindCanceled:
		return err
	case failure.KindInvalidInput,
		failure.KindUnsupportedImage,
		failure.KindNotFound,
		failure.KindAlreadyExists,
		failure.KindIntegrity,
		failure.KindContentConflict,
		failure.KindCatalogConflict,
		failure.KindPrecondition,
		failure.KindInternal:
		return failure.Wrap(failure.KindCatalogConflict, operation, err)
	}
	return failure.Wrap(failure.KindCatalogConflict, operation, err)
}

// classifyContext distinguishes cancellation from an exhausted deadline.
func classifyContext(operation string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return failure.Wrap(failure.KindDeadline, operation, err)
	}
	return failure.Wrap(failure.KindCanceled, operation, err)
}

// ensureArtifacts creates or verifies both immutable local image artifacts.
func (service *Service) ensureArtifacts(
	ctx context.Context,
	vm image.VMImage,
	documents catalog.Documents,
) error {
	for _, location := range documents.Artifacts() {
		artifact, err := selectArtifact(vm, location.Kind())
		if err != nil {
			return err
		}
		if err := service.ensureImmutable(
			ctx,
			artifactObject(service.prefix.Join(location.Key()), artifact),
		); err != nil {
			return err
		}
	}
	return nil
}

// ensureSnapshot creates or verifies the immutable merged product document.
func (service *Service) ensureSnapshot(ctx context.Context, documents catalog.Documents) error {
	operationContext, cancel := context.WithTimeout(ctx, service.options.CatalogTimeout)
	defer cancel()
	return service.ensureImmutable(
		operationContext,
		bytesObject(service.prefix.Join(documents.SnapshotKey()), documents.Snapshot(), "application/json"),
	)
}

// ensureImmutable accepts an existing object only after all designed identities match.
func (service *Service) ensureImmutable(ctx context.Context, input CreateObject) error {
	err := service.store.Create(ctx, input)
	if err == nil {
		return nil
	}
	if !failure.IsKind(err, failure.KindPrecondition) {
		return err
	}
	if verifyErr := verifyLocalBody(input); verifyErr != nil {
		return verifyErr
	}
	attributes, err := service.store.Stat(ctx, input.Key)
	if err != nil {
		return err
	}
	if !storedObjectMatches(input, attributes) {
		return failure.New(
			failure.KindContentConflict,
			"verify immutable object",
			"stored object does not match the expected size and checksums",
		)
	}
	return nil
}

// commitIndex conditionally creates or replaces the sole mutable publication pointer.
func (service *Service) commitIndex(
	ctx context.Context,
	documents catalog.Documents,
	revision object.CatalogRevision,
) error {
	operationContext, cancel := context.WithTimeout(ctx, service.options.CatalogTimeout)
	defer cancel()
	return service.store.Commit(
		operationContext,
		bytesObject(service.prefix.Join(documents.IndexKey()), documents.Index(), "application/json"),
		revision,
	)
}

// verifyLocalBody proves a skipped upload still refers to the locally inspected bytes.
func verifyLocalBody(input CreateObject) error {
	body, err := input.Open()
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, body)
	closeErr := body.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return failure.Wrap(failure.KindIntegrity, "close verified upload body", closeErr)
	}
	return nil
}

// storedObjectMatches checks the trusted service and full-object checksum records.
func storedObjectMatches(input CreateObject, attributes ObjectAttributes) bool {
	if attributes.Size != input.Size || attributes.MetadataSHA256 == nil ||
		*attributes.MetadataSHA256 != input.SHA256 {
		return false
	}
	if input.CRC64NVME != nil {
		return attributes.ChecksumCRC64NVME != nil && *attributes.ChecksumCRC64NVME == *input.CRC64NVME
	}
	return attributes.ChecksumSHA256 != nil && *attributes.ChecksumSHA256 == input.SHA256
}

// mirrorSource adapts revision-aware object reads to a single Mirror attempt.
type mirrorSource struct {
	store    Store
	prefix   object.KeyPrefix
	revision object.CatalogRevision
}

// Open reads one mirror-relative document and captures the root index revision.
func (source *mirrorSource) Open(ctx context.Context, path simplestreams.RelativePath) (io.ReadCloser, error) {
	key, err := object.NewObjectKey(path.String())
	if err != nil {
		return nil, err
	}
	result, err := source.store.Open(ctx, source.prefix.Join(key))
	if failure.IsKind(err, failure.KindNotFound) {
		return nil, fmt.Errorf("%w: %w", simplestreams.ErrNotFound, err)
	}
	if err != nil {
		return nil, err
	}
	if path == simplestreams.DefaultIndexPath {
		source.revision = result.Attributes.Revision
	}
	return result.Body, nil
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
