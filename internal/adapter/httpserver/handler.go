// Package httpserver maps authenticated S3 reads to the production HTTP contract.
package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/proxy"
)

const phaseTwoReadHeaderTimeout = 5 * time.Second

// Options configures the HTTP adapter's bounded production behavior.
type Options struct {
	// MaxStreams caps concurrent S3-backed object requests.
	MaxStreams int
	// Logger receives structured access and lifecycle records; nil discards records.
	Logger *slog.Logger
	// Metrics receives bounded request and stream emission points; nil discards them.
	Metrics proxy.Metrics
}

// Handler serves exact Simple Streams object paths without content transformation.
type Handler struct {
	proxy   *proxy.Service
	streams chan struct{}
	logger  *slog.Logger
	metrics proxy.Metrics
}

// errorBody is the intentionally small sanitized HTTP error document.
type errorBody struct {
	// Code is the stable public failure code.
	Code string `json:"code"`
	// RequestID lets callers correlate the response with a sanitized access record.
	RequestID string `json:"request_id"`
}

// NewHandler constructs an HTTP adapter with normative Phase 4 defaults.
func NewHandler(service *proxy.Service) *Handler {
	return NewHandlerWithOptions(service, Options{MaxStreams: 64})
}

// NewHandlerWithOptions constructs an HTTP adapter over the proxy application service.
func NewHandlerWithOptions(service *proxy.Service, options Options) *Handler {
	if options.MaxStreams < 1 {
		options.MaxStreams = 64
	}
	if options.Logger == nil {
		options.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if options.Metrics == nil {
		options.Metrics = proxy.NoopMetrics()
	}
	return &Handler{
		proxy:   service,
		streams: make(chan struct{}, options.MaxStreams),
		logger:  options.Logger,
		metrics: options.Metrics,
	}
}

// ServeHTTP reserves health paths and dispatches exact GET or HEAD object reads.
func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestID := requestID(request.Header.Get("X-Request-ID"))
	writer.Header().Set("X-Request-ID", requestID)
	switch request.URL.Path {
	case "/healthz":
		handler.health(writer, request, requestID)
		return
	case "/readyz":
		writeError(writer, request.Method, http.StatusServiceUnavailable, "starting", requestID)
		return
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead:
		handler.object(writer, request, requestID)
	default:
		writer.Header().Set("Allow", "GET, HEAD")
		writeError(writer, request.Method, http.StatusMethodNotAllowed, "method_not_allowed", requestID)
	}
}

// health returns process-local liveness without contacting S3 or metrics.
func (handler *Handler) health(writer http.ResponseWriter, request *http.Request, requestID string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = io.WriteString(writer, `{"status":"ok"}`)
	}
	handler.logger.Debug("request completed", "request_id", requestID, "http.route", "health", "http.response.status_code", http.StatusOK)
}

// object applies the bounded HTTP-to-S3 read contract to one object request.
func (handler *Handler) object(writer http.ResponseWriter, request *http.Request, requestID string) {
	readRequest, err := parseRequest(request)
	if err != nil {
		writeError(writer, request.Method, http.StatusBadRequest, "invalid_input", requestID)
		handler.record(request.Context(), requestID, request.Method, http.StatusBadRequest, 0, "invalid_input", false)
		return
	}
	if !handler.acquire() {
		writer.Header().Set("Retry-After", "1")
		writeError(writer, request.Method, http.StatusServiceUnavailable, "stream_limit", requestID)
		handler.metrics.RecordRejectedStream(request.Context())
		handler.record(request.Context(), requestID, request.Method, http.StatusServiceUnavailable, 0, "unavailable", readRequest.Range != "")
		return
	}
	defer handler.release()

	if request.Method == http.MethodHead {
		handler.head(writer, request, requestID, readRequest)
		return
	}
	handler.get(writer, request, requestID, readRequest)
}

// get streams one exact S3 representation without buffering or rewriting it.
func (handler *Handler) get(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	readRequest proxy.Request,
) {
	result, err := handler.proxy.Get(request.Context(), request.URL.EscapedPath(), readRequest)
	if err != nil {
		status, code := writeApplicationError(writer, request.Method, err, requestID)
		handler.record(request.Context(), requestID, request.Method, status, 0, code, readRequest.Range != "")
		return
	}
	defer result.Body.Close()
	writeAttributes(writer.Header(), result.Attributes)
	status := http.StatusOK
	if result.Attributes.ContentRange != "" {
		status = http.StatusPartialContent
	}
	writer.WriteHeader(status)
	written, copyErr := io.Copy(writer, result.Body)
	if copyErr != nil {
		handler.metrics.RecordIncompleteStream(request.Context())
		handler.logger.Warn("stream incomplete", "request_id", requestID, "error.kind", "unavailable", "http.response.body.size", written)
		handler.record(request.Context(), requestID, request.Method, status, written, "unavailable", readRequest.Range != "")
		return
	}
	handler.record(request.Context(), requestID, request.Method, status, written, "", readRequest.Range != "")
}

// head returns only the exact S3 representation attributes.
func (handler *Handler) head(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	readRequest proxy.Request,
) {
	attributes, err := handler.proxy.Head(request.Context(), request.URL.EscapedPath(), readRequest)
	if err != nil {
		status, code := writeApplicationError(writer, request.Method, err, requestID)
		handler.record(request.Context(), requestID, request.Method, status, 0, code, readRequest.Range != "")
		return
	}
	writeAttributes(writer.Header(), attributes)
	status := http.StatusOK
	if attributes.ContentRange != "" {
		status = http.StatusPartialContent
	}
	writer.WriteHeader(status)
	handler.record(request.Context(), requestID, request.Method, status, 0, "", readRequest.Range != "")
}

// acquire reserves one S3-backed stream without queueing callers.
func (handler *Handler) acquire() bool {
	select {
	case handler.streams <- struct{}{}:
		return true
	default:
		return false
	}
}

// release returns one previously acquired S3-backed stream reservation.
func (handler *Handler) release() { <-handler.streams }

// record writes one low-cardinality completion record and its no-op metric emission.
func (handler *Handler) record(
	ctx context.Context,
	requestID string,
	method string,
	status int,
	bodySize int64,
	errorKind string,
	rangeRequested bool,
) {
	handler.metrics.RecordRequest(ctx, proxy.RequestMetric{
		Method: method, Route: "object", StatusCode: status, BodySize: bodySize, ErrorKind: errorKind,
	})
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("http.request.method", method),
		slog.String("http.route", "object"),
		slog.Int("http.response.status_code", status),
		slog.Int64("http.response.body.size", bodySize),
		slog.Bool("range_requested", rangeRequested),
	}
	if errorKind != "" {
		attributes = append(attributes, slog.String("error.kind", errorKind))
	}
	handler.logger.LogAttrs(context.Background(), slog.LevelInfo, "request completed", attributes...)
}

// writeAttributes maps the fixed allowlist of S3 response properties.
func writeAttributes(header http.Header, attributes proxy.Attributes) {
	if attributes.ContentType != "" {
		header.Set("Content-Type", attributes.ContentType)
	}
	header.Set("Content-Length", strconv.FormatInt(attributes.Size.Int64(), 10))
	if attributes.ContentRange != "" {
		header.Set("Content-Range", attributes.ContentRange)
	}
	if attributes.AcceptRanges != "" {
		header.Set("Accept-Ranges", attributes.AcceptRanges)
	}
	if attributes.ETag != "" {
		header.Set("ETag", attributes.ETag)
	}
	if !attributes.LastModified.IsZero() {
		header.Set("Last-Modified", attributes.LastModified.UTC().Format(http.TimeFormat))
	}
	if attributes.CacheControl != "" {
		header.Set("Cache-Control", attributes.CacheControl)
	}
	if attributes.ContentDisposition != "" {
		header.Set("Content-Disposition", attributes.ContentDisposition)
	}
	if attributes.ContentEncoding != "" {
		header.Set("Content-Encoding", attributes.ContentEncoding)
	}
	if !attributes.Expires.IsZero() {
		header.Set("Expires", attributes.Expires.UTC().Format(http.TimeFormat))
	}
}

// writeApplicationError translates stable application failure kinds once at the HTTP boundary.
func writeApplicationError(writer http.ResponseWriter, method string, err error, requestID string) (int, string) {
	status := http.StatusInternalServerError
	code := "internal_failure"
	switch failure.KindOf(err) {
	case failure.KindInvalidInput:
		status, code = http.StatusBadRequest, string(failure.KindInvalidInput)
	case failure.KindNotFound:
		status, code = http.StatusNotFound, string(failure.KindNotFound)
	case failure.KindNotModified:
		status, code = http.StatusNotModified, string(failure.KindNotModified)
	case failure.KindRangeNotSatisfiable:
		status, code = http.StatusRequestedRangeNotSatisfiable, string(failure.KindRangeNotSatisfiable)
	case failure.KindPrecondition:
		status, code = http.StatusPreconditionFailed, string(failure.KindPrecondition)
	case failure.KindDeadline:
		status, code = http.StatusGatewayTimeout, string(failure.KindDeadline)
	case failure.KindUnavailable, failure.KindCanceled:
		status, code = http.StatusServiceUnavailable, string(failure.KindUnavailable)
	case failure.KindUnauthorized:
		status, code = http.StatusBadGateway, string(failure.KindUnauthorized)
	case failure.KindUnsupportedImage, failure.KindAlreadyExists, failure.KindIntegrity,
		failure.KindContentConflict, failure.KindCatalogConflict, failure.KindInternal:
	}
	if status == http.StatusServiceUnavailable {
		writer.Header().Set("Retry-After", "1")
	}
	writeError(writer, method, status, code, requestID)
	return status, code
}

// writeError emits a small sanitized response and never writes a HEAD body.
func writeError(writer http.ResponseWriter, method string, status int, code string, requestID string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	if method == http.MethodHead || status == http.StatusNotModified {
		return
	}
	_ = json.NewEncoder(writer).Encode(errorBody{Code: code, RequestID: requestID})
}

// parseRequest validates HTTP condition syntax and selects one forwarding-safe range.
func parseRequest(request *http.Request) (proxy.Request, error) {
	ifMatch := request.Header.Get("If-Match")
	ifNoneMatch := request.Header.Get("If-None-Match")
	if !validETagList(ifMatch) || !validETagList(ifNoneMatch) {
		return proxy.Request{}, errors.New("invalid entity-tag condition")
	}
	modifiedSince, err := parseHTTPTime(request.Header.Get("If-Modified-Since"))
	if err != nil {
		return proxy.Request{}, err
	}
	unmodifiedSince, err := parseHTTPTime(request.Header.Get("If-Unmodified-Since"))
	if err != nil {
		return proxy.Request{}, err
	}
	return proxy.Request{
		Range:             singleRange(request.Header.Get("Range"), request.Header.Get("If-Range") != ""),
		IfMatch:           ifMatch,
		IfNoneMatch:       ifNoneMatch,
		IfModifiedSince:   modifiedSince,
		IfUnmodifiedSince: unmodifiedSince,
	}, nil
}

// parseHTTPTime parses one optional HTTP date condition.
func parseHTTPTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// validETagList accepts the RFC entity-tag subset that S3 can evaluate safely.
func validETagList(value string) bool {
	if value == "" || value == "*" {
		return true
	}
	for item := range strings.SplitSeq(value, ",") {
		item = strings.TrimSpace(item)
		if strings.HasPrefix(item, "W/") {
			item = strings.TrimPrefix(item, "W/")
		}
		if len(item) < 2 || item[0] != '"' || item[len(item)-1] != '"' || strings.ContainsAny(item[1:len(item)-1], "\"\\\r\n") {
			return false
		}
	}
	return true
}

// singleRange returns one syntactically valid bytes range or the full-representation empty value.
func singleRange(value string, ignore bool) string {
	if value == "" || ignore || !strings.HasPrefix(value, "bytes=") {
		return ""
	}
	specification := strings.TrimPrefix(value, "bytes=")
	if strings.Contains(specification, ",") {
		return ""
	}
	start, end, found := strings.Cut(specification, "-")
	if !found || (start == "" && end == "") {
		return ""
	}
	if start != "" {
		if _, err := strconv.ParseInt(start, 10, 64); err != nil {
			return ""
		}
	}
	if end != "" {
		if _, err := strconv.ParseInt(end, 10, 64); err != nil {
			return ""
		}
	}
	if start != "" && end != "" {
		startValue, _ := strconv.ParseInt(start, 10, 64)
		endValue, _ := strconv.ParseInt(end, 10, 64)
		if endValue < startValue {
			return ""
		}
	}
	return value
}

// requestID accepts a bounded caller correlation ID or generates a safe replacement.
func requestID(value string) string {
	if len(value) > 0 && len(value) <= 128 {
		for _, character := range value {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') || strings.ContainsRune("._-", character) {
				continue
			}
			return generatedRequestID()
		}
		return value
	}
	return generatedRequestID()
}

// generatedRequestID creates a log-safe correlation ID without external state.
func generatedRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(buffer)
}

// Server runs the HTTP adapter until cancellation or listener failure.
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
	go func() { result <- server.server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		if err := server.server.Close(); err != nil {
			return errors.Join(ctx.Err(), err)
		}
		return nil
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
