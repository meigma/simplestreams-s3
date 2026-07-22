package catalog

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	simplestreams "github.com/meigma/go-simplestreams"
	incusschema "github.com/meigma/go-simplestreams/schema/incus"

	"github.com/meigma/simplestreams-s3/internal/evidence"
	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/image"
	"github.com/meigma/simplestreams-s3/internal/object"
)

// Current contains the Mirror-decoded catalog generation observed by one publication attempt.
type Current struct {
	// Index is the current root index, or nil when the index is confirmed absent.
	Index *simplestreams.Index
	// ProductFile is the document referenced by the owned images entry, when present.
	ProductFile *simplestreams.ProductFile
}

// candidateCatalog contains one locally validated product/version projection.
type candidateCatalog struct {
	productName ProductName
	versionID   VersionID
	artifacts   [2]ArtifactLocation
	evidence    *EvidenceManifestLocation
	productFile *simplestreams.ProductFile
}

// Merge adopts current and adds vm without discarding compatible catalog state.
func Merge(
	current Current,
	vm image.VMImage,
	additionalAliases []Alias,
	releaseTitle ReleaseTitle,
	updated time.Time,
) (Documents, error) {
	return MergeWithEvidence(current, vm, additionalAliases, releaseTitle, updated, nil)
}

// MergeWithEvidence adopts current and adds vm with an optional custom evidence companion.
func MergeWithEvidence(
	current Current,
	vm image.VMImage,
	additionalAliases []Alias,
	releaseTitle ReleaseTitle,
	updated time.Time,
	bundle *evidence.Bundle,
) (Documents, error) {
	var manifest *evidence.Artifact
	if bundle != nil {
		value := bundle.Manifest()
		manifest = &value
	}
	candidate, err := buildCandidate(vm, additionalAliases, releaseTitle, updated, manifest)
	if err != nil {
		return Documents{}, err
	}
	if validateErr := validateCurrent(current); validateErr != nil {
		return Documents{}, validateErr
	}

	merged, changed, err := mergeProductFile(current.ProductFile, candidate.productFile, candidate.productName)
	if err != nil {
		return Documents{}, err
	}
	indexKey, err := object.NewObjectKey(simplestreams.DefaultIndexPath.String())
	if err != nil {
		return Documents{}, failure.Wrap(failure.KindInternal, "derive index key", err)
	}
	if !changed {
		entry := current.Index.Entries[incusschema.ContentIDImages]
		snapshotKey, parseErr := object.NewObjectKey(entry.Path.String())
		if parseErr != nil {
			return Documents{}, failure.Wrap(failure.KindCatalogConflict, "validate adopted snapshot key", parseErr)
		}
		return Documents{
			productName: candidate.productName,
			versionID:   candidate.versionID,
			artifacts:   candidate.artifacts,
			evidence:    candidate.evidence,
			snapshotKey: snapshotKey,
			indexKey:    indexKey,
		}, nil
	}

	publicationTime, err := mergedPublicationTime(current, updated)
	if err != nil {
		return Documents{}, err
	}
	merged.Updated = publicationTime.Format(time.RFC1123Z)
	return renderMergedDocuments(current.Index, merged, candidate, indexKey)
}

// mergeProductFile clones the adopted product tree and inserts the candidate when needed.
func mergeProductFile(
	current *simplestreams.ProductFile,
	candidate *simplestreams.ProductFile,
	productName ProductName,
) (*simplestreams.ProductFile, bool, error) {
	if current == nil {
		return cloneProductFile(candidate), true, nil
	}
	merged := cloneProductFile(current)
	candidateProduct := candidate.Products[productName.String()]
	if err := rejectAliasConflicts(merged.Products, candidateProduct, productName.String()); err != nil {
		return nil, false, err
	}
	existingProduct, exists := merged.Products[productName.String()]
	if !exists {
		merged.Products[productName.String()] = cloneProduct(candidateProduct)
		return merged, true, nil
	}
	if !sameProductMetadata(existingProduct, candidateProduct) {
		return nil, false, catalogConflict(
			"merge product",
			"product identity already has different metadata or aliases",
		)
	}
	candidateVersion := candidateProduct.Versions[firstVersionName(candidateProduct)]
	existingVersion, exists := existingProduct.Versions[candidateVersion.Name]
	if !exists {
		existingProduct.Versions[candidateVersion.Name] = cloneVersion(candidateVersion)
		return merged, true, nil
	}
	if sameVersion(existingVersion, candidateVersion) {
		return merged, false, nil
	}
	if !sameVersionWithoutEvidence(existingVersion, candidateVersion) {
		return nil, false, catalogConflict("merge product", "product version already has different artifacts")
	}
	existingEvidence, existingHasEvidence := existingVersion.Items[evidence.ManifestItemName]
	candidateEvidence, candidateHasEvidence := candidateVersion.Items[evidence.ManifestItemName]
	switch {
	case !existingHasEvidence && candidateHasEvidence:
		existingVersion.Items[evidence.ManifestItemName] = cloneItem(candidateEvidence)
		return merged, true, nil
	case existingHasEvidence && !candidateHasEvidence:
		return merged, false, nil
	case existingHasEvidence && candidateHasEvidence && !sameItem(existingEvidence, candidateEvidence):
		return nil, false, catalogConflict("merge product", "product version already has different evidence")
	default:
		return merged, false, nil
	}
}

// validateCurrent rejects catalogs that cannot be safely adopted as the V1 owned namespace.
func validateCurrent(current Current) error {
	if current.Index == nil {
		if current.ProductFile != nil {
			return catalogConflict("adopt catalog", "product document exists without a root index")
		}
		return nil
	}
	if current.Index.Format != simplestreams.IndexFormat {
		return catalogConflict("adopt catalog", "root index has an incompatible format")
	}
	entry, ownsImages := current.Index.Entries[incusschema.ContentIDImages]
	if !ownsImages {
		if current.ProductFile != nil {
			return catalogConflict("adopt catalog", "unreferenced images product document was supplied")
		}
		return validateCatalogTimestamps(current)
	}
	if entry == nil || entry.Format != simplestreams.ProductsFormat ||
		entry.DataType != incusschema.DataTypeImageDownloads {
		return catalogConflict("adopt catalog", "images index entry is incompatible with the V1 contract")
	}
	if current.ProductFile == nil {
		return catalogConflict("adopt catalog", "images index entry has no readable product document")
	}
	return validateOwnedProductDocument(current, entry)
}

// validateOwnedProductDocument verifies the owned images document and index projection.
func validateOwnedProductDocument(current Current, entry *simplestreams.IndexEntry) error {
	if current.ProductFile.ContentID != incusschema.ContentIDImages ||
		current.ProductFile.DataType != incusschema.DataTypeImageDownloads {
		return catalogConflict("adopt catalog", "images product document identity is incompatible")
	}
	if err := validateIncusProjection(current.ProductFile); err != nil {
		return failure.Wrap(failure.KindCatalogConflict, "validate adopted product document", err)
	}
	if err := validateCatalogTimestamps(current); err != nil {
		return err
	}
	productNames := sortedProductNames(current.ProductFile.Products)
	entryProducts := slices.Clone(entry.Products)
	slices.Sort(entryProducts)
	if !slices.Equal(productNames, entryProducts) {
		return catalogConflict("adopt catalog", "images index product list does not match its document")
	}
	return validateProductAliases(current.ProductFile, productNames)
}

// validateProductAliases verifies every product and rejects ambiguous alias ownership.
func validateProductAliases(productFile *simplestreams.ProductFile, productNames []string) error {
	aliasOwners := map[Alias]string{}
	for _, productName := range productNames {
		product := productFile.Products[productName]
		aliases, err := validateVMProduct(productName, product)
		if err != nil {
			return err
		}
		for _, alias := range aliases {
			if owner, exists := aliasOwners[alias]; exists && owner != productName {
				return catalogConflict("adopt catalog", "alias is owned by more than one product")
			}
			aliasOwners[alias] = productName
		}
	}
	return nil
}

// validateVMProduct verifies one adopted product identity and its closed split-VM versions.
func validateVMProduct(productName string, product *simplestreams.Product) ([]Alias, error) {
	if product == nil || product.Name != productName || len(product.Versions) == 0 {
		return nil, catalogConflict("adopt product", "product identity or versions are invalid")
	}
	parts := strings.Split(productName, ":")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" ||
		(parts[3] != image.ArchitectureAMD64.String() && parts[3] != image.ArchitectureARM64.String()) {
		return nil, catalogConflict("adopt product", "product name is not a supported V1 identity")
	}
	for key, expected := range map[string]string{
		"os": parts[0], "release": parts[1], "variant": parts[2], "arch": parts[3],
	} {
		value, ok := product.Metadata[key].(string)
		if !ok || value != expected {
			return nil, catalogConflict("adopt product", "product name and metadata disagree")
		}
	}
	aliases, err := parseAliases(product.Metadata["aliases"])
	if err != nil {
		return nil, err
	}
	defaultAlias, err := NewAlias(strings.Join(parts[:3], "/"))
	if err != nil || !slices.Contains(aliases, defaultAlias) {
		return nil, catalogConflict("adopt product", "product is missing its default alias")
	}
	for versionName, version := range product.Versions {
		if err := validateVMVersion(versionName, version); err != nil {
			return nil, err
		}
	}
	return aliases, nil
}

// validateVMVersion verifies the split-VM items and optional evidence companion.
func validateVMVersion(versionName string, version *simplestreams.Version) error {
	if version == nil || version.Name != versionName || len(version.Metadata) != 0 ||
		(len(version.Items) != 2 && len(version.Items) != 3) {
		return catalogConflict("adopt version", "version is not the supported VM item set")
	}
	if _, err := time.Parse("200601021504", versionName); err != nil {
		return failure.Wrap(failure.KindCatalogConflict, "validate adopted version identity", err)
	}
	metadataItem, metadataExists := version.Items[artifactMetadataName]
	diskItem, diskExists := version.Items[artifactDiskName]
	if !metadataExists || !diskExists {
		return catalogConflict("adopt version", "version is missing a required V1 VM item")
	}
	if err := validateArtifactItem(metadataItem, image.ArtifactMetadata, ".incus.tar.xz"); err != nil {
		return err
	}
	if len(metadataItem.Metadata) != 1 {
		return catalogConflict("adopt version", "metadata item has incompatible fields")
	}
	combined, ok := metadataItem.Metadata["combined_disk-kvm-img_sha256"].(string)
	if !ok {
		return catalogConflict("adopt version", "metadata item is missing the combined fingerprint")
	}
	if _, err := object.ParseSHA256Digest(combined); err != nil {
		return failure.Wrap(failure.KindCatalogConflict, "validate adopted fingerprint", err)
	}
	if err := validateArtifactItem(diskItem, image.ArtifactDisk, ".qcow2"); err != nil {
		return err
	}
	if len(diskItem.Metadata) != 0 {
		return catalogConflict("adopt version", "disk item has incompatible fields")
	}
	evidenceItem, hasEvidence := version.Items[evidence.ManifestItemName]
	if len(version.Items) == 3 && !hasEvidence {
		return catalogConflict("adopt version", "version contains an unsupported companion item")
	}
	if hasEvidence {
		if err := validateEvidenceItem(evidenceItem); err != nil {
			return err
		}
	}
	return nil
}

// validateEvidenceItem verifies the sole custom version item and its content address.
func validateEvidenceItem(item *simplestreams.Item) error {
	if item == nil || item.FileType != evidence.ManifestFileType || item.Size == nil || *item.Size <= 0 ||
		item.MD5 != "" || item.SHA512 != "" || len(item.Mirrors) != 0 || len(item.Metadata) != 0 {
		return catalogConflict("adopt evidence manifest", "evidence item fields are incompatible")
	}
	digest, err := object.ParseSHA256Digest(item.SHA256)
	if err != nil {
		return failure.Wrap(failure.KindCatalogConflict, "validate adopted evidence digest", err)
	}
	want := "images/evidence/" + digest.String() + ".evidence-manifest.json"
	if item.Path.String() != want {
		return catalogConflict("adopt evidence manifest", "evidence path is not its content address")
	}
	return nil
}

// validateArtifactItem verifies one content-addressed immutable artifact reference.
func validateArtifactItem(item *simplestreams.Item, kind image.ArtifactKind, suffix string) error {
	if item == nil || item.FileType != kind.String() || item.Size == nil || *item.Size <= 0 ||
		item.MD5 != "" || item.SHA512 != "" || len(item.Mirrors) != 0 {
		return catalogConflict("adopt artifact", "artifact fields are incompatible with V1")
	}
	digest, err := object.ParseSHA256Digest(item.SHA256)
	if err != nil {
		return failure.Wrap(failure.KindCatalogConflict, "validate adopted artifact digest", err)
	}
	if item.Path.String() != "images/"+digest.String()+suffix {
		return catalogConflict("adopt artifact", "artifact path is not its V1 content address")
	}
	return nil
}

// rejectAliasConflicts prevents a candidate alias from moving between product identities.
func rejectAliasConflicts(
	products map[string]*simplestreams.Product,
	candidate *simplestreams.Product,
	candidateName string,
) error {
	candidateAliases, err := parseAliases(candidate.Metadata["aliases"])
	if err != nil {
		return err
	}
	for productName, product := range products {
		if productName == candidateName {
			continue
		}
		existingAliases, parseErr := parseAliases(product.Metadata["aliases"])
		if parseErr != nil {
			return parseErr
		}
		for _, alias := range candidateAliases {
			if slices.Contains(existingAliases, alias) {
				return catalogConflict("merge product", "alias is already owned by another product")
			}
		}
	}
	return nil
}

// parseAliases validates, normalizes, and deduplicates an adopted alias field.
func parseAliases(value any) ([]Alias, error) {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return nil, catalogConflict("adopt aliases", "aliases must be a non-empty string")
	}
	parts := strings.Split(raw, ",")
	aliases := make([]Alias, 0, len(parts))
	for _, part := range parts {
		alias, err := NewAlias(part)
		if err != nil {
			return nil, failure.Wrap(failure.KindCatalogConflict, "validate adopted alias", err)
		}
		aliases = append(aliases, alias)
	}
	return normalizeAliases(aliases), nil
}

// sameProductMetadata compares owned metadata while treating alias order as insignificant.
func sameProductMetadata(left *simplestreams.Product, right *simplestreams.Product) bool {
	leftMetadata := cloneMetadata(left.Metadata)
	rightMetadata := cloneMetadata(right.Metadata)
	leftAliases, leftErr := parseAliases(leftMetadata["aliases"])
	rightAliases, rightErr := parseAliases(rightMetadata["aliases"])
	delete(leftMetadata, "aliases")
	delete(rightMetadata, "aliases")
	return leftErr == nil && rightErr == nil && slices.Equal(leftAliases, rightAliases) &&
		reflect.DeepEqual(leftMetadata, rightMetadata)
}

// sameVersion compares the observable V1 version document fields.
func sameVersion(left *simplestreams.Version, right *simplestreams.Version) bool {
	if left == nil || right == nil || left.Name != right.Name || !sameMetadata(left.Metadata, right.Metadata) ||
		len(left.Items) != len(right.Items) {
		return false
	}
	for name, leftItem := range left.Items {
		rightItem, exists := right.Items[name]
		if !exists || !sameItem(leftItem, rightItem) {
			return false
		}
	}
	return true
}

// sameVersionWithoutEvidence compares only the immutable Incus image artifact set.
func sameVersionWithoutEvidence(left *simplestreams.Version, right *simplestreams.Version) bool {
	leftCopy := cloneVersion(left)
	rightCopy := cloneVersion(right)
	delete(leftCopy.Items, evidence.ManifestItemName)
	delete(rightCopy.Items, evidence.ManifestItemName)
	return sameVersion(leftCopy, rightCopy)
}

// sameItem compares one rendered Simple Streams item without its private parent link.
func sameItem(left *simplestreams.Item, right *simplestreams.Item) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Name == right.Name && left.FileType == right.FileType && left.Path == right.Path &&
		reflect.DeepEqual(left.Size, right.Size) && left.MD5 == right.MD5 && left.SHA256 == right.SHA256 &&
		left.SHA512 == right.SHA512 && slices.Equal(left.Mirrors, right.Mirrors) &&
		sameMetadata(left.Metadata, right.Metadata)
}

// sameMetadata compares decoded and constructed metadata with nil and empty maps equivalent.
func sameMetadata(left map[string]any, right map[string]any) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		rightValue, exists := right[key]
		if !exists || !reflect.DeepEqual(leftValue, rightValue) {
			return false
		}
	}
	return true
}

// mergedPublicationTime keeps catalog time monotonic across a changed generation.
func mergedPublicationTime(current Current, frozen time.Time) (time.Time, error) {
	result := frozen.UTC().Truncate(time.Second)
	if current.Index == nil {
		return result, nil
	}
	values := []string{current.Index.Updated}
	if entry := current.Index.Entries[incusschema.ContentIDImages]; entry != nil {
		values = append(values, entry.Updated)
	}
	if current.ProductFile != nil {
		values = append(values, current.ProductFile.Updated)
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC1123Z, value)
		if err != nil {
			return time.Time{}, failure.Wrap(failure.KindCatalogConflict, "parse adopted catalog timestamp", err)
		}
		if parsed.After(result) {
			result = parsed
		}
	}
	return result, nil
}

// validateCatalogTimestamps rejects non-empty timestamps that cannot participate in monotonic updates.
func validateCatalogTimestamps(current Current) error {
	_, err := mergedPublicationTime(current, time.Unix(0, 0).UTC())
	return err
}

// renderMergedDocuments renders one changed immutable snapshot and replacement root index.
func renderMergedDocuments(
	currentIndex *simplestreams.Index,
	productFile *simplestreams.ProductFile,
	candidate candidateCatalog,
	indexKey object.ObjectKey,
) (Documents, error) {
	snapshot, err := simplestreams.MarshalJSONDocument(productFile)
	if err != nil {
		return Documents{}, failure.Wrap(failure.KindInternal, "render product document", err)
	}
	snapshotDigest := object.DigestBytes(snapshot)
	snapshotKey, err := object.NewObjectKey("streams/v1/images-" + snapshotDigest.String() + ".json")
	if err != nil {
		return Documents{}, failure.Wrap(failure.KindInternal, "derive product document key", err)
	}
	var indexBody []byte
	if currentIndex == nil {
		indexBody, _, err = buildIndex(productFile, candidate.productName, snapshotKey)
	} else {
		indexBody, err = buildMergedIndex(currentIndex, productFile, snapshotKey)
	}
	if err != nil {
		return Documents{}, err
	}
	return Documents{
		productName: candidate.productName,
		versionID:   candidate.versionID,
		artifacts:   candidate.artifacts,
		evidence:    candidate.evidence,
		changed:     true,
		snapshotKey: snapshotKey,
		snapshot:    snapshot,
		indexKey:    indexKey,
		index:       indexBody,
	}, nil
}

// validateIncusProjection applies the closed Incus schema after removing the custom item Incus ignores.
func validateIncusProjection(productFile *simplestreams.ProductFile) error {
	incusProjection := cloneProductFile(productFile)
	for _, product := range incusProjection.Products {
		for _, version := range product.Versions {
			delete(version.Items, evidence.ManifestItemName)
		}
	}
	return incusschema.ValidateRuntimeProductFile(incusProjection)
}

// buildMergedIndex preserves other entries and metadata while replacing only images.
func buildMergedIndex(
	current *simplestreams.Index,
	productFile *simplestreams.ProductFile,
	snapshotKey object.ObjectKey,
) ([]byte, error) {
	snapshotPath, err := simplestreams.ParseRelativePath(snapshotKey.String())
	if err != nil {
		return nil, failure.Wrap(failure.KindInternal, "derive product document path", err)
	}
	entries := make([]simplestreams.BuildIndexEntry, 0, len(current.Entries)+1)
	for contentID, entry := range current.Entries {
		if contentID == incusschema.ContentIDImages {
			continue
		}
		entries = append(entries, simplestreams.BuildIndexEntry{
			ContentID: contentID,
			Path:      entry.Path,
			Format:    entry.Format,
			DataType:  entry.DataType,
			Updated:   entry.Updated,
			Products:  slices.Clone(entry.Products),
		})
	}
	entries = append(entries, simplestreams.BuildIndexEntry{
		ContentID: incusschema.ContentIDImages,
		Path:      snapshotPath,
		Format:    simplestreams.ProductsFormat,
		DataType:  incusschema.DataTypeImageDownloads,
		Updated:   productFile.Updated,
		Products:  sortedProductNames(productFile.Products),
	})
	index, err := simplestreams.BuildIndex(entries, productFile.Updated)
	if err != nil {
		return nil, failure.Wrap(failure.KindInternal, "build merged index document", err)
	}
	index.Metadata = cloneMetadata(current.Metadata)
	for contentID, entry := range index.Entries {
		if previous := current.Entries[contentID]; previous != nil {
			entry.Metadata = cloneMetadata(previous.Metadata)
		}
	}
	body, err := simplestreams.MarshalJSONDocument(index)
	if err != nil {
		return nil, failure.Wrap(failure.KindInternal, "render merged index document", err)
	}
	return body, nil
}

// sortedProductNames returns the deterministic product list stored in the index entry.
func sortedProductNames(products map[string]*simplestreams.Product) []string {
	names := make([]string, 0, len(products))
	for name := range products {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// firstVersionName returns the sole candidate version identity.
func firstVersionName(product *simplestreams.Product) string {
	for name := range product.Versions {
		return name
	}
	return ""
}

// cloneProductFile copies the mutable product tree while preserving admitted metadata values.
func cloneProductFile(source *simplestreams.ProductFile) *simplestreams.ProductFile {
	result := &simplestreams.ProductFile{
		Format:    source.Format,
		ContentID: source.ContentID,
		DataType:  source.DataType,
		Updated:   source.Updated,
		Metadata: cloneMetadata(
			source.Metadata,
		),
		Products: make(map[string]*simplestreams.Product, len(source.Products)),
	}
	for name, product := range source.Products {
		result.Products[name] = cloneProduct(product)
	}
	return result
}

// cloneProduct copies one product and its versions.
func cloneProduct(source *simplestreams.Product) *simplestreams.Product {
	result := &simplestreams.Product{
		Name: source.Name, Metadata: cloneMetadata(source.Metadata),
		Versions: make(map[string]*simplestreams.Version, len(source.Versions)),
	}
	for name, version := range source.Versions {
		result.Versions[name] = cloneVersion(version)
	}
	return result
}

// cloneVersion copies one version and its exact item set.
func cloneVersion(source *simplestreams.Version) *simplestreams.Version {
	result := &simplestreams.Version{
		Name: source.Name, Metadata: cloneMetadata(source.Metadata),
		Items: make(map[string]*simplestreams.Item, len(source.Items)),
	}
	for name, item := range source.Items {
		result.Items[name] = cloneItem(item)
	}
	return result
}

// cloneItem copies one item without retaining its Mirror parent link.
func cloneItem(source *simplestreams.Item) *simplestreams.Item {
	if source == nil {
		return nil
	}
	result := *source
	if source.Size != nil {
		size := *source.Size
		result.Size = &size
	}
	result.Mirrors = slices.Clone(source.Mirrors)
	result.Metadata = cloneMetadata(source.Metadata)
	return &result
}

// cloneMetadata copies a metadata map without mutating preserved nested JSON values.
func cloneMetadata(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	maps.Copy(result, source)
	return result
}

// catalogConflict constructs one stable adopted-catalog failure.
func catalogConflict(operation string, message string) error {
	return failure.New(failure.KindCatalogConflict, operation, message)
}
