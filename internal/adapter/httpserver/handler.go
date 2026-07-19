// Package httpserver maps Phase 2 HTTP requests to the proxy application service.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/proxy"
)

const phaseTwoReadHeaderTimeout = 5 * time.Second

// Handler serves exact Simple Streams object paths without content transformation.
type Handler struct {
	proxy *proxy.Service
}

// errorBody is the intentionally small sanitized Phase 2 error document.
type errorBody struct {
	Code string `json:"code"`
}

// NewHandler constructs an HTTP adapter over the proxy application service.
func NewHandler(service *proxy.Service) *Handler {
	return &Handler{proxy: service}
}

// ServeHTTP reserves health paths and dispatches exact GET or HEAD object reads.
func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/healthz":
		writer.WriteHeader(http.StatusOK)
		return
	case "/readyz":
		writeError(writer, request.Method, http.StatusServiceUnavailable, "readiness_not_implemented")
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.get(writer, request)
	case http.MethodHead:
		handler.head(writer, request)
	default:
		writer.Header().Set("Allow", "GET, HEAD")
		writeError(writer, request.Method, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

// get streams one exact S3 representation without buffering or rewriting it.
func (handler *Handler) get(writer http.ResponseWriter, request *http.Request) {
	result, err := handler.proxy.Get(request.Context(), request.URL.EscapedPath())
	if err != nil {
		writeApplicationError(writer, request.Method, err)
		return
	}
	defer result.Body.Close()
	writeAttributes(writer.Header(), result.Attributes)
	writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(writer, result.Body)
}

// head returns only the exact S3 representation attributes.
func (handler *Handler) head(writer http.ResponseWriter, request *http.Request) {
	attributes, err := handler.proxy.Head(request.Context(), request.URL.EscapedPath())
	if err != nil {
		writeApplicationError(writer, request.Method, err)
		return
	}
	writeAttributes(writer.Header(), attributes)
	writer.WriteHeader(http.StatusOK)
}

// writeAttributes maps the Phase 2 response attributes without arbitrary metadata.
func writeAttributes(header http.Header, attributes proxy.Attributes) {
	if attributes.ContentType != "" {
		header.Set("Content-Type", attributes.ContentType)
	}
	header.Set("Content-Length", strconv.FormatInt(attributes.Size.Int64(), 10))
	if attributes.ETag != "" {
		header.Set("ETag", attributes.ETag)
	}
	if !attributes.LastModified.IsZero() {
		header.Set("Last-Modified", attributes.LastModified.UTC().Format(http.TimeFormat))
	}
}

// writeApplicationError translates stable application failure kinds once at the HTTP boundary.
func writeApplicationError(writer http.ResponseWriter, method string, err error) {
	status := http.StatusInternalServerError
	code := "internal_failure"
	switch failure.KindOf(err) {
	case failure.KindInvalidInput:
		status, code = http.StatusBadRequest, string(failure.KindInvalidInput)
	case failure.KindNotFound:
		status, code = http.StatusNotFound, string(failure.KindNotFound)
	case failure.KindDeadline:
		status, code = http.StatusGatewayTimeout, string(failure.KindDeadline)
	case failure.KindUnavailable:
		status, code = http.StatusServiceUnavailable, string(failure.KindUnavailable)
	case failure.KindUnauthorized:
		status, code = http.StatusBadGateway, string(failure.KindUnauthorized)
	case failure.KindCanceled:
		status, code = http.StatusServiceUnavailable, string(failure.KindCanceled)
	case failure.KindUnsupportedImage, failure.KindAlreadyExists, failure.KindIntegrity,
		failure.KindContentConflict, failure.KindCatalogConflict, failure.KindPrecondition,
		failure.KindInternal:
	}
	writeError(writer, method, status, code)
}

// writeError emits a small sanitized response and never writes a HEAD body.
func writeError(writer http.ResponseWriter, method string, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	if status == http.StatusServiceUnavailable {
		writer.Header().Set("Retry-After", "1")
	}
	writer.WriteHeader(status)
	if method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(writer).Encode(errorBody{Code: code})
}

// Server runs the Phase 2 HTTP adapter until cancellation or listener failure.
type Server struct {
	server *http.Server
}

// NewServer constructs a plain-HTTP trusted-boundary listener.
func NewServer(listen string, handler http.Handler) *Server {
	return &Server{server: &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: phaseTwoReadHeaderTimeout,
	}}
}

// Run serves until ctx is canceled or the listener fails.
func (server *Server) Run(ctx context.Context) error {
	result := make(chan error, 1)
	go func() {
		result <- server.server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		if err := server.server.Close(); err != nil {
			return fmt.Errorf("close HTTP server: %w", err)
		}
		return nil
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	}
}
