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
}

// Head returns fixed response attributes or the configured failure.
func (reader *fakeHTTPReader) Head(_ context.Context, _ object.ObjectKey, _ proxy.Request) (proxy.Attributes, error) {
	reader.headCalls++
	if reader.err != nil {
		return proxy.Attributes{}, reader.err
	}
	size, _ := object.NewByteSize(7)
	return proxy.Attributes{Size: size, ContentType: "application/json", ETag: `"etag"`}, nil
}

// Get returns a fixed exact response body or the configured failure.
func (reader *fakeHTTPReader) Get(_ context.Context, _ object.ObjectKey, _ proxy.Request) (proxy.Object, error) {
	reader.getCalls++
	if reader.err != nil {
		return proxy.Object{}, reader.err
	}
	size, _ := object.NewByteSize(7)
	return proxy.Object{
		Attributes: proxy.Attributes{Size: size, ContentType: "application/json", ETag: `"etag"`},
		Body:       io.NopCloser(strings.NewReader("catalog")),
	}, nil
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
