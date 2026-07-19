// Package catalog projects validated VM images into deterministic Simple Streams documents.
package catalog

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	simplestreams "github.com/meigma/go-simplestreams"
	incusschema "github.com/meigma/go-simplestreams/schema/incus"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/image"
	"github.com/meigma/simplestreams-s3/internal/object"
)

// Alias is a validated slash-separated Incus image alias.
type Alias string

// ProductName is the deterministic product-map identity.
type ProductName string

// VersionID is the sortable UTC image creation identity.
type VersionID string

// ReleaseTitle is the human-readable release label.
type ReleaseTitle string

// Requirements is the closed typed V1 requirements value.
type Requirements struct{}

// ArtifactLocation maps one VM artifact to its immutable mirror path.
type ArtifactLocation struct {
	kind image.ArtifactKind
	key  object.ObjectKey
}

// Documents contains the complete empty-catalog publication generation.
type Documents struct {
	productName ProductName
	versionID   VersionID
	artifacts   [2]ArtifactLocation
	snapshotKey object.ObjectKey
	snapshot    []byte
	indexKey    object.ObjectKey
	index       []byte
}

// NewAlias validates one Incus alias without cleaning it.
func NewAlias(value string) (Alias, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || strings.ContainsAny(value, "\\,:") {
		return "", errors.New("alias must be a non-empty relative slash-separated value")
	}
	for segment := range strings.SplitSeq(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("alias contains unsafe segment %q", segment)
		}
	}
	return Alias(value), nil
}

// NewReleaseTitle validates a non-empty release title.
func NewReleaseTitle(value string) (ReleaseTitle, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("release title must not be empty")
	}
	return ReleaseTitle(trimmed), nil
}

// Render constructs and validates one deterministic two-item VM catalog generation.
func Render(
	vm image.VMImage,
	additionalAliases []Alias,
	releaseTitle ReleaseTitle,
	updated time.Time,
) (Documents, error) {
	fingerprint, err := vm.Fingerprint()
	if err != nil {
		return Documents{}, err
	}
	productName, versionID, aliases, err := catalogIdentity(vm, additionalAliases)
	if err != nil {
		return Documents{}, err
	}
	metadataLocation, err := artifactLocation(vm.Metadata(), ".incus.tar.xz")
	if err != nil {
		return Documents{}, err
	}
	diskLocation, err := artifactLocation(vm.Disk(), ".qcow2")
	if err != nil {
		return Documents{}, err
	}
	productFile, err := buildProductFile(
		vm,
		productName,
		versionID,
		aliases,
		releaseTitle,
		updated,
		metadataLocation,
		diskLocation,
		fingerprint,
	)
	if err != nil {
		return Documents{}, err
	}

	snapshot, err := simplestreams.MarshalJSONDocument(productFile)
	if err != nil {
		return Documents{}, failure.Wrap(failure.KindInternal, "render product document", err)
	}
	snapshotDigest := object.DigestBytes(snapshot)
	snapshotKey, err := object.NewObjectKey("streams/v1/images-" + snapshotDigest.String() + ".json")
	if err != nil {
		return Documents{}, failure.Wrap(failure.KindInternal, "derive product document key", err)
	}
	indexBody, indexKey, err := buildIndex(productFile, productName, snapshotKey)
	if err != nil {
		return Documents{}, err
	}
	return Documents{
		productName: productName,
		versionID:   versionID,
		artifacts:   [2]ArtifactLocation{metadataLocation, diskLocation},
		snapshotKey: snapshotKey,
		snapshot:    snapshot,
		indexKey:    indexKey,
		index:       indexBody,
	}, nil
}

// catalogIdentity derives the product, version, and deterministic alias identities.
func catalogIdentity(vm image.VMImage, additionalAliases []Alias) (ProductName, VersionID, []Alias, error) {
	productName := ProductName(strings.Join([]string{
		vm.OperatingSystem().String(),
		vm.Release().String(),
		vm.Variant().String(),
		vm.Architecture().String(),
	}, ":"))
	versionID := VersionID(vm.Created().UTC().Format("200601021504"))
	defaultAlias, err := NewAlias(strings.Join([]string{
		vm.OperatingSystem().String(),
		vm.Release().String(),
		vm.Variant().String(),
	}, "/"))
	if err != nil {
		return "", "", nil, failure.Wrap(failure.KindInternal, "derive default alias", err)
	}
	aliases := normalizeAliases(append([]Alias{defaultAlias}, additionalAliases...))
	return productName, versionID, aliases, nil
}

// buildProductFile constructs and validates the closed Incus VM product document.
func buildProductFile(
	vm image.VMImage,
	productName ProductName,
	versionID VersionID,
	aliases []Alias,
	releaseTitle ReleaseTitle,
	updated time.Time,
	metadataLocation ArtifactLocation,
	diskLocation ArtifactLocation,
	fingerprint object.SHA256Digest,
) (*simplestreams.ProductFile, error) {
	metadataPath, err := simplestreams.ParseRelativePath(metadataLocation.key.String())
	if err != nil {
		return nil, failure.Wrap(failure.KindInternal, "derive metadata artifact path", err)
	}
	diskPath, err := simplestreams.ParseRelativePath(diskLocation.key.String())
	if err != nil {
		return nil, failure.Wrap(failure.KindInternal, "derive disk artifact path", err)
	}
	productFile := simplestreams.NewProductFile(incusschema.ContentIDImages)
	productFile.DataType = incusschema.DataTypeImageDownloads
	productFile.Updated = updated.UTC().Format(time.RFC1123Z)
	product := productFile.SetProduct(productName.String(), nil)
	product.SetMetadata("aliases", joinAliases(aliases))
	product.SetMetadata("arch", vm.Architecture().String())
	product.SetMetadata("os", vm.OperatingSystem().String())
	product.SetMetadata("release", vm.Release().String())
	product.SetMetadata("release_title", releaseTitle.String())
	product.SetMetadata("variant", vm.Variant().String())
	product.SetMetadata("requirements", map[string]any{})
	version := product.SetVersion(versionID.String(), nil)

	metadataSize := vm.Metadata().Size().Int64()
	metadataItem := version.SetItem(artifactMetadataName, &simplestreams.Item{
		FileType: vm.Metadata().Kind().String(),
		Path:     metadataPath,
		Size:     &metadataSize,
		SHA256:   vm.Metadata().SHA256().String(),
	})
	metadataItem.SetMetadata("combined_disk-kvm-img_sha256", fingerprint.String())
	diskSize := vm.Disk().Size().Int64()
	version.SetItem(artifactDiskName, &simplestreams.Item{
		FileType: vm.Disk().Kind().String(),
		Path:     diskPath,
		Size:     &diskSize,
		SHA256:   vm.Disk().SHA256().String(),
	})

	if validationErr := incusschema.ValidateRuntimeProductFile(productFile); validationErr != nil {
		return nil, failure.Wrap(failure.KindInternal, "validate generated product document", validationErr)
	}
	return productFile, nil
}

// buildIndex renders the root publication pointer for one immutable snapshot.
func buildIndex(
	productFile *simplestreams.ProductFile,
	productName ProductName,
	snapshotKey object.ObjectKey,
) ([]byte, object.ObjectKey, error) {
	snapshotPath, err := simplestreams.ParseRelativePath(snapshotKey.String())
	if err != nil {
		return nil, "", failure.Wrap(failure.KindInternal, "derive product document path", err)
	}
	index, err := simplestreams.BuildIndex([]simplestreams.BuildIndexEntry{{
		ContentID: incusschema.ContentIDImages,
		Path:      snapshotPath,
		Format:    simplestreams.ProductsFormat,
		DataType:  incusschema.DataTypeImageDownloads,
		Updated:   productFile.Updated,
		Products:  []string{productName.String()},
	}}, productFile.Updated)
	if err != nil {
		return nil, "", failure.Wrap(failure.KindInternal, "build index document", err)
	}
	indexBody, err := simplestreams.MarshalJSONDocument(index)
	if err != nil {
		return nil, "", failure.Wrap(failure.KindInternal, "render index document", err)
	}
	indexKey, err := object.NewObjectKey(simplestreams.DefaultIndexPath.String())
	if err != nil {
		return nil, "", failure.Wrap(failure.KindInternal, "derive index key", err)
	}
	return indexBody, indexKey, nil
}

const (
	artifactMetadataName = "incus.tar.xz"
	artifactDiskName     = "disk-kvm.img"
)

// ProductName returns the generated catalog product identity.
func (documents Documents) ProductName() ProductName { return documents.productName }

// VersionID returns the generated catalog version identity.
func (documents Documents) VersionID() VersionID { return documents.versionID }

// Artifacts returns the metadata and disk locations in fixed order.
func (documents Documents) Artifacts() [2]ArtifactLocation { return documents.artifacts }

// SnapshotKey returns the immutable product-document key.
func (documents Documents) SnapshotKey() object.ObjectKey { return documents.snapshotKey }

// Snapshot returns a copy of the rendered product document.
func (documents Documents) Snapshot() []byte { return slices.Clone(documents.snapshot) }

// IndexKey returns the sole mutable publication pointer key.
func (documents Documents) IndexKey() object.ObjectKey { return documents.indexKey }

// Index returns a copy of the rendered root index document.
func (documents Documents) Index() []byte { return slices.Clone(documents.index) }

// Kind returns the artifact class stored at the location.
func (location ArtifactLocation) Kind() image.ArtifactKind { return location.kind }

// Key returns the immutable mirror-relative object key.
func (location ArtifactLocation) Key() object.ObjectKey { return location.key }

// String returns the alias wire value.
func (alias Alias) String() string { return string(alias) }

// String returns the product-map key.
func (name ProductName) String() string { return string(name) }

// String returns the version-map key.
func (version VersionID) String() string { return string(version) }

// String returns the release title.
func (title ReleaseTitle) String() string { return string(title) }

// artifactLocation derives one content-addressed path from a validated artifact.
func artifactLocation(artifact image.Artifact, suffix string) (ArtifactLocation, error) {
	key, err := object.NewObjectKey("images/" + artifact.SHA256().String() + suffix)
	if err != nil {
		return ArtifactLocation{}, failure.Wrap(failure.KindInternal, "derive artifact key", err)
	}
	if _, err := simplestreams.ParseRelativePath(key.String()); err != nil {
		return ArtifactLocation{}, failure.Wrap(failure.KindInternal, "derive artifact path", err)
	}
	return ArtifactLocation{kind: artifact.Kind(), key: key}, nil
}

// normalizeAliases deduplicates and sorts aliases for deterministic rendering.
func normalizeAliases(aliases []Alias) []Alias {
	seen := make(map[Alias]struct{}, len(aliases))
	unique := make([]Alias, 0, len(aliases))
	for _, alias := range aliases {
		if _, exists := seen[alias]; exists {
			continue
		}
		seen[alias] = struct{}{}
		unique = append(unique, alias)
	}
	slices.Sort(unique)
	return unique
}

// joinAliases renders the comma-separated Incus alias property.
func joinAliases(aliases []Alias) string {
	values := make([]string, len(aliases))
	for index, alias := range aliases {
		values[index] = alias.String()
	}
	return strings.Join(values, ",")
}
