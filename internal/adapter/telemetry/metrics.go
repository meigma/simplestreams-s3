// Package telemetry adapts the proxy metrics port to optional OTLP/HTTP export.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/meigma/simplestreams-s3/internal/proxy"
)

const (
	instrumentationName = "github.com/meigma/simplestreams-s3/internal/adapter/telemetry"
	warningInterval     = time.Minute
)

// Options configures one optional OTLP/HTTP metrics runtime.
type Options struct {
	// Endpoint is the collector host and port; empty disables export.
	Endpoint string
	// Interval controls periodic metric collection and export.
	Interval time.Duration
	// Timeout bounds individual exports.
	Timeout time.Duration
	// Insecure permits cleartext transport after configuration validation.
	Insecure bool
	// ServiceVersion identifies the running binary as a resource attribute.
	ServiceVersion string
	// Logger receives sanitized, rate-limited exporter warnings.
	Logger *slog.Logger
}

// Runtime owns the optional meter provider and proxy metrics adapter.
type Runtime struct {
	metrics  proxy.Metrics
	provider *metric.MeterProvider
}

// metricSet implements the proxy metrics port with fixed instruments and attributes.
type metricSet struct {
	requestDuration   otelmetric.Float64Histogram
	responseBody      otelmetric.Int64Histogram
	s3Duration        otelmetric.Float64Histogram
	s3Requests        otelmetric.Int64Counter
	s3Retries         otelmetric.Int64Counter
	s3Transferred     otelmetric.Int64Counter
	streamsRejected   otelmetric.Int64Counter
	streamsIncomplete otelmetric.Int64Counter
	activeStreams     atomic.Int64
	readiness         atomic.Int64
	readinessMu       sync.RWMutex
	readinessReason   string
}

// warningExporter decorates OTLP export with sanitized rate-limited warnings.
type warningExporter struct {
	metric.Exporter

	logger      *slog.Logger
	now         func() time.Time
	mu          sync.Mutex
	lastWarning time.Time
}

// NewRuntime constructs disabled no-op metrics or a periodic OTLP/HTTP exporter.
func NewRuntime(ctx context.Context, options Options) (*Runtime, error) {
	if options.Endpoint == "" {
		return &Runtime{metrics: proxy.NoopMetrics()}, nil
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	logger = logger.With("component", "telemetry")
	exporterOptions := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(options.Endpoint),
		otlpmetrichttp.WithTimeout(options.Timeout),
	}
	if options.Insecure {
		exporterOptions = append(exporterOptions, otlpmetrichttp.WithInsecure())
	}
	exporter, err := otlpmetrichttp.New(ctx, exporterOptions...)
	if err != nil {
		return nil, err
	}
	wrapper := &warningExporter{Exporter: exporter, logger: logger, now: time.Now}
	reader := metric.NewPeriodicReader(
		wrapper,
		metric.WithInterval(options.Interval),
		metric.WithTimeout(options.Timeout),
	)
	provider := metric.NewMeterProvider(
		metric.WithReader(reader),
		metric.WithResource(resource.NewSchemaless(
			attribute.String("service.name", "simplestreams-s3"),
			attribute.String("service.version", options.ServiceVersion),
		)),
	)
	metrics, err := newMetricSet(provider.Meter(instrumentationName))
	if err != nil {
		shutdownContext, cancel := context.WithTimeout(context.Background(), options.Timeout)
		defer cancel()
		return nil, errors.Join(err, provider.Shutdown(shutdownContext))
	}
	return &Runtime{metrics: metrics, provider: provider}, nil
}

// Metrics returns the proxy-facing metric emission port.
func (runtime *Runtime) Metrics() proxy.Metrics {
	if runtime == nil || runtime.metrics == nil {
		return proxy.NoopMetrics()
	}
	return runtime.metrics
}

// ForceFlush exports pending observations within the caller's context bound.
func (runtime *Runtime) ForceFlush(ctx context.Context) error {
	if runtime == nil || runtime.provider == nil {
		return nil
	}
	return runtime.provider.ForceFlush(ctx)
}

// Shutdown flushes pending observations and releases exporter resources.
func (runtime *Runtime) Shutdown(ctx context.Context) error {
	if runtime == nil || runtime.provider == nil {
		return nil
	}
	return runtime.provider.Shutdown(ctx)
}

// newMetricSet creates the closed Phase 5 instrument set.
func newMetricSet(meter otelmetric.Meter) (*metricSet, error) {
	metrics := &metricSet{}
	var errs []error
	metrics.requestDuration, errs = float64Histogram(
		meter,
		"http.server.request.duration",
		"Duration of inbound HTTP requests.",
		"s",
		errs,
	)
	metrics.responseBody, errs = int64Histogram(
		meter,
		"http.server.response.body.size",
		"Size of completed HTTP response bodies.",
		"By",
		errs,
	)
	metrics.s3Duration, errs = float64Histogram(
		meter,
		"simplestreams_s3.s3.request.duration",
		"Duration of authenticated S3 operations.",
		"s",
		errs,
	)
	metrics.s3Requests, errs = int64Counter(
		meter,
		"simplestreams_s3.s3.requests",
		"Authenticated S3 operations.",
		"{request}",
		errs,
	)
	metrics.s3Retries, errs = int64Counter(
		meter,
		"simplestreams_s3.s3.retries",
		"S3 attempts after the first request attempt.",
		"{retry}",
		errs,
	)
	metrics.s3Transferred, errs = int64Counter(
		meter,
		"simplestreams_s3.s3.transferred",
		"Bytes associated with successful S3 operations.",
		"By",
		errs,
	)
	metrics.streamsRejected, errs = int64Counter(
		meter,
		"simplestreams_s3.streams.rejected",
		"Object streams rejected by the local concurrency bound.",
		"{stream}",
		errs,
	)
	metrics.streamsIncomplete, errs = int64Counter(
		meter,
		"simplestreams_s3.streams.incomplete",
		"Object streams terminated before completion.",
		"{stream}",
		errs,
	)
	_, err := meter.Int64ObservableUpDownCounter(
		"http.server.active_requests",
		otelmetric.WithDescription("Active S3-backed HTTP requests."),
		otelmetric.WithUnit("{request}"),
		otelmetric.WithInt64Callback(func(_ context.Context, observer otelmetric.Int64Observer) error {
			observer.Observe(metrics.activeStreams.Load())
			return nil
		}),
	)
	errs = appendError(errs, err)
	_, err = meter.Int64ObservableGauge(
		"simplestreams_s3.readiness",
		otelmetric.WithDescription("Latest cached S3 catalog readiness state."),
		otelmetric.WithUnit("1"),
		otelmetric.WithInt64Callback(func(_ context.Context, observer otelmetric.Int64Observer) error {
			value, attributes := metrics.readinessObservation()
			observer.Observe(value, otelmetric.WithAttributes(attributes...))
			return nil
		}),
	)
	errs = appendError(errs, err)
	return metrics, errors.Join(errs...)
}

// float64Histogram constructs one fixed histogram and accumulates construction errors.
func float64Histogram(
	meter otelmetric.Meter,
	name string,
	description string,
	unit string,
	errs []error,
) (otelmetric.Float64Histogram, []error) {
	instrument, err := meter.Float64Histogram(
		name,
		otelmetric.WithDescription(description),
		otelmetric.WithUnit(unit),
	)
	return instrument, appendError(errs, err)
}

// int64Histogram constructs one fixed histogram and accumulates construction errors.
func int64Histogram(
	meter otelmetric.Meter,
	name string,
	description string,
	unit string,
	errs []error,
) (otelmetric.Int64Histogram, []error) {
	instrument, err := meter.Int64Histogram(
		name,
		otelmetric.WithDescription(description),
		otelmetric.WithUnit(unit),
	)
	return instrument, appendError(errs, err)
}

// int64Counter constructs one fixed counter and accumulates construction errors.
func int64Counter(
	meter otelmetric.Meter,
	name string,
	description string,
	unit string,
	errs []error,
) (otelmetric.Int64Counter, []error) {
	instrument, err := meter.Int64Counter(
		name,
		otelmetric.WithDescription(description),
		otelmetric.WithUnit(unit),
	)
	return instrument, appendError(errs, err)
}

// appendError retains only non-nil instrument-construction failures.
func appendError(errs []error, err error) []error {
	if err != nil {
		return append(errs, err)
	}
	return errs
}

// RecordRequest observes one completed HTTP request with semantic-convention attributes.
func (metrics *metricSet) RecordRequest(ctx context.Context, input proxy.RequestMetric) {
	attributes := []attribute.KeyValue{
		attribute.String("http.request.method", input.Method),
		attribute.String("http.route", input.Route),
		attribute.Int("http.response.status_code", input.StatusCode),
		attribute.String("url.scheme", input.Scheme),
		attribute.String("network.protocol.name", "http"),
		attribute.String("network.protocol.version", input.ProtocolVersion),
	}
	options := otelmetric.WithAttributes(attributes...)
	metrics.requestDuration.Record(ctx, input.Duration.Seconds(), options)
	metrics.responseBody.Record(ctx, input.BodySize, options)
}

// RecordRejectedStream observes one local concurrency rejection.
func (metrics *metricSet) RecordRejectedStream(ctx context.Context) {
	metrics.streamsRejected.Add(ctx, 1)
}

// RecordActiveStreams stores the latest active S3-backed request count.
func (metrics *metricSet) RecordActiveStreams(_ context.Context, count int) {
	metrics.activeStreams.Store(int64(count))
}

// RecordIncompleteStream observes one response terminated after headers were sent.
func (metrics *metricSet) RecordIncompleteStream(ctx context.Context) {
	metrics.streamsIncomplete.Add(ctx, 1)
}

// RecordReadiness stores the latest bounded readiness state and reason.
func (metrics *metricSet) RecordReadiness(_ context.Context, ready bool, reason string) {
	value := int64(0)
	if ready {
		value = 1
	}
	metrics.readiness.Store(value)
	metrics.readinessMu.Lock()
	metrics.readinessReason = reason
	metrics.readinessMu.Unlock()
}

// RecordS3Request observes one bounded authenticated S3 operation.
func (metrics *metricSet) RecordS3Request(ctx context.Context, input proxy.S3Metric) {
	attributes := []attribute.KeyValue{
		attribute.String("aws.operation", input.Operation),
		attribute.String("outcome", input.Outcome),
	}
	if input.ErrorKind != "" {
		attributes = append(attributes, attribute.String("error.kind", input.ErrorKind))
	}
	options := otelmetric.WithAttributes(attributes...)
	metrics.s3Requests.Add(ctx, 1, options)
	metrics.s3Duration.Record(ctx, input.Duration.Seconds(), options)
	if input.Retries > 0 {
		metrics.s3Retries.Add(ctx, input.Retries, options)
	}
	if input.Transferred > 0 {
		metrics.s3Transferred.Add(ctx, input.Transferred, options)
	}
}

// readinessObservation returns one gauge value with only bounded attributes.
func (metrics *metricSet) readinessObservation() (int64, []attribute.KeyValue) {
	metrics.readinessMu.RLock()
	reason := metrics.readinessReason
	metrics.readinessMu.RUnlock()
	outcome := "not_ready"
	if metrics.readiness.Load() == 1 {
		outcome = "ready"
	}
	attributes := []attribute.KeyValue{attribute.String("outcome", outcome)}
	if reason != "" {
		attributes = append(attributes, attribute.String("error.kind", reason))
	}
	return metrics.readiness.Load(), attributes
}

// Temporality delegates aggregation temporality selection to the OTLP exporter.
func (exporter *warningExporter) Temporality(kind metric.InstrumentKind) metricdata.Temporality {
	return exporter.Exporter.Temporality(kind)
}

// Aggregation delegates aggregation selection to the OTLP exporter.
func (exporter *warningExporter) Aggregation(kind metric.InstrumentKind) metric.Aggregation {
	return exporter.Exporter.Aggregation(kind)
}

// Export delegates one OTLP request and records a sanitized warning on failure.
func (exporter *warningExporter) Export(ctx context.Context, data *metricdata.ResourceMetrics) error {
	err := exporter.Exporter.Export(ctx, data)
	if err != nil {
		exporter.warn()
	}
	return err
}

// ForceFlush delegates bounded exporter flushing.
func (exporter *warningExporter) ForceFlush(ctx context.Context) error {
	return exporter.Exporter.ForceFlush(ctx)
}

// Shutdown delegates bounded exporter shutdown.
func (exporter *warningExporter) Shutdown(ctx context.Context) error {
	return exporter.Exporter.Shutdown(ctx)
}

// warn emits at most one exporter-failure warning per fixed interval.
func (exporter *warningExporter) warn() {
	now := exporter.now()
	exporter.mu.Lock()
	if !exporter.lastWarning.IsZero() && now.Sub(exporter.lastWarning) < warningInterval {
		exporter.mu.Unlock()
		return
	}
	exporter.lastWarning = now
	exporter.mu.Unlock()
	exporter.logger.Warn("metric export failed", "error.kind", "export_failed")
}
