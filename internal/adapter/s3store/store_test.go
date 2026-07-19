package s3store

import (
	"context"
	"testing"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/failure"
)

// TestClassifyTranslatesOperationalFailures proves storage details do not leak inward.
func TestClassifyTranslatesOperationalFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		kind failure.Kind
	}{
		{name: "cancellation", err: context.Canceled, kind: failure.KindCanceled},
		{name: "deadline", err: context.DeadlineExceeded, kind: failure.KindDeadline},
		{
			name: "access denied",
			err:  &smithy.GenericAPIError{Code: "AccessDenied", Message: "denied"},
			kind: failure.KindUnauthorized,
		},
		{
			name: "conditional conflict",
			err:  &smithy.GenericAPIError{Code: "ConditionalRequestConflict", Message: "raced"},
			kind: failure.KindPrecondition,
		},
		{
			name: "request timeout",
			err:  &smithy.GenericAPIError{Code: "RequestTimeout", Message: "timed out"},
			kind: failure.KindDeadline,
		},
		{
			name: "throttling",
			err:  &smithy.GenericAPIError{Code: "SlowDown", Message: "throttled"},
			kind: failure.KindUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.kind, failure.KindOf(classify("test operation", test.err)))
		})
	}
}

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
