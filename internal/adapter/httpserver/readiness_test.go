package httpserver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/meigma/simplestreams-s3/internal/failure"
)

// TestReadinessCachesSuccessAndMapsStableFailureReasons proves handlers read only bounded cached state.
func TestReadinessCachesSuccessAndMapsStableFailureReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		probeError error
		wantReason string
	}{
		{
			name:       "missing catalog",
			probeError: failure.New(failure.KindNotFound, "probe", "missing"),
			wantReason: "catalog_missing",
		},
		{
			name:       "misconfigured credentials",
			probeError: failure.New(failure.KindUnauthorized, "probe", "denied"),
			wantReason: "s3_misconfigured",
		},
		{
			name:       "transient s3 failure",
			probeError: failure.New(failure.KindUnavailable, "probe", "down"),
			wantReason: "s3_unavailable",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			readiness := NewReadiness(
				func(context.Context) error { return testCase.probeError },
				time.Second,
				time.Second,
				time.Second,
			)
			readiness.check(context.Background())

			ready, reason := readiness.Status(time.Now())

			assert.False(t, ready)
			assert.Equal(t, testCase.wantReason, reason)
		})
	}
}

// TestReadinessRemainsReadyUntilTheConfiguredSuccessStalenessWindowExpires proves one failed check does not flap readiness.
func TestReadinessRemainsReadyUntilTheConfiguredSuccessStalenessWindowExpires(t *testing.T) {
	t.Parallel()
	readiness := NewReadiness(func(context.Context) error { return nil }, time.Second, time.Second, time.Second)
	readiness.check(context.Background())
	readiness.mu.Lock()
	readiness.reason = "s3_unavailable"
	readiness.mu.Unlock()

	ready, reason := readiness.Status(time.Now())

	assert.True(t, ready)
	assert.Empty(t, reason)
	readiness.SetDraining()
	ready, reason = readiness.Status(time.Now())
	assert.False(t, ready)
	assert.Equal(t, "draining", reason)
}
