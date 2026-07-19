package s3store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/failure"
)

// TestClientOptionsFromEnvironmentAcceptsLocalEndpoint proves the hidden CLI rehearsal hook.
func TestClientOptionsFromEnvironmentAcceptsLocalEndpoint(t *testing.T) {
	t.Setenv(testEndpointEnvironment, "http://127.0.0.1:9000")
	t.Setenv(testPathStyleEnvironment, "true")
	t.Setenv(testThresholdEnvironment, "134217728")

	options, err := clientOptionsFromEnvironment()
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:9000", options.baseEndpoint)
	assert.True(t, options.pathStyle)
	assert.EqualValues(t, 134217728, options.uploadThreshold)
}

// TestClientOptionsFromEnvironmentRejectsUnsafeValues proves malformed hooks fail closed.
func TestClientOptionsFromEnvironmentRejectsUnsafeValues(t *testing.T) {
	t.Run("path style without endpoint", func(t *testing.T) {
		t.Setenv(testPathStyleEnvironment, "true")

		_, err := clientOptionsFromEnvironment()
		require.Error(t, err)
		assert.Equal(t, failure.KindInvalidInput, failure.KindOf(err))
	})

	t.Run("non-HTTP endpoint", func(t *testing.T) {
		t.Setenv(testEndpointEnvironment, "file:///tmp/s3")

		_, err := clientOptionsFromEnvironment()
		require.Error(t, err)
		assert.Equal(t, failure.KindInvalidInput, failure.KindOf(err))
	})

	t.Run("invalid upload threshold", func(t *testing.T) {
		t.Setenv(testEndpointEnvironment, "http://127.0.0.1:9000")
		t.Setenv(testThresholdEnvironment, "0")

		_, err := clientOptionsFromEnvironment()
		require.Error(t, err)
		assert.Equal(t, failure.KindInvalidInput, failure.KindOf(err))
	})
}
