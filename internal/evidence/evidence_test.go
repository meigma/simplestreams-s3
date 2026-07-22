package evidence

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/image"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

// TestInspectProjectsCompletePassingEvidence proves local handoff paths become fetchable mirror paths.
func TestInspectProjectsCompletePassingEvidence(t *testing.T) {
	t.Parallel()
	manifestPath, vm := writePassingManifest(t, true)

	bundle, err := Inspect(manifestPath, vm)
	require.NoError(t, err)
	require.Len(t, bundle.Objects(), 9)
	assert.Equal(t, ManifestFileType, bundle.Manifest().Role())
	assert.Equal(t, ManifestMediaType, bundle.Manifest().ContentType())
	assert.Contains(t, bundle.Manifest().Key().String(), "images/evidence/")

	body := readArtifact(t, bundle.Manifest())
	var published publishedManifest
	require.NoError(t, json.Unmarshal(body, &published))
	assert.Equal(t, "pass", published.Result)
	require.NotNil(t, published.AttestationURL)
	assert.Equal(t, "https://github.com/meigma/example/attestations/1", *published.AttestationURL)
	assert.Equal(t, vm.Disk().SHA256().String(), published.Artifacts.Disk.SHA256)
	assert.Equal(t, "images/"+vm.Disk().SHA256().String()+".qcow2", published.Artifacts.Disk.Path)
	require.NotNil(t, published.Artifacts.Metadata)
	assert.Equal(
		t,
		"images/"+vm.Metadata().SHA256().String()+".incus.tar.xz",
		published.Artifacts.Metadata.Path,
	)
	require.NotNil(t, published.Artifacts.BuildManifest)
	assert.Contains(t, published.Artifacts.BuildManifest.Path, "images/evidence/")
	for _, entry := range published.Evidence {
		assert.Contains(t, entry.Path, "images/evidence/"+entry.SHA256+"/")
		assert.NotContains(t, entry.Path, string(filepath.Separator)+"tmp"+string(filepath.Separator))
	}
}

// TestInspectRejectsUnpublishableHandoffs proves evidence fails closed before storage is contacted.
func TestInspectRejectsUnpublishableHandoffs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*sourceManifest, string)
		kind   failure.Kind
	}{
		{
			name: "failed validation",
			mutate: func(manifest *sourceManifest, _ string) {
				manifest.Result = "fail"
			},
			kind: failure.KindInvalidInput,
		},
		{
			name: "disk digest mismatch",
			mutate: func(manifest *sourceManifest, _ string) {
				replacement := "0"
				if manifest.Artifacts.Disk.SHA256[0] == '0' {
					replacement = "1"
				}
				manifest.Artifacts.Disk.SHA256 = replacement + manifest.Artifacts.Disk.SHA256[1:]
			},
			kind: failure.KindIntegrity,
		},
		{
			name: "metadata binding missing",
			mutate: func(manifest *sourceManifest, _ string) {
				manifest.Artifacts.Metadata = nil
			},
			kind: failure.KindInvalidInput,
		},
		{
			name: "required evidence missing",
			mutate: func(manifest *sourceManifest, _ string) {
				manifest.Evidence = manifest.Evidence[1:]
			},
			kind: failure.KindInvalidInput,
		},
		{
			name: "duplicate role",
			mutate: func(manifest *sourceManifest, _ string) {
				manifest.Evidence[1].Role = manifest.Evidence[0].Role
			},
			kind: failure.KindInvalidInput,
		},
		{
			name: "proof outside manifest directory",
			mutate: func(manifest *sourceManifest, directory string) {
				outside := filepath.Join(filepath.Dir(directory), "outside.json")
				require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
				manifest.Evidence[0].Path = outside
				manifest.Evidence[0].SHA256 = object.DigestBytes([]byte("outside")).String()
			},
			kind: failure.KindInvalidInput,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifestPath, vm := writePassingManifest(t, false)
			manifest := readSourceManifest(t, manifestPath)
			test.mutate(&manifest, filepath.Dir(manifestPath))
			writeSourceManifest(t, manifestPath, manifest)

			_, err := Inspect(manifestPath, vm)
			require.Error(t, err)
			assert.Equal(t, test.kind, failure.KindOf(err))
		})
	}
}

// TestInspectRejectsPartialSignedEvidence proves a signed handoff cannot omit one bundle.
func TestInspectRejectsPartialSignedEvidence(t *testing.T) {
	t.Parallel()
	manifestPath, vm := writePassingManifest(t, true)
	manifest := readSourceManifest(t, manifestPath)
	manifest.Evidence = manifest.Evidence[:len(manifest.Evidence)-1]
	writeSourceManifest(t, manifestPath, manifest)

	_, err := Inspect(manifestPath, vm)
	require.Error(t, err)
	assert.Equal(t, failure.KindInvalidInput, failure.KindOf(err))
}

// writePassingManifest constructs one complete version-1 handoff fixture.
func writePassingManifest(t testing.TB, includeOptional bool) (string, image.VMImage) {
	t.Helper()
	directory := t.TempDir()
	metadataPath, diskPath := testfixture.WriteSplitVM(t, directory, testfixture.DefaultVMOptions())
	vm, err := image.Inspect(metadataPath, diskPath)
	require.NoError(t, err)
	evidenceDirectory := filepath.Join(directory, "evidence")
	require.NoError(t, os.Mkdir(evidenceDirectory, 0o700))

	// sourceSpec describes one fixture proof role and media type.
	type sourceSpec struct {
		role      string
		mediaType string
	}
	sources := []sourceSpec{
		{"checksums", "text/plain"},
		{"sbom", "application/spdx+json"},
		{"vulnerability-report", "application/json"},
		{"validation-report", "application/json"},
		{"validation-predicate", "application/vnd.in-toto+json"},
	}
	if includeOptional {
		sources = append(sources,
			sourceSpec{"provenance-attestation", "application/vnd.dev.sigstore.bundle+json"},
			sourceSpec{"sbom-attestation", "application/vnd.dev.sigstore.bundle+json"},
			sourceSpec{"validation-attestation", "application/vnd.dev.sigstore.bundle+json"},
		)
	}
	evidenceEntries := make([]sourceEvidence, 0, len(sources))
	for _, source := range sources {
		body := []byte(source.role + "-body")
		path := filepath.Join(evidenceDirectory, source.role+".json")
		require.NoError(t, os.WriteFile(path, body, 0o600))
		evidenceEntries = append(evidenceEntries, sourceEvidence{
			Role: source.role, Path: path, SHA256: object.DigestBytes(body).String(), MediaType: source.mediaType,
		})
	}
	manifest := sourceManifest{
		SchemaVersion: "1",
		Result:        "pass",
		Artifacts: sourceArtifacts{
			Disk:     sourceArtifact{Path: diskPath, SHA256: vm.Disk().SHA256().String()},
			Metadata: &sourceArtifact{Path: metadataPath, SHA256: vm.Metadata().SHA256().String()},
		},
		Evidence: evidenceEntries,
	}
	if includeOptional {
		attestationURL := "https://github.com/meigma/example/attestations/1"
		manifest.AttestationURL = &attestationURL
		body := []byte(`{"builder":"test"}`)
		path := filepath.Join(directory, "build-manifest.json")
		require.NoError(t, os.WriteFile(path, body, 0o600))
		manifest.Artifacts.BuildManifest = &sourceArtifact{
			Path: path, SHA256: object.DigestBytes(body).String(),
		}
	}
	manifestPath := filepath.Join(evidenceDirectory, "evidence-manifest.json")
	writeSourceManifest(t, manifestPath, manifest)
	return manifestPath, vm
}

// writeSourceManifest renders one source fixture to path.
func writeSourceManifest(t testing.TB, path string, manifest sourceManifest) {
	t.Helper()
	body, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
}

// readSourceManifest decodes one mutable test fixture.
func readSourceManifest(t testing.TB, path string) sourceManifest {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var manifest sourceManifest
	require.NoError(t, json.Unmarshal(body, &manifest))
	return manifest
}

// readArtifact consumes one prepared object for assertions.
func readArtifact(t testing.TB, artifact Artifact) []byte {
	t.Helper()
	reader, err := artifact.Open()
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	return body
}
