package proxy

import (
	"context"
	"time"
)

// Metrics records Phase 4 proxy emission points without coupling request behavior to an exporter.
type Metrics interface {
	// RecordRequest observes one completed HTTP request.
	RecordRequest(context.Context, RequestMetric)
	// RecordRejectedStream observes a request rejected by the local stream limit.
	RecordRejectedStream(context.Context)
	// RecordActiveStreams observes the current number of active S3-backed streams.
	RecordActiveStreams(context.Context, int)
	// RecordIncompleteStream observes an upstream or downstream stream interruption.
	RecordIncompleteStream(context.Context)
	// RecordReadiness observes the latest cached readiness result and stable reason.
	RecordReadiness(context.Context, bool, string)
	// RecordS3Request observes one proxy S3 operation without object identity attributes.
	RecordS3Request(context.Context, S3Metric)
}

// RequestMetric is the bounded data emitted for one proxied HTTP request.
type RequestMetric struct {
	// Method is the request method.
	Method string
	// Route is the low-cardinality route class.
	Route string
	// StatusCode is the final HTTP status code.
	StatusCode int
	// BodySize is the number of response body bytes sent.
	BodySize int64
	// Duration is the complete server request duration.
	Duration time.Duration
	// Scheme is the low-cardinality request URL scheme.
	Scheme string
	// ProtocolVersion is the negotiated HTTP protocol version.
	ProtocolVersion string
	// ErrorKind is the stable failure kind when unsuccessful.
	ErrorKind string
}

// S3Metric is the bounded data emitted for one authenticated proxy S3 operation.
type S3Metric struct {
	// Operation is the fixed AWS API operation name.
	Operation string
	// Outcome is success or error.
	Outcome string
	// ErrorKind is the stable application failure kind when unsuccessful.
	ErrorKind string
	// Duration is the complete SDK operation duration including retries.
	Duration time.Duration
	// Retries is the number of attempts after the first request attempt.
	Retries int64
	// Transferred is the response size associated with the operation.
	Transferred int64
}

// NoopMetrics constructs metrics that preserve all call sites without exporting data.
func NoopMetrics() Metrics { return noopMetrics{} }

// noopMetrics deliberately discards Phase 4 emission points until Phase 5 wires OTLP.
type noopMetrics struct{}

// RecordRequest discards one request observation.
func (noopMetrics) RecordRequest(context.Context, RequestMetric) {}

// RecordRejectedStream discards one saturation observation.
func (noopMetrics) RecordRejectedStream(context.Context) {}

// RecordActiveStreams discards one active-stream observation.
func (noopMetrics) RecordActiveStreams(context.Context, int) {}

// RecordIncompleteStream discards one incomplete-stream observation.
func (noopMetrics) RecordIncompleteStream(context.Context) {}

// RecordReadiness discards one readiness observation.
func (noopMetrics) RecordReadiness(context.Context, bool, string) {}

// RecordS3Request discards one proxy S3 observation.
func (noopMetrics) RecordS3Request(context.Context, S3Metric) {}
