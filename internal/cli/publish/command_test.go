package publish

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/config"
	application "github.com/meigma/simplestreams-s3/internal/publish"
)

// TestCommandAppliesFlagEnvironmentFileDefaultPrecedence proves the stable configuration order.
func TestCommandAppliesFlagEnvironmentFileDefaultPrecedence(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("s3:\n  bucket: file-bucket\n"), 0o600))
	t.Setenv("SIMPLESTREAMS_S3_BUCKET", "environment-bucket")
	var captured config.Publish
	command := NewCommand(func(
		_ context.Context,
		runtime config.Publish,
		_ application.Request,
	) (application.Result, error) {
		captured = runtime
		return application.Result{}, nil
	})
	command.SetArgs([]string{
		"--config", configPath,
		"--s3-bucket", "flag-bucket",
		"metadata.tar.xz",
		"disk.qcow2",
	})

	require.NoError(t, command.ExecuteContext(context.Background()))
	assert.Equal(t, "flag-bucket", captured.S3.Bucket.String())
	assert.Equal(t, config.DefaultS3MaxAttempts, captured.S3.MaxAttempts)
	assert.Equal(t, config.DefaultPublishTimeout, captured.Timeout)
	assert.Equal(t, config.DefaultCatalogAttempts, captured.CatalogAttempts)
}

// TestCommandLoadsEnvironmentOnlyValuesAndConfigSelector proves explicit env registration unmarshals.
func TestCommandLoadsEnvironmentOnlyValuesAndConfigSelector(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("publish:\n  release_title: From file\n"), 0o600))
	t.Setenv("SIMPLESTREAMS_S3_CONFIG", configPath)
	t.Setenv("SIMPLESTREAMS_S3_BUCKET", "environment-bucket")
	t.Setenv("SIMPLESTREAMS_S3_PREFIX", "private/incus")
	t.Setenv("SIMPLESTREAMS_S3_CATALOG_ATTEMPTS", "7")
	var captured config.Publish
	command := NewCommand(func(
		_ context.Context,
		runtime config.Publish,
		_ application.Request,
	) (application.Result, error) {
		captured = runtime
		return application.Result{}, nil
	})
	command.SetArgs([]string{"metadata.tar.xz", "disk.qcow2"})

	require.NoError(t, command.ExecuteContext(context.Background()))
	assert.Equal(t, "environment-bucket", captured.S3.Bucket.String())
	assert.Equal(t, "private/incus", captured.S3.Prefix.String())
	assert.Equal(t, "From file", captured.ReleaseTitle)
	assert.Equal(t, 7, captured.CatalogAttempts)
}

// TestCommandRejectsUnknownYAMLKeys proves selected configuration files are structurally strict.
func TestCommandRejectsUnknownYAMLKeys(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("s3:\n  bucket: private-bucket\nunknown: true\n"), 0o600))
	called := false
	command := NewCommand(func(
		_ context.Context,
		_ config.Publish,
		_ application.Request,
	) (application.Result, error) {
		called = true
		return application.Result{}, nil
	})
	command.SetArgs([]string{"--config", configPath, "metadata.tar.xz", "disk.qcow2"})

	err := command.ExecuteContext(context.Background())
	require.Error(t, err)
	assert.False(t, called)
	assert.Contains(t, err.Error(), "decode configuration")
}
