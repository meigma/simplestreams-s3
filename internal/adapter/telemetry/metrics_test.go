package telemetry

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	collectormetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/proxy"
)

// collectorRequest captures one decoded OTLP/HTTP export attempt.
type collectorRequest struct {
	payload *collectormetricpb.ExportMetricsServiceRequest
	header  http.Header
	err     error
}

// testCollector owns one protobuf OTLP/HTTP receiver used by behavioral tests.
type testCollector struct {
	server   *httptest.Server
	requests chan collectorRequest
}

// newTestCollector starts one loopback OTLP receiver that decodes protobuf payloads.
func newTestCollector(t *testing.T) *testCollector {
	t.Helper()
	requests := make(chan collectorRequest, 8)
	response, err := proto.Marshal(&collectormetricpb.ExportMetricsServiceResponse{})
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		payload := new(collectormetricpb.ExportMetricsServiceRequest)
		if readErr == nil {
			readErr = proto.Unmarshal(body, payload)
		}
		requests <- collectorRequest{payload: payload, header: request.Header.Clone(), err: readErr}
		writer.Header().Set("Content-Type", "application/x-protobuf")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(response)
	}))
	t.Cleanup(server.Close)
	return &testCollector{server: server, requests: requests}
}

// endpoint returns the collector host and port accepted by OTLP WithEndpoint.
func (collector *testCollector) endpoint() string {
	return strings.TrimPrefix(collector.server.URL, "http://")
}

// next returns the next bounded collector request.
func (collector *testCollector) next(t *testing.T) collectorRequest {
	t.Helper()
	select {
	case request := <-collector.requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for OTLP metrics")
		return collectorRequest{}
	}
}

// TestRuntimeExportsTheClosedMetricContract proves real OTLP/protobuf delivery and attribute bounds.
func TestRuntimeExportsTheClosedMetricContract(t *testing.T) {
	collector := newTestCollector(t)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-test-authorization=present")
	runtime, err := NewRuntime(t.Context(), Options{
		Endpoint:       collector.endpoint(),
		Interval:       time.Hour,
		Timeout:        time.Second,
		Insecure:       true,
		ServiceVersion: "test-version",
	})
	require.NoError(t, err)
	metrics := runtime.Metrics()
	metrics.RecordRequest(t.Context(), proxy.RequestMetric{
		Method: "GET", Route: "object", StatusCode: http.StatusOK, BodySize: 42,
		Duration: 25 * time.Millisecond, Scheme: "http", ProtocolVersion: "1.1",
	})
	metrics.RecordActiveStreams(t.Context(), 2)
	metrics.RecordRejectedStream(t.Context())
	metrics.RecordIncompleteStream(t.Context())
	metrics.RecordReadiness(t.Context(), false, "s3_unavailable")
	metrics.RecordS3Request(t.Context(), proxy.S3Metric{
		Operation: "GetObject", Outcome: "success", Duration: 15 * time.Millisecond, Retries: 2, Transferred: 42,
	})

	require.NoError(t, runtime.ForceFlush(t.Context()))
	request := collector.next(t)
	require.NoError(t, request.err)
	assert.Equal(t, "present", request.header.Get("X-Test-Authorization"))
	metricsByName := exportedMetrics(request.payload)
	expected := []string{
		"http.server.active_requests",
		"http.server.request.duration",
		"http.server.response.body.size",
		"simplestreams_s3.readiness",
		"simplestreams_s3.s3.request.duration",
		"simplestreams_s3.s3.requests",
		"simplestreams_s3.s3.retries",
		"simplestreams_s3.s3.transferred",
		"simplestreams_s3.streams.incomplete",
		"simplestreams_s3.streams.rejected",
	}
	assert.Equal(t, expected, sortedMetricNames(metricsByName))
	assertResource(t, request.payload, "simplestreams-s3", "test-version")
	assertMetricAttributes(t, metricsByName)
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, runtime.Shutdown(shutdownContext))
}

// TestRuntimeDisabledHasNoExporterEffect proves an empty endpoint preserves no-op behavior.
func TestRuntimeDisabledHasNoExporterEffect(t *testing.T) {
	runtime, err := NewRuntime(t.Context(), Options{})
	require.NoError(t, err)
	runtime.Metrics().RecordRejectedStream(t.Context())
	require.NoError(t, runtime.ForceFlush(t.Context()))
	require.NoError(t, runtime.Shutdown(t.Context()))
}

// TestRuntimeRateLimitsSanitizedExporterWarnings proves collector failure remains bounded and fail-open.
func TestRuntimeRateLimitsSanitizedExporterWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "collector detail that must not be logged", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	var output bytes.Buffer
	runtime, err := NewRuntime(t.Context(), Options{
		Endpoint:       strings.TrimPrefix(server.URL, "http://"),
		Interval:       time.Hour,
		Timeout:        30 * time.Millisecond,
		Insecure:       true,
		ServiceVersion: "test-version",
		Logger:         slog.New(slog.NewJSONHandler(&output, nil)),
	})
	require.NoError(t, err)
	runtime.Metrics().RecordRejectedStream(t.Context())
	for range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		err = runtime.ForceFlush(ctx)
		cancel()
		require.Error(t, err)
	}
	assert.Equal(t, 1, strings.Count(output.String(), "metric export failed"))
	assert.NotContains(t, output.String(), "collector detail")
	assert.NotContains(t, output.String(), server.URL)
	shutdownContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = runtime.Shutdown(shutdownContext)
}

// TestRuntimeShutdownHonorsCallerDeadline proves a stalled collector cannot extend process shutdown.
func TestRuntimeShutdownHonorsCallerDeadline(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		started <- struct{}{}
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	runtime, err := NewRuntime(t.Context(), Options{
		Endpoint:       strings.TrimPrefix(server.URL, "http://"),
		Interval:       time.Hour,
		Timeout:        40 * time.Millisecond,
		Insecure:       true,
		ServiceVersion: "test-version",
	})
	require.NoError(t, err)
	runtime.Metrics().RecordRejectedStream(t.Context())
	result := make(chan error, 1)
	startedAt := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		result <- runtime.Shutdown(ctx)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("collector request did not start")
	}
	err = <-result
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(startedAt), 500*time.Millisecond)
}

// exportedMetrics indexes all exported metric payloads by their fixed name.
func exportedMetrics(request *collectormetricpb.ExportMetricsServiceRequest) map[string]*metricpb.Metric {
	result := make(map[string]*metricpb.Metric)
	for _, resourceMetrics := range request.GetResourceMetrics() {
		for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
			for _, instrument := range scopeMetrics.GetMetrics() {
				result[instrument.GetName()] = instrument
			}
		}
	}
	return result
}

// sortedMetricNames returns stable names for exact instrument-set assertions.
func sortedMetricNames(metrics map[string]*metricpb.Metric) []string {
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// assertResource verifies the only application-owned OTLP resource identities.
func assertResource(
	t *testing.T,
	request *collectormetricpb.ExportMetricsServiceRequest,
	serviceName string,
	serviceVersion string,
) {
	t.Helper()
	resources := request.GetResourceMetrics()
	require.NotEmpty(t, resources)
	values := make(map[string]string)
	for _, item := range resources[0].GetResource().GetAttributes() {
		values[item.GetKey()] = item.GetValue().GetStringValue()
	}
	assert.Equal(t, serviceName, values["service.name"])
	assert.Equal(t, serviceVersion, values["service.version"])
}

// assertMetricAttributes proves every emitted dimension belongs to the design allowlist.
func assertMetricAttributes(t *testing.T, metrics map[string]*metricpb.Metric) {
	t.Helper()
	standard := map[string]bool{
		"http.request.method": true, "http.response.status_code": true, "http.route": true,
		"url.scheme": true, "network.protocol.name": true, "network.protocol.version": true,
	}
	s3 := map[string]bool{"aws.operation": true, "outcome": true, "error.kind": true}
	readiness := map[string]bool{"outcome": true, "error.kind": true}
	for name, instrument := range metrics {
		allowed := map[string]bool{}
		switch {
		case strings.HasPrefix(name, "http.server.") && name != "http.server.active_requests":
			allowed = standard
		case strings.HasPrefix(name, "simplestreams_s3.s3."):
			allowed = s3
		case name == "simplestreams_s3.readiness":
			allowed = readiness
		}
		for _, key := range metricAttributeKeys(instrument) {
			assert.True(t, allowed[key], "metric %s emitted prohibited attribute %s", name, key)
		}
	}
}

// metricAttributeKeys returns every distinct data-point attribute key from one instrument.
func metricAttributeKeys(instrument *metricpb.Metric) []string {
	keys := make(map[string]struct{})
	if gauge := instrument.GetGauge(); gauge != nil {
		for _, point := range gauge.GetDataPoints() {
			for _, item := range point.GetAttributes() {
				keys[item.GetKey()] = struct{}{}
			}
		}
	}
	if sum := instrument.GetSum(); sum != nil {
		for _, point := range sum.GetDataPoints() {
			for _, item := range point.GetAttributes() {
				keys[item.GetKey()] = struct{}{}
			}
		}
	}
	if histogram := instrument.GetHistogram(); histogram != nil {
		for _, point := range histogram.GetDataPoints() {
			for _, item := range point.GetAttributes() {
				keys[item.GetKey()] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
