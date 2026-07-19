package httpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
}
