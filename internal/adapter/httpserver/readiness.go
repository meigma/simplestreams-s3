package httpserver

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/proxy"
)

const (
	drainingReason    = "draining"
	unavailableReason = "s3_unavailable"
)

// Readiness tracks cached catalog-probe state without making HTTP handlers wait on S3.
type Readiness struct {
	probe     func(context.Context) error
	interval  time.Duration
	timeout   time.Duration
	staleness time.Duration
	metrics   proxy.Metrics
	logger    *slog.Logger

	mu          sync.RWMutex
	lastSuccess time.Time
	reason      string
	draining    bool

	transitionMu     sync.Mutex
	transitionReady  bool
	transitionReason string
}

// NewReadiness constructs a cached readiness checker for one authenticated catalog probe.
func NewReadiness(
	probe func(context.Context) error,
	interval time.Duration,
	timeout time.Duration,
	staleness time.Duration,
) *Readiness {
	return NewReadinessWithObservers(probe, interval, timeout, staleness, proxy.NoopMetrics(), nil)
}

// NewReadinessWithMetrics constructs a cached readiness checker with optional metric emission.
func NewReadinessWithMetrics(
	probe func(context.Context) error,
	interval time.Duration,
	timeout time.Duration,
	staleness time.Duration,
	metrics proxy.Metrics,
) *Readiness {
	return NewReadinessWithObservers(probe, interval, timeout, staleness, metrics, nil)
}

// NewReadinessWithObservers constructs a cached checker with metric and transition-log adapters.
func NewReadinessWithObservers(
	probe func(context.Context) error,
	interval time.Duration,
	timeout time.Duration,
	staleness time.Duration,
	metrics proxy.Metrics,
	logger *slog.Logger,
) *Readiness {
	if metrics == nil {
		metrics = proxy.NoopMetrics()
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Readiness{
		probe: probe, interval: interval, timeout: timeout, staleness: staleness,
		metrics: metrics, logger: logger.With("component", "readiness"),
		reason: "starting", transitionReason: "starting",
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
		return false, drainingReason
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
	readiness.reason = drainingReason
	readiness.mu.Unlock()
	readiness.metrics.RecordReadiness(context.Background(), false, drainingReason)
	readiness.logTransition(false, drainingReason)
}

// check runs one bounded probe and updates only cached state.
func (readiness *Readiness) check(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, readiness.timeout)
	err := readiness.probe(ctx)
	cancel()
	readiness.mu.Lock()
	if err == nil {
		now := time.Now()
		readiness.lastSuccess = now
		readiness.reason = ""
		ready, reason := readiness.stateLocked(now)
		readiness.mu.Unlock()
		readiness.metrics.RecordReadiness(parent, ready, reason)
		readiness.logTransition(ready, reason)
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
	ready, reason := readiness.stateLocked(time.Now())
	readiness.mu.Unlock()
	metricReason := reason
	if ready {
		metricReason = ""
	}
	readiness.metrics.RecordReadiness(parent, ready, metricReason)
	readiness.logTransition(ready, metricReason)
}

// stateLocked returns the externally visible cached state while the caller holds the mutex.
func (readiness *Readiness) stateLocked(now time.Time) (bool, string) {
	if readiness.draining {
		return false, drainingReason
	}
	if !readiness.lastSuccess.IsZero() && now.Sub(readiness.lastSuccess) <= readiness.staleness {
		return true, ""
	}
	return false, readiness.reason
}

// logTransition emits one bounded record only when reported readiness changes.
func (readiness *Readiness) logTransition(ready bool, reason string) {
	readiness.transitionMu.Lock()
	if readiness.transitionReady == ready && readiness.transitionReason == reason {
		readiness.transitionMu.Unlock()
		return
	}
	readiness.transitionReady = ready
	readiness.transitionReason = reason
	readiness.transitionMu.Unlock()
	readiness.logger.Info(
		"readiness transition",
		"event",
		"readiness_transition",
		"ready",
		ready,
		"reason",
		reason,
	)
}
