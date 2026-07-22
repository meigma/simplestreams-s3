//go:build integration

package integration_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/adapter/httpserver"
	"github.com/meigma/simplestreams-s3/internal/image"
	"github.com/meigma/simplestreams-s3/internal/proxy"
	"github.com/meigma/simplestreams-s3/internal/publish"
	"github.com/meigma/simplestreams-s3/internal/testfixture"
)

const (
	incusAcceptanceEnvironment  = "SIMPLESTREAMS_S3_INCUS_ACCEPTANCE"
	incusCertificateEnvironment = "SIMPLESTREAMS_S3_INCUS_TLS_CERT"
	incusKeyEnvironment         = "SIMPLESTREAMS_S3_INCUS_TLS_KEY"
	incusSudoEnvironment        = "SIMPLESTREAMS_S3_INCUS_SUDO"
)

// incusImage describes the bounded CLI fields asserted by functional acceptance.
type incusImage struct {
	Fingerprint string       `json:"fingerprint"`
	Aliases     []incusAlias `json:"aliases"`
}

// incusAlias describes one image alias returned by the Incus CLI.
type incusAlias struct {
	Name string `json:"name"`
}

// TestIncusAcceptance proves publish, trusted HTTPS listing, import, and exact fingerprint through a real Incus client.
func TestIncusAcceptance(t *testing.T) {
	if os.Getenv(incusAcceptanceEnvironment) == "" {
		t.Skip("set SIMPLESTREAMS_S3_INCUS_ACCEPTANCE to run the Incus functional gate")
	}
	certificatePath := os.Getenv(incusCertificateEnvironment)
	keyPath := os.Getenv(incusKeyEnvironment)
	if certificatePath == "" || keyPath == "" {
		t.Fatal("SIMPLESTREAMS_S3_INCUS_TLS_CERT and SIMPLESTREAMS_S3_INCUS_TLS_KEY are required")
	}
	certificate, err := tls.LoadX509KeyPair(certificatePath, keyPath)
	require.NoError(t, err)
	scenario := newMinIOScenario(t)
	options := testfixture.DefaultVMOptions()
	switch runtime.GOARCH {
	case "amd64":
		options.Architecture = "x86_64"
		options.PropertyArchitecture = "amd64"
	case "arm64":
	default:
		t.Fatalf("Incus acceptance fixture does not support runner architecture %q", runtime.GOARCH)
	}
	metadataPath, diskPath := testfixture.WriteSplitVM(t, t.TempDir(), options)
	evidenceManifestPath := testfixture.WriteEvidenceManifest(
		t,
		t.TempDir(),
		metadataPath,
		diskPath,
		"pass",
	)
	vm, err := image.Inspect(metadataPath, diskPath)
	require.NoError(t, err)
	expectedFingerprint, err := vm.Fingerprint()
	require.NoError(t, err)
	_, err = scenario.publisher.Publish(t.Context(), publish.Request{
		MetadataPath:         metadataPath,
		DiskPath:             diskPath,
		EvidenceManifestPath: evidenceManifestPath,
	})
	require.NoError(t, err)

	server := httptest.NewUnstartedServer(httpserver.NewHandler(proxy.NewService(scenario.store, "")))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	remoteName := "phase5-" + suffix
	localAlias := "phase5-import-" + suffix
	t.Cleanup(func() {
		_, _ = runIncus(context.Background(), "image", "delete", localAlias)
		_, _ = runIncus(context.Background(), "remote", "remove", remoteName)
	})

	_, err = runIncus(t.Context(), "remote", "add", remoteName, server.URL, "--protocol", "simplestreams")
	require.NoError(t, err)
	listed := listIncusImages(t, remoteName+":")
	assertIncusImage(t, listed, "alpinelinux/3.22/cloud", expectedFingerprint.String())

	_, err = runIncus(
		t.Context(),
		"image",
		"copy",
		remoteName+":alpinelinux/3.22/cloud",
		"local:",
		"--vm",
		"--alias",
		localAlias,
	)
	require.NoError(t, err)
	imported := listIncusImages(t, "local:", localAlias)
	require.Len(t, imported, 1)
	assert.Equal(t, expectedFingerprint.String(), imported[0].Fingerprint)
}

// listIncusImages returns the structured images from one bounded CLI query.
func listIncusImages(t *testing.T, arguments ...string) []incusImage {
	t.Helper()
	output, err := runIncus(t.Context(), append([]string{"image", "list"}, append(arguments, "--format", "json")...)...)
	require.NoError(t, err)
	var images []incusImage
	require.NoError(t, json.Unmarshal(output, &images))
	return images
}

// assertIncusImage proves one alias resolves to the expected full fingerprint.
func assertIncusImage(t *testing.T, images []incusImage, alias string, fingerprint string) {
	t.Helper()
	for _, candidate := range images {
		for _, candidateAlias := range candidate.Aliases {
			if candidateAlias.Name == alias {
				assert.Equal(t, fingerprint, candidate.Fingerprint)
				return
			}
		}
	}
	t.Errorf("Incus did not list expected alias %q", alias)
}

// runIncus executes one non-interactive Incus CLI command and preserves diagnostic output.
func runIncus(ctx context.Context, arguments ...string) ([]byte, error) {
	commandName := "incus"
	commandArguments := arguments
	if os.Getenv(incusSudoEnvironment) != "" {
		commandName = "sudo"
		commandArguments = append([]string{"-n", "incus"}, arguments...)
	}
	command := exec.CommandContext(ctx, commandName, commandArguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("incus command failed: %w: %s", err, output)
	}
	return output, nil
}
