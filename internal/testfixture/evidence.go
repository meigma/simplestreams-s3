package testfixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/object"
)

// WriteEvidenceManifest writes a complete unsigned attest-vm-image v1 handoff fixture.
func WriteEvidenceManifest(
	t testing.TB,
	directory string,
	metadataPath string,
	diskPath string,
	result string,
) string {
	t.Helper()
	const pathField = "path"
	const sha256Field = "sha256"
	require.NoError(t, os.MkdirAll(directory, 0o700))
	roles := []struct {
		name      string
		mediaType string
	}{
		{"checksums", "text/plain"},
		{"sbom", "application/spdx+json"},
		{"vulnerability-report", "application/json"},
		{"validation-report", "application/json"},
		{"validation-predicate", "application/vnd.in-toto+json"},
	}
	evidence := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		body := []byte(role.name + "-fixture")
		path := filepath.Join(directory, role.name+".json")
		require.NoError(t, os.WriteFile(path, body, 0o600))
		evidence = append(evidence, map[string]any{
			"role":      role.name,
			pathField:   path,
			sha256Field: object.DigestBytes(body).String(),
			"mediaType": role.mediaType,
		})
	}
	manifest := map[string]any{
		"schemaVersion": "1",
		"result":        result,
		"artifacts": map[string]any{
			"disk":          map[string]any{pathField: diskPath, sha256Field: digestFile(t, diskPath)},
			"metadata":      map[string]any{pathField: metadataPath, sha256Field: digestFile(t, metadataPath)},
			"buildManifest": nil,
		},
		"evidence": evidence,
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)
	manifestPath := filepath.Join(directory, "evidence-manifest.json")
	require.NoError(t, os.WriteFile(manifestPath, body, 0o600))
	return manifestPath
}

// digestFile returns one fixture file's lowercase SHA-256.
func digestFile(t testing.TB, path string) string {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	digest, _, err := object.DigestReader(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	return digest.String()
}
