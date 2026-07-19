package proxy

import "context"

// Metrics records Phase 4 proxy emission points without coupling request behavior to an exporter.
type Metrics interface {
	// RecordRequest observes one completed HTTP request.
	RecordRequest(context.Context, RequestMetric)
	// RecordRejectedStream observes a request rejected by the local stream limit.
	RecordRejectedStream(context.Context)
	// RecordIncompleteStream observes an upstream or downstream stream interruption.
	RecordIncompleteStream(context.Context)
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
	// ErrorKind is the stable failure kind when unsuccessful.
	ErrorKind string
}

// NoopMetrics constructs metrics that preserve all call sites without exporting data.
func NoopMetrics() Metrics { return noopMetrics{} }

// noopMetrics deliberately discards Phase 4 emission points until Phase 5 wires OTLP.
type noopMetrics struct{}

// RecordRequest discards one request observation.
func (noopMetrics) RecordRequest(context.Context, RequestMetric) {}

// RecordRejectedStream discards one saturation observation.
func (noopMetrics) RecordRejectedStream(context.Context) {}

// RecordIncompleteStream discards one incomplete-stream observation.
func (noopMetrics) RecordIncompleteStream(context.Context) {}
