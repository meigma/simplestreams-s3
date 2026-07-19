package httpserver

import (
	"context"
	"sync"
	"time"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/proxy"
)

const unavailableReason = "s3_unavailable"

// Readiness tracks cached catalog-probe state without making HTTP handlers wait on S3.
type Readiness struct {
	probe     func(context.Context) error
	interval  time.Duration
	timeout   time.Duration
	staleness time.Duration
	metrics   proxy.Metrics

	mu          sync.RWMutex
	lastSuccess time.Time
	reason      string
	draining    bool
}

// NewReadiness constructs a cached readiness checker for one authenticated catalog probe.
func NewReadiness(
	probe func(context.Context) error,
	interval time.Duration,
	timeout time.Duration,
	staleness time.Duration,
) *Readiness {
	return NewReadinessWithMetrics(probe, interval, timeout, staleness, proxy.NoopMetrics())
}

// NewReadinessWithMetrics constructs a cached readiness checker with optional metric emission.
func NewReadinessWithMetrics(
	probe func(context.Context) error,
	interval time.Duration,
	timeout time.Duration,
	staleness time.Duration,
	metrics proxy.Metrics,
) *Readiness {
	if metrics == nil {
		metrics = proxy.NoopMetrics()
	}
	return &Readiness{
		probe: probe, interval: interval, timeout: timeout, staleness: staleness, metrics: metrics, reason: "starting",
	}
}

// Start runs immediate and periodic catalog probes until ctx is canceled.
func (readiness *Readiness) Start(ctx context.Context) {
	if readiness == nil || readiness.probe == nil {
		return
	}
	readiness.check(ctx)
	ticker := time.NewTicker(readiness.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			readiness.check(ctx)
		}
	}
}

// Status returns the cached readiness state and only stable public reason codes.
func (readiness *Readiness) Status(now time.Time) (bool, string) {
	if readiness == nil {
		return false, "starting"
	}
	readiness.mu.RLock()
	defer readiness.mu.RUnlock()
	if readiness.draining {
		return false, "draining"
	}
	if !readiness.lastSuccess.IsZero() && now.Sub(readiness.lastSuccess) <= readiness.staleness {
		return true, ""
	}
	return false, readiness.reason
}

// SetDraining makes readiness unavailable immediately during graceful shutdown.
func (readiness *Readiness) SetDraining() {
	if readiness == nil {
		return
	}
	readiness.mu.Lock()
	readiness.draining = true
	readiness.reason = "draining"
	readiness.mu.Unlock()
	readiness.metrics.RecordReadiness(context.Background(), false, "draining")
}

// check runs one bounded probe and updates only cached state.
func (readiness *Readiness) check(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, readiness.timeout)
	err := readiness.probe(ctx)
	cancel()
	readiness.mu.Lock()
	if err == nil {
		readiness.lastSuccess = time.Now()
		readiness.reason = ""
		readiness.mu.Unlock()
		readiness.metrics.RecordReadiness(parent, true, "")
		return
	}
	switch failure.KindOf(err) {
	case failure.KindNotFound:
		readiness.reason = "catalog_missing"
	case failure.KindUnauthorized:
		readiness.reason = "s3_misconfigured"
	case failure.KindInvalidInput,
		failure.KindUnsupportedImage,
		failure.KindAlreadyExists,
		failure.KindIntegrity,
		failure.KindContentConflict,
		failure.KindCatalogConflict,
		failure.KindPrecondition,
		failure.KindNotModified,
		failure.KindRangeNotSatisfiable,
		failure.KindUnavailable,
		failure.KindDeadline,
		failure.KindCanceled,
		failure.KindInternal:
		readiness.reason = unavailableReason
	}
	reason := readiness.reason
	readiness.mu.Unlock()
	readiness.metrics.RecordReadiness(parent, false, reason)
}
