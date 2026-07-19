package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
	"github.com/meigma/simplestreams-s3/internal/proxy"
)

// fakeHTTPReader returns a fixed object or configured application failure.
type fakeHTTPReader struct {
	err       error
	headCalls int
	getCalls  int
	request   proxy.Request
}

// blockingHTTPReader holds one Get call open to exercise the local stream semaphore.
type blockingHTTPReader struct {
	started chan struct{}
	release chan struct{}
}

// failingHTTPReader returns one partial body followed by an upstream read failure.
type failingHTTPReader struct{}

// stalledHTTPReader returns a body that makes no upstream progress until the handler closes it.
type stalledHTTPReader struct{}

// lockedBuffer makes concurrent lifecycle-log assertions race-safe.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

// readinessMetric captures one cached readiness observation.
type readinessMetric struct {
	ready  bool
	reason string
}

// recordingMetrics captures observable handler emission behavior without an exporter.
type recordingMetrics struct {
	requests  []proxy.RequestMetric
	active    []int
	readiness []readinessMetric
}

// RecordRequest captures one completed HTTP request.
func (metrics *recordingMetrics) RecordRequest(_ context.Context, input proxy.RequestMetric) {
	metrics.requests = append(metrics.requests, input)
}

// RecordRejectedStream accepts a local stream rejection.
func (*recordingMetrics) RecordRejectedStream(context.Context) {}

// RecordActiveStreams captures the current S3-backed stream count.
func (metrics *recordingMetrics) RecordActiveStreams(_ context.Context, count int) {
	metrics.active = append(metrics.active, count)
}

// RecordIncompleteStream accepts one interrupted stream.
func (*recordingMetrics) RecordIncompleteStream(context.Context) {}

// RecordReadiness captures one background cached-state observation.
func (metrics *recordingMetrics) RecordReadiness(_ context.Context, ready bool, reason string) {
	metrics.readiness = append(metrics.readiness, readinessMetric{ready: ready, reason: reason})
}

// RecordS3Request accepts one adapter operation observation.
func (*recordingMetrics) RecordS3Request(context.Context, proxy.S3Metric) {}

// Write appends one log fragment safely.
func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

// String returns a stable snapshot of recorded log output.
func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

// Head returns an unused fixed representation.
func (failingHTTPReader) Head(context.Context, object.ObjectKey, proxy.Request) (proxy.Attributes, error) {
	size, _ := object.NewByteSize(7)
	return proxy.Attributes{Size: size}, nil
}

// Get returns a body that fails after its first response bytes have been written.
func (failingHTTPReader) Get(context.Context, object.ObjectKey, proxy.Request) (proxy.Object, error) {
	size, _ := object.NewByteSize(7)
	return proxy.Object{
		Attributes: proxy.Attributes{Size: size},
		Body:       io.NopCloser(&failingBody{}),
	}, nil
}

// Head returns an unused fixed representation.
func (stalledHTTPReader) Head(context.Context, object.ObjectKey, proxy.Request) (proxy.Attributes, error) {
	size, _ := object.NewByteSize(7)
	return proxy.Attributes{Size: size}, nil
}

// Get returns an upstream body that only unblocks when its owner closes it.
func (stalledHTTPReader) Get(context.Context, object.ObjectKey, proxy.Request) (proxy.Object, error) {
	size, _ := object.NewByteSize(7)
	return proxy.Object{Attributes: proxy.Attributes{Size: size}, Body: &stalledBody{closed: make(chan struct{})}}, nil
}

// failingBody supplies one chunk before simulating a broken upstream transfer.
type failingBody struct{ sent bool }

// stalledBody models an upstream that stops producing bytes forever.
type stalledBody struct {
	closed chan struct{}
	once   sync.Once
}

// Read blocks until Close interrupts the stalled upstream response.
func (body *stalledBody) Read([]byte) (int, error) {
	<-body.closed
	return 0, context.Canceled
}

// Close interrupts the blocked Read exactly once.
func (body *stalledBody) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

// Read returns one partial chunk and then a deterministic upstream failure.
func (body *failingBody) Read(buffer []byte) (int, error) {
	if body.sent {
		return 0, errors.New("upstream interrupted")
	}
	body.sent = true
	return copy(buffer, "partial"), nil
}

// Head returns an unused fixed representation.
func (reader *blockingHTTPReader) Head(context.Context, object.ObjectKey, proxy.Request) (proxy.Attributes, error) {
	size, _ := object.NewByteSize(7)
	return proxy.Attributes{Size: size}, nil
}

// Get waits until the test releases the first admitted stream.
func (reader *blockingHTTPReader) Get(context.Context, object.ObjectKey, proxy.Request) (proxy.Object, error) {
	close(reader.started)
	<-reader.release
	size, _ := object.NewByteSize(7)
	return proxy.Object{Attributes: proxy.Attributes{Size: size}, Body: io.NopCloser(strings.NewReader("catalog"))}, nil
}

// Head returns fixed response attributes or the configured failure.
func (reader *fakeHTTPReader) Head(
	_ context.Context,
	_ object.ObjectKey,
	request proxy.Request,
) (proxy.Attributes, error) {
	reader.headCalls++
	reader.request = request
	if reader.err != nil {
		return proxy.Attributes{}, reader.err
	}
	size, _ := object.NewByteSize(7)
	return proxy.Attributes{Size: size, ContentType: "application/json", ETag: `"etag"`}, nil
}

// Get returns a fixed exact response body or the configured failure.
func (reader *fakeHTTPReader) Get(_ context.Context, _ object.ObjectKey, request proxy.Request) (proxy.Object, error) {
	reader.getCalls++
	reader.request = request
	if reader.err != nil {
		return proxy.Object{}, reader.err
	}
	size, _ := object.NewByteSize(7)
	return proxy.Object{
		Attributes: proxy.Attributes{Size: size, ContentType: "application/json", ETag: `"etag"`},
		Body:       io.NopCloser(strings.NewReader("catalog")),
	}, nil
}

// TestHandlerForwardsOneValidRangeAndConditions proves the transport adapter preserves valid request semantics.
func TestHandlerForwardsOneValidRangeAndConditions(t *testing.T) {
	t.Parallel()
	reader := &fakeHTTPReader{}
	handler := NewHandler(proxy.NewService(reader, ""))
	request := httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil)
	request.Header.Set("Range", "bytes=1-3")
	request.Header.Set("If-Match", `"etag"`)
	request.Header.Set("If-None-Match", `W/"older"`)
	request.Header.Set("If-Modified-Since", "Mon, 02 Jan 2006 15:04:05 GMT")
	request.Header.Set("If-Unmodified-Since", "Tue, 03 Jan 2006 15:04:05 GMT")

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "bytes=1-3", reader.request.Range)
	assert.Equal(t, `"etag"`, reader.request.IfMatch)
	assert.Equal(t, `W/"older"`, reader.request.IfNoneMatch)
	require.NotNil(t, reader.request.IfModifiedSince)
	require.NotNil(t, reader.request.IfUnmodifiedSince)
}

// TestHandlerFallsBackToFullRepresentationForUnsupportedRangeSyntax proves unsupported range forms are not forwarded.
func TestHandlerFallsBackToFullRepresentationForUnsupportedRangeSyntax(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		rangeValue string
		ifRange    string
	}{
		{name: "multiple ranges", rangeValue: "bytes=0-1,3-4"},
		{name: "unsupported unit", rangeValue: "items=0-1"},
		{name: "invalid bounds", rangeValue: "bytes=5-1"},
		{name: "if range", rangeValue: "bytes=0-1", ifRange: `"etag"`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			reader := &fakeHTTPReader{}
			handler := NewHandler(proxy.NewService(reader, ""))
			request := httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil)
			request.Header.Set("Range", testCase.rangeValue)
			request.Header.Set("If-Range", testCase.ifRange)

			handler.ServeHTTP(httptest.NewRecorder(), request)

			assert.Empty(t, reader.request.Range)
		})
	}
}

// TestHandlerRejectsMalformedConditions proves malformed boundary values do not reach the S3 port.
func TestHandlerRejectsMalformedConditions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "invalid match", header: "If-Match", value: "not-an-etag"},
		{name: "invalid modified time", header: "If-Modified-Since", value: "not-a-date"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			reader := &fakeHTTPReader{}
			handler := NewHandler(proxy.NewService(reader, ""))
			request := httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil)
			request.Header.Set(testCase.header, testCase.value)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Zero(t, reader.getCalls)
		})
	}
}

// TestHandlerRestrictsHealthRoutesToGETAndHEAD proves reserved routes do not bypass the HTTP method contract.
func TestHandlerRestrictsHealthRoutesToGETAndHEAD(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			handler := NewHandler(proxy.NewService(&fakeHTTPReader{}, ""))
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))

			assert.Equal(t, http.StatusMethodNotAllowed, response.Code)
			assert.Equal(t, "GET, HEAD", response.Header().Get("Allow"))
		})
	}
}

// TestHandlerEmitsStandardMetricsForEveryRouteClass proves bounded route coverage at the HTTP boundary.
func TestHandlerEmitsStandardMetricsForEveryRouteClass(t *testing.T) {
	metrics := new(recordingMetrics)
	handler := NewHandlerWithOptions(proxy.NewService(&fakeHTTPReader{}, ""), Options{Metrics: metrics})
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
		httptest.NewRequest(http.MethodGet, "/readyz", nil),
		httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil),
		httptest.NewRequest(http.MethodPost, "/unsupported", nil),
	} {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	routes := make([]string, 0, len(metrics.requests))
	for _, request := range metrics.requests {
		routes = append(routes, request.Route)
		assert.Positive(t, request.Duration)
		assert.Equal(t, "http", request.Scheme)
		assert.Equal(t, "1.1", request.ProtocolVersion)
	}
	assert.Equal(t, []string{"health", "readiness", "object", "unmatched"}, routes)
	assert.Equal(t, []int{1, 0}, metrics.active)
}

// TestReadinessEmitsProbeResults proves metrics follow background S3 checks instead of endpoint traffic.
func TestReadinessEmitsProbeResults(t *testing.T) {
	metrics := new(recordingMetrics)
	var output bytes.Buffer
	readiness := NewReadinessWithObservers(
		func(context.Context) error { return failure.New(failure.KindUnavailable, "probe", "unavailable") },
		time.Hour,
		time.Second,
		time.Second,
		metrics,
		slog.New(slog.NewJSONHandler(&output, nil)),
	)
	readiness.check(t.Context())

	require.Len(t, metrics.readiness, 1)
	assert.False(t, metrics.readiness[0].ready)
	assert.Equal(t, "s3_unavailable", metrics.readiness[0].reason)
	assert.Contains(t, output.String(), `"event":"readiness_transition"`)
}

// TestReadinessObserversTrackVisibleState proves staleness and draining cannot report false transitions.
func TestReadinessObserversTrackVisibleState(t *testing.T) {
	metrics := new(recordingMetrics)
	var output bytes.Buffer
	probeErr := error(nil)
	readiness := NewReadinessWithObservers(
		func(context.Context) error { return probeErr },
		time.Hour,
		time.Second,
		time.Minute,
		metrics,
		slog.New(slog.NewJSONHandler(&output, nil)),
	)

	readiness.check(t.Context())
	probeErr = failure.New(failure.KindUnavailable, "probe", "unavailable")
	readiness.check(t.Context())
	assert.Equal(t, []readinessMetric{{ready: true}, {ready: true}}, metrics.readiness)
	assert.Equal(t, 1, strings.Count(output.String(), `"event":"readiness_transition"`))

	readiness.mu.Lock()
	readiness.lastSuccess = time.Now().Add(-2 * time.Minute)
	readiness.mu.Unlock()
	readiness.check(t.Context())
	assert.Equal(t, readinessMetric{ready: false, reason: unavailableReason}, metrics.readiness[2])
	assert.Equal(t, 2, strings.Count(output.String(), `"event":"readiness_transition"`))

	readiness.SetDraining()
	probeErr = nil
	readiness.check(t.Context())
	assert.Equal(t, readinessMetric{ready: false, reason: drainingReason}, metrics.readiness[4])
	assert.Equal(t, 3, strings.Count(output.String(), `"event":"readiness_transition"`))
}

// TestHandlerRejectsSaturatedStreamsImmediately proves proxy requests never wait in an unbounded queue.
func TestHandlerRejectsSaturatedStreamsImmediately(t *testing.T) {
	t.Parallel()
	reader := &blockingHTTPReader{started: make(chan struct{}), release: make(chan struct{})}
	handler := NewHandlerWithOptions(proxy.NewService(reader, ""), Options{MaxStreams: 1})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil))
	}()
	<-reader.started

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil))

	assert.Equal(t, http.StatusServiceUnavailable, second.Code)
	assert.Equal(t, "1", second.Header().Get("Retry-After"))
	close(reader.release)
	<-firstDone
}

// TestHandlerAbortsTheConnectionAfterMidStreamFailure proves object bytes are never followed by an error document.
func TestHandlerAbortsTheConnectionAfterMidStreamFailure(t *testing.T) {
	t.Parallel()
	handler := NewHandler(proxy.NewService(failingHTTPReader{}, ""))
	response := httptest.NewRecorder()

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil))
	})
	assert.Equal(t, "partial", response.Body.String())
	assert.NotContains(t, response.Body.String(), "internal_failure")
}

// TestHandlerAbortsStalledUpstreams proves the upstream idle bound cancels a no-progress body.
func TestHandlerAbortsStalledUpstreams(t *testing.T) {
	t.Parallel()
	handler := NewHandlerWithOptions(proxy.NewService(stalledHTTPReader{}, ""), Options{
		UpstreamIdleTimeout: 20 * time.Millisecond,
	})

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil))
	})
}

// TestServerDrainsOnCancellation proves shutdown marks readiness unavailable and keeps lifecycle logs structured.
func TestServerDrainsOnCancellation(t *testing.T) {
	t.Parallel()
	var output lockedBuffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	readiness := NewReadiness(func(context.Context) error { return nil }, time.Hour, time.Second, time.Second)
	server := NewServerWithOptions(
		"127.0.0.1:0",
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		ServerOptions{
			Readiness:     readiness,
			ShutdownGrace: time.Second,
			Logger:        logger,
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- server.Run(ctx) }()

	require.Eventually(
		t,
		func() bool { return strings.Contains(output.String(), "server_listening") },
		time.Second,
		5*time.Millisecond,
	)
	cancel()
	require.NoError(t, <-result)
	ready, reason := readiness.Status(time.Now())
	assert.False(t, ready)
	assert.Equal(t, "draining", reason)
	assert.Contains(t, output.String(), `"component":"httpserver"`)
	assert.NotContains(t, output.String(), `"component":"httpserver","component":"httpserver"`)
}

// TestHandlerWritesOneSanitizedJSONCompletionRecord proves access logs include only bounded request fields.
func TestHandlerWritesOneSanitizedJSONCompletionRecord(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil)).With(
		"service.name", "simplestreams-s3",
		"service.version", "test",
	)
	handler := NewHandlerWithOptions(proxy.NewService(&fakeHTTPReader{}, ""), Options{Logger: logger})
	request := httptest.NewRequest(http.MethodGet, "/streams/v1/index.json?ignored=key", nil)
	request.Header.Set("X-Request-ID", "safe-request-id")

	handler.ServeHTTP(httptest.NewRecorder(), request)

	var record map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &record))
	assert.Equal(t, "request completed", record["msg"])
	assert.Equal(t, "safe-request-id", record["request_id"])
	assert.Equal(t, "object", record["http.route"])
	assert.Equal(t, "httpserver", record["component"])
	assert.Contains(t, record, "duration_ms")
	assert.NotContains(t, output.String(), "streams/v1/index.json")
}

// TestHandlerStreamsGETAndMapsHEADWithoutBodies proves the thin Phase 2 HTTP contract.
func TestHandlerStreamsGETAndMapsHEADWithoutBodies(t *testing.T) {
	t.Parallel()
	reader := &fakeHTTPReader{}
	handler := NewHandler(proxy.NewService(reader, ""))

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/streams/v1/index.json", nil))
	assert.Equal(t, http.StatusOK, getResponse.Code)
	assert.Equal(t, "catalog", getResponse.Body.String())
	assert.Equal(t, "application/json", getResponse.Header().Get("Content-Type"))
	assert.Equal(t, `"etag"`, getResponse.Header().Get("ETag"))

	headResponse := httptest.NewRecorder()
	handler.ServeHTTP(headResponse, httptest.NewRequest(http.MethodHead, "/streams/v1/index.json", nil))
	assert.Equal(t, http.StatusOK, headResponse.Code)
	assert.Empty(t, headResponse.Body.String())
	assert.Equal(t, "7", headResponse.Header().Get("Content-Length"))
}

// TestHandlerSanitizesErrorsAndReservesHealthRoutes proves failures never expose upstream detail.
func TestHandlerSanitizesErrorsAndReservesHealthRoutes(t *testing.T) {
	t.Parallel()
	reader := &fakeHTTPReader{err: failure.New(failure.KindNotFound, "get secret-bucket/key", "AWS detail")}
	handler := NewHandler(proxy.NewService(reader, ""))

	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodGet, "/missing", nil)
	missingRequest.Header.Set("X-Request-ID", "request-1")
	handler.ServeHTTP(missing, missingRequest)
	assert.Equal(t, http.StatusNotFound, missing.Code)
	assert.JSONEq(t, `{"code":"not_found","request_id":"request-1"}`, missing.Body.String())
	assert.Equal(t, "request-1", missing.Header().Get("X-Request-ID"))
	assert.NotContains(t, missing.Body.String(), "secret-bucket")

	health := httptest.NewRecorder()
	healthRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(health, healthRequest)
	assert.Equal(t, http.StatusOK, health.Code)
	assert.Zero(t, reader.headCalls)
	require.Equal(t, 1, reader.getCalls)

	readiness := httptest.NewRecorder()
	handler.ServeHTTP(readiness, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, readiness.Code)
	assert.JSONEq(t, `{"status":"not_ready","reason":"starting"}`, readiness.Body.String())
}
