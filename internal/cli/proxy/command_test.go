package proxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/config"
)

// TestCommandAppliesProductionProxyConfigurationPrecedence proves flags override environment, file, and defaults.
func TestCommandAppliesProductionProxyConfigurationPrecedence(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(
		"s3:\n  bucket: file-bucket\nproxy:\n  max_streams: 3\nlogging:\n  level: warn\n",
	), 0o600))
	t.Setenv("SIMPLESTREAMS_S3_BUCKET", "environment-bucket")
	t.Setenv("SIMPLESTREAMS_S3_MAX_STREAMS", "5")
	var captured config.Proxy
	command := NewCommand(func(_ context.Context, runtime config.Proxy) error {
		captured = runtime
		return nil
	})
	command.SetArgs([]string{
		"--config", configPath,
		"--s3-bucket", "flag-bucket",
		"--max-streams", "7",
		"--upstream-idle-timeout", "45s",
		"--log-level", "debug",
	})

	require.NoError(t, command.ExecuteContext(context.Background()))
	assert.Equal(t, "flag-bucket", captured.S3.Bucket.String())
	assert.Equal(t, 7, captured.MaxStreams)
	assert.Equal(t, 45*time.Second, captured.UpstreamIdleTimeout)
	assert.Equal(t, config.DefaultProxyWriteIdleTimeout, captured.WriteIdleTimeout)
	assert.Equal(t, "debug", captured.LogLevel)
}

// TestCommandLoadsEnvironmentOnlyProxyValues proves explicitly registered environment keys populate runtime settings.
func TestCommandLoadsEnvironmentOnlyProxyValues(t *testing.T) {
	t.Setenv("SIMPLESTREAMS_S3_BUCKET", "private-bucket")
	t.Setenv("SIMPLESTREAMS_S3_READINESS_INTERVAL", "12s")
	t.Setenv("SIMPLESTREAMS_S3_READINESS_STALENESS", "36s")
	t.Setenv("SIMPLESTREAMS_S3_LOG_LEVEL", "error")
	var captured config.Proxy
	command := NewCommand(func(_ context.Context, runtime config.Proxy) error {
		captured = runtime
		return nil
	})

	require.NoError(t, command.ExecuteContext(context.Background()))
	assert.Equal(t, 12*time.Second, captured.ReadinessInterval)
	assert.Equal(t, 36*time.Second, captured.ReadinessStaleness)
	assert.Equal(t, "error", captured.LogLevel)
}

// TestCommandRejectsInvalidReadinessAndLoggingSettings proves proxy operational bounds fail before startup.
func TestCommandRejectsInvalidReadinessAndLoggingSettings(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "staleness before interval",
			args: []string{"--readiness-interval", "20s", "--readiness-staleness", "10s"},
		},
		{name: "unknown log level", args: []string{"--log-level", "verbose"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			command := NewCommand(func(context.Context, config.Proxy) error {
				called = true
				return nil
			})
			command.SetArgs(append([]string{"--s3-bucket", "private-bucket"}, testCase.args...))

			err := command.ExecuteContext(context.Background())

			require.Error(t, err)
			assert.False(t, called)
		})
	}
}
