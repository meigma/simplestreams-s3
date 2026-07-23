// Package evidence validates and projects attest-vm-image evidence for publication.
package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/image"
	"github.com/meigma/simplestreams-s3/internal/object"
)

const (
	// ManifestItemName is the Simple Streams version-item name used for evidence discovery.
	ManifestItemName = "evidence-manifest"
	// ManifestFileType is the intentionally custom Simple Streams item vocabulary.
	ManifestFileType = "evidence-manifest"
	// ManifestMediaType is the HTTP content type of the published version-1 handoff.
	ManifestMediaType          = "application/vnd.meigma.vm-image-evidence-manifest.v1+json"
	maxManifestBytes           = int64(1 << 20)
	resultPass                 = "pass"
	roleChecksums              = "checksums"
	roleSBOM                   = "sbom"
	roleVulnerabilityReport    = "vulnerability-report"
	roleValidationReport       = "validation-report"
	roleValidationPredicate    = "validation-predicate"
	roleProvenanceAttestation  = "provenance-attestation"
	roleSBOMAttestation        = "sbom-attestation"
	roleValidationAttestation  = "validation-attestation"
	signedEvidenceRoleCount    = 3
	mediaTypeJSON              = "application/json"
	mediaTypeOctetStream       = "application/octet-stream"
	mediaTypeSigstoreBundle    = "application/vnd.dev.sigstore.bundle+json"
	mediaTypeSigstoreBundleV03 = "application/vnd.dev.sigstore.bundle.v0.3+json"
)

// Artifact is one immutable evidence object prepared for mirror publication.
type Artifact struct {
	role        string
	path        string
	body        []byte
	key         object.ObjectKey
	size        object.ByteSize
	sha256      object.SHA256Digest
	contentType string
}

// Bundle contains every proof object and its rewritten, discoverable manifest.
type Bundle struct {
	objects  []Artifact
	manifest Artifact
}

// sourceManifest is the closed version-1 attest-vm-image handoff wire shape.
type sourceManifest struct {
	SchemaVersion  string           `json:"schemaVersion"`
	Result         string           `json:"result"`
	Artifacts      sourceArtifacts  `json:"artifacts"`
	Evidence       []sourceEvidence `json:"evidence"`
	AttestationURL *string          `json:"attestationUrl,omitempty"`
}

// sourceArtifacts binds the image inputs and optional build manifest.
type sourceArtifacts struct {
	Disk          sourceArtifact  `json:"disk"`
	Metadata      *sourceArtifact `json:"metadata"`
	BuildManifest *sourceArtifact `json:"buildManifest"`
}

// sourceArtifact is one runner-local file reference and digest.
type sourceArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// sourceEvidence is one runner-local proof reference.
type sourceEvidence struct {
	Role      string `json:"role"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"mediaType"`
}

// publishedManifest is the downstream handoff with mirror-relative paths.
type publishedManifest struct {
	SchemaVersion  string              `json:"schemaVersion"`
	Result         string              `json:"result"`
	Artifacts      publishedArtifacts  `json:"artifacts"`
	Evidence       []publishedEvidence `json:"evidence"`
	AttestationURL *string             `json:"attestationUrl,omitempty"`
}

// publishedArtifacts contains fetchable mirror references for all bound inputs.
type publishedArtifacts struct {
	Disk          publishedArtifact  `json:"disk"`
	Metadata      *publishedArtifact `json:"metadata"`
	BuildManifest *publishedArtifact `json:"buildManifest"`
}

// publishedArtifact is one mirror-relative file reference and digest.
type publishedArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// publishedEvidence is one fetchable proof reference.
type publishedEvidence struct {
	Role      string `json:"role"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"mediaType"`
}

// Inspect validates one version-1 attest-vm-image handoff and rewrites local paths to mirror keys.
func Inspect(manifestPath string, vm image.VMImage) (*Bundle, error) {
	source, manifestDirectory, err := decodeManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if source.SchemaVersion != "1" {
		return nil, invalid("evidence manifest schemaVersion must be \"1\"")
	}
	if source.Result != resultPass {
		return nil, invalid("evidence manifest result must be \"pass\"")
	}
	if validationErr := validateImageArtifacts(source.Artifacts, vm); validationErr != nil {
		return nil, validationErr
	}
	if signingErr := validateSigningSet(source.Evidence, source.AttestationURL); signingErr != nil {
		return nil, signingErr
	}

	objects, publishedEvidence, err := inspectEvidence(manifestDirectory, source.Evidence)
	if err != nil {
		return nil, err
	}
	projectedArtifacts := publishedArtifacts{
		Disk: publishedArtifact{
			Path:   imageKey(vm.Disk(), ".qcow2").String(),
			SHA256: vm.Disk().SHA256().String(),
		},
		Metadata: &publishedArtifact{
			Path:   imageKey(vm.Metadata(), ".incus.tar.xz").String(),
			SHA256: vm.Metadata().SHA256().String(),
		},
	}
	if source.Artifacts.BuildManifest != nil {
		artifact, inspectErr := inspectFile(
			"build-manifest",
			source.Artifacts.BuildManifest.Path,
			source.Artifacts.BuildManifest.SHA256,
			mediaTypeOctetStream,
			false,
			manifestDirectory,
		)
		if inspectErr != nil {
			return nil, inspectErr
		}
		objects = append(objects, artifact)
		projectedArtifacts.BuildManifest = &publishedArtifact{
			Path: artifact.key.String(), SHA256: artifact.sha256.String(),
		}
	}

	published := publishedManifest{
		SchemaVersion:  "1",
		Result:         resultPass,
		Artifacts:      projectedArtifacts,
		Evidence:       publishedEvidence,
		AttestationURL: source.AttestationURL,
	}
	body, err := json.MarshalIndent(published, "", "  ")
	if err != nil {
		return nil, failure.Wrap(failure.KindInternal, "render evidence manifest", err)
	}
	manifest, err := bytesArtifact(ManifestItemName, body, ManifestMediaType, ".evidence-manifest.json")
	if err != nil {
		return nil, err
	}
	return &Bundle{objects: objects, manifest: manifest}, nil
}

// validateSigningSet accepts unsigned evidence or a complete three-bundle signing set.
func validateSigningSet(sources []sourceEvidence, attestationURL *string) error {
	signedCount := 0
	for _, source := range sources {
		switch source.Role {
		case roleProvenanceAttestation, roleSBOMAttestation, roleValidationAttestation:
			signedCount++
		}
	}
	if attestationURL != nil && *attestationURL == "" {
		return invalid("evidence attestation URL must not be empty")
	}
	if signedCount == 0 && attestationURL == nil {
		return nil
	}
	if signedCount == 0 {
		return invalid("evidence attestation URL requires all three bundles")
	}
	if signedCount != signedEvidenceRoleCount {
		return invalid("signed evidence must include all three bundles")
	}
	return nil
}

// decodeManifest strictly decodes the bounded handoff and resolves its directory.
func decodeManifest(manifestPath string) (sourceManifest, string, error) {
	file, err := os.Open(manifestPath)
	if err != nil {
		return sourceManifest{}, "", failure.Wrap(failure.KindInvalidInput, "open evidence manifest", err)
	}
	defer file.Close()
	information, err := file.Stat()
	if err != nil {
		return sourceManifest{}, "", failure.Wrap(failure.KindInvalidInput, "stat evidence manifest", err)
	}
	if information.Size() > maxManifestBytes {
		return sourceManifest{}, "", invalid("evidence manifest exceeds 1 MiB")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxManifestBytes+1))
	decoder.DisallowUnknownFields()
	var manifest sourceManifest
	if err = decoder.Decode(&manifest); err != nil {
		return sourceManifest{}, "", failure.Wrap(failure.KindInvalidInput, "decode evidence manifest", err)
	}
	if err = ensureJSONEOF(decoder); err != nil {
		return sourceManifest{}, "", err
	}
	absolute, err := filepath.Abs(manifestPath)
	if err != nil {
		return sourceManifest{}, "", failure.Wrap(failure.KindInvalidInput, "resolve evidence manifest", err)
	}
	resolvedManifest, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return sourceManifest{}, "", failure.Wrap(failure.KindInvalidInput, "resolve evidence manifest", err)
	}
	return manifest, filepath.Dir(resolvedManifest), nil
}

// ensureJSONEOF rejects trailing JSON values after the manifest object.
func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return invalid("evidence manifest contains multiple JSON values")
	}
	return failure.Wrap(failure.KindInvalidInput, "decode evidence manifest", err)
}

// validateImageArtifacts requires the handoff to bind both published image artifacts.
func validateImageArtifacts(artifacts sourceArtifacts, vm image.VMImage) error {
	if artifacts.Metadata == nil {
		return invalid("evidence manifest must bind the Incus metadata artifact")
	}
	if artifacts.Disk.Path == "" || artifacts.Metadata.Path == "" {
		return invalid("evidence manifest image artifact paths must not be empty")
	}
	if err := matchDigest("disk", artifacts.Disk.SHA256, vm.Disk().SHA256()); err != nil {
		return err
	}
	return matchDigest("metadata", artifacts.Metadata.SHA256, vm.Metadata().SHA256())
}

// inspectEvidence validates the closed proof vocabulary and prepares immutable objects.
func inspectEvidence(
	manifestDirectory string,
	sources []sourceEvidence,
) ([]Artifact, []publishedEvidence, error) {
	requiredRoles := requiredEvidenceRoles()
	if len(sources) < len(requiredRoles) || len(sources) > 8 {
		return nil, nil, invalid("evidence manifest has an incomplete or excessive evidence set")
	}
	seen := make(map[string]struct{}, len(sources))
	objects := make([]Artifact, 0, len(sources))
	published := make([]publishedEvidence, 0, len(sources))
	for _, source := range sources {
		mediaTypes := allowedMediaTypes(source.Role)
		if len(mediaTypes) == 0 || !slices.Contains(mediaTypes, source.MediaType) {
			return nil, nil, invalid("evidence manifest contains an unsupported role or media type")
		}
		if _, duplicate := seen[source.Role]; duplicate {
			return nil, nil, invalid("evidence manifest contains a duplicate role")
		}
		seen[source.Role] = struct{}{}
		artifact, err := inspectFile(
			source.Role,
			source.Path,
			source.SHA256,
			source.MediaType,
			true,
			manifestDirectory,
		)
		if err != nil {
			return nil, nil, err
		}
		objects = append(objects, artifact)
		published = append(published, publishedEvidence{
			Role:      source.Role,
			Path:      artifact.key.String(),
			SHA256:    artifact.sha256.String(),
			MediaType: source.MediaType,
		})
	}
	for _, role := range requiredRoles {
		if _, exists := seen[role]; !exists {
			return nil, nil, invalid("evidence manifest is missing required role " + role)
		}
	}
	return objects, published, nil
}

// requiredEvidenceRoles returns the unsigned proof set every version-1 handoff contains.
func requiredEvidenceRoles() []string {
	return []string{
		roleChecksums,
		roleSBOM,
		roleVulnerabilityReport,
		roleValidationReport,
		roleValidationPredicate,
	}
}

// allowedMediaTypes returns the closed version-1 media-type vocabulary for role.
func allowedMediaTypes(role string) []string {
	switch role {
	case roleChecksums:
		return []string{"text/plain"}
	case roleSBOM:
		return []string{"application/spdx+json", "application/vnd.cyclonedx+json"}
	case roleVulnerabilityReport, roleValidationReport:
		return []string{mediaTypeJSON}
	case roleValidationPredicate:
		return []string{"application/vnd.in-toto+json"}
	case roleProvenanceAttestation, roleSBOMAttestation, roleValidationAttestation:
		return []string{mediaTypeSigstoreBundle, mediaTypeSigstoreBundleV03}
	default:
		return nil
	}
}

// inspectFile resolves and hashes one manifest-referenced local file.
func inspectFile(
	role string,
	path string,
	digestText string,
	contentType string,
	requireContained bool,
	manifestDirectory string,
) (Artifact, error) {
	if path == "" {
		return Artifact{}, invalid("evidence file path must not be empty")
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInvalidInput, "resolve evidence file", err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInvalidInput, "resolve evidence file", err)
	}
	if requireContained && !pathWithin(manifestDirectory, resolved) {
		return Artifact{}, invalid("evidence files must be contained by the manifest directory")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInvalidInput, "open evidence file", err)
	}
	digest, size, digestErr := object.DigestReader(file)
	closeErr := file.Close()
	if digestErr != nil {
		return Artifact{}, failure.Wrap(failure.KindInvalidInput, "hash evidence file", digestErr)
	}
	if closeErr != nil {
		return Artifact{}, failure.Wrap(failure.KindInvalidInput, "close evidence file", closeErr)
	}
	if err = matchDigest(role, digestText, digest); err != nil {
		return Artifact{}, err
	}
	key, err := object.NewObjectKey("images/evidence/" + digest.String() + "/" + role)
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInternal, "derive evidence key", err)
	}
	return Artifact{
		role: role, path: resolved, key: key, size: size, sha256: digest, contentType: contentType,
	}, nil
}

// bytesArtifact prepares one in-memory rendered document for immutable publication.
func bytesArtifact(role string, body []byte, contentType string, suffix string) (Artifact, error) {
	digest := object.DigestBytes(body)
	size, err := object.NewByteSize(int64(len(body)))
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInternal, "size evidence manifest", err)
	}
	key, err := object.NewObjectKey("images/evidence/" + digest.String() + suffix)
	if err != nil {
		return Artifact{}, failure.Wrap(failure.KindInternal, "derive evidence manifest key", err)
	}
	return Artifact{role: role, body: body, key: key, size: size, sha256: digest, contentType: contentType}, nil
}

// matchDigest requires a canonical lowercase digest equal to expected.
func matchDigest(name string, value string, expected object.SHA256Digest) error {
	parsed, err := object.ParseSHA256Digest(value)
	if err != nil || value != expected.String() || parsed != expected {
		return failure.New(failure.KindIntegrity, "validate evidence manifest", name+" SHA-256 does not match")
	}
	return nil
}

// imageKey reproduces the publisher's stable content-addressed image path.
func imageKey(artifact image.Artifact, suffix string) object.ObjectKey {
	key, err := object.NewObjectKey("images/" + artifact.SHA256().String() + suffix)
	if err != nil {
		panic(err)
	}
	return key
}

// pathWithin reports whether target is directory or one of its descendants.
func pathWithin(directory string, target string) bool {
	relative, err := filepath.Rel(directory, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// invalid constructs a stable evidence-manifest input failure.
func invalid(message string) error {
	return failure.New(failure.KindInvalidInput, "validate evidence manifest", message)
}

// Objects returns the proof objects referenced by Manifest in publication order.
func (bundle Bundle) Objects() []Artifact { return slices.Clone(bundle.objects) }

// Manifest returns the rewritten discoverable manifest object.
func (bundle Bundle) Manifest() Artifact { return bundle.manifest }

// Role returns the stable evidence role.
func (artifact Artifact) Role() string { return artifact.role }

// Key returns the immutable mirror-relative object key.
func (artifact Artifact) Key() object.ObjectKey { return artifact.key }

// Size returns the exact object size.
func (artifact Artifact) Size() object.ByteSize { return artifact.size }

// SHA256 returns the exact object digest.
func (artifact Artifact) SHA256() object.SHA256Digest { return artifact.sha256 }

// ContentType returns the stable HTTP media type.
func (artifact Artifact) ContentType() string { return artifact.contentType }

// Open returns a fresh reader over the validated local file or rendered manifest.
func (artifact Artifact) Open() (io.ReadCloser, error) {
	if artifact.body != nil {
		return io.NopCloser(bytes.NewReader(artifact.body)), nil
	}
	file, err := os.Open(artifact.path)
	if err != nil {
		return nil, failure.Wrap(failure.KindInvalidInput, fmt.Sprintf("open %s evidence", artifact.role), err)
	}
	return file, nil
}
