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
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/proxy"
)

const (
	phaseTwoReadHeaderTimeout = 5 * time.Second
	defaultMaxStreams         = 64
	defaultMaxHeaderBytes     = 32768
	defaultShutdownGrace      = 30 * time.Second
	requestIDBytes            = 16
)

// Options configures the HTTP adapter's bounded production behavior.
type Options struct {
	// MaxStreams caps concurrent S3-backed object requests.
	MaxStreams int
	// Logger receives structured access and lifecycle records; nil discards records.
	Logger *slog.Logger
	// Metrics receives bounded request and stream emission points; nil discards them.
	Metrics proxy.Metrics
	// Readiness provides cached catalog state for the reserved readiness route.
	Readiness *Readiness
	// UpstreamIdleTimeout bounds time without upstream body-read progress.
	UpstreamIdleTimeout time.Duration
	// WriteIdleTimeout bounds time without downstream write progress.
	WriteIdleTimeout time.Duration
}

// Handler serves exact Simple Streams object paths without content transformation.
type Handler struct {
	proxy               *proxy.Service
	streams             chan struct{}
	logger              *slog.Logger
	metrics             proxy.Metrics
	ready               *Readiness
	upstreamIdleTimeout time.Duration
	writeIdleTimeout    time.Duration
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
	return NewHandlerWithOptions(service, Options{MaxStreams: defaultMaxStreams})
}

// NewHandlerWithOptions constructs an HTTP adapter over the proxy application service.
func NewHandlerWithOptions(service *proxy.Service, options Options) *Handler {
	if options.MaxStreams < 1 {
		options.MaxStreams = defaultMaxStreams
	}
	if options.Logger == nil {
		options.Logger = slog.New(slog.DiscardHandler)
	}
	options.Logger = options.Logger.With("component", "httpserver")
	if options.Metrics == nil {
		options.Metrics = proxy.NoopMetrics()
	}
	return &Handler{
		proxy:               service,
		streams:             make(chan struct{}, options.MaxStreams),
		logger:              options.Logger,
		metrics:             options.Metrics,
		ready:               options.Readiness,
		upstreamIdleTimeout: options.UpstreamIdleTimeout,
		writeIdleTimeout:    options.WriteIdleTimeout,
	}
}

// ServeHTTP reserves health paths and dispatches exact GET or HEAD object reads.
func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	started := time.Now()
	requestID := requestID(request.Header.Get("X-Request-ID"))
	writer.Header().Set("X-Request-ID", requestID)
	switch request.URL.Path {
	case "/healthz":
		if !healthMethod(writer, request, requestID) {
			handler.record(
				request,
				requestID,
				"health",
				http.StatusMethodNotAllowed,
				0,
				"method_not_allowed",
				false,
				started,
			)
			return
		}
		handler.health(writer, request, requestID, started)
		return
	case "/readyz":
		if !healthMethod(writer, request, requestID) {
			handler.record(
				request,
				requestID,
				"readiness",
				http.StatusMethodNotAllowed,
				0,
				"method_not_allowed",
				false,
				started,
			)
			return
		}
		handler.readiness(writer, request, requestID, started)
		return
	}
	switch request.Method {
	case http.MethodGet, http.MethodHead:
		handler.object(writer, request, requestID, started)
	default:
		writer.Header().Set("Allow", "GET, HEAD")
		writeError(writer, request.Method, http.StatusMethodNotAllowed, "method_not_allowed", requestID)
		handler.record(
			request,
			requestID,
			"unmatched",
			http.StatusMethodNotAllowed,
			0,
			"method_not_allowed",
			false,
			started,
		)
	}
}

// healthMethod enforces the shared GET and HEAD contract for health routes.
func healthMethod(writer http.ResponseWriter, request *http.Request, requestID string) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return true
	}
	writer.Header().Set("Allow", "GET, HEAD")
	writeError(writer, request.Method, http.StatusMethodNotAllowed, "method_not_allowed", requestID)
	return false
}

// readiness returns cached catalog availability without waiting for an S3 operation.
func (handler *Handler) readiness(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	started time.Time,
) {
	ready, reason := handler.ready.Status(time.Now())
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	status := http.StatusServiceUnavailable
	body := `{"status":"not_ready","reason":"` + reason + `"}`
	if ready {
		status = http.StatusOK
		body = `{"status":"ready"}`
	}
	writer.WriteHeader(status)
	bodySize := int64(0)
	if request.Method != http.MethodHead {
		written, _ := io.WriteString(writer, body)
		bodySize = int64(written)
	}
	handler.record(request, requestID, "readiness", status, bodySize, "", false, started)
}

// health returns process-local liveness without contacting S3 or metrics.
func (handler *Handler) health(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	started time.Time,
) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	bodySize := int64(0)
	if request.Method != http.MethodHead {
		written, _ := io.WriteString(writer, `{"status":"ok"}`)
		bodySize = int64(written)
	}
	handler.record(request, requestID, "health", http.StatusOK, bodySize, "", false, started)
}

// object applies the bounded HTTP-to-S3 read contract to one object request.
func (handler *Handler) object(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	started time.Time,
) {
	readRequest, err := parseRequest(request)
	if err != nil {
		writeError(writer, request.Method, http.StatusBadRequest, "invalid_input", requestID)
		handler.record(
			request,
			requestID,
			"object",
			http.StatusBadRequest,
			0,
			"invalid_input",
			false,
			started,
		)
		return
	}
	if !handler.acquire() {
		writer.Header().Set("Retry-After", "1")
		writeError(writer, request.Method, http.StatusServiceUnavailable, "stream_limit", requestID)
		handler.metrics.RecordRejectedStream(request.Context())
		handler.record(
			request,
			requestID,
			"object",
			http.StatusServiceUnavailable,
			0,
			"unavailable",
			readRequest.Range != "",
			started,
		)
		return
	}
	handler.metrics.RecordActiveStreams(request.Context(), len(handler.streams))
	defer func() {
		handler.release()
		handler.metrics.RecordActiveStreams(request.Context(), len(handler.streams))
	}()

	if request.Method == http.MethodHead {
		handler.head(writer, request, requestID, readRequest, started)
		return
	}
	handler.get(writer, request, requestID, readRequest, started)
}

// get streams one exact S3 representation without buffering or rewriting it.
func (handler *Handler) get(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	readRequest proxy.Request,
	started time.Time,
) {
	streamContext, cancel := context.WithCancel(request.Context())
	defer cancel()
	result, err := handler.proxy.Get(streamContext, request.URL.EscapedPath(), readRequest)
	if err != nil {
		status, code := writeApplicationError(writer, request.Method, err, requestID)
		handler.record(request, requestID, "object", status, 0, code, readRequest.Range != "", started)
		return
	}
	defer result.Body.Close()
	writeAttributes(writer.Header(), result.Attributes)
	status := http.StatusOK
	if result.Attributes.ContentRange != "" {
		status = http.StatusPartialContent
	}
	writer.WriteHeader(status)
	written, copyErr := handler.copyStream(streamContext, cancel, writer, result.Body)
	if copyErr != nil {
		handler.metrics.RecordIncompleteStream(request.Context())
		errorKind := "unavailable"
		if errors.Is(copyErr, context.Canceled) || request.Context().Err() != nil {
			errorKind = "canceled"
		} else if errors.Is(copyErr, context.DeadlineExceeded) {
			errorKind = "deadline_exceeded"
		}
		handler.logger.WarnContext(
			request.Context(),
			"stream incomplete",
			"request_id",
			requestID,
			"error.kind",
			errorKind,
			"http.response.body.size",
			written,
		)
		handler.record(
			request,
			requestID,
			"object",
			status,
			written,
			errorKind,
			readRequest.Range != "",
			started,
		)
		panic(http.ErrAbortHandler)
	}
	handler.record(request, requestID, "object", status, written, "", readRequest.Range != "", started)
}

// copyStream applies independent upstream-read and downstream-write progress bounds.
func (handler *Handler) copyStream(
	ctx context.Context,
	cancel context.CancelFunc,
	writer http.ResponseWriter,
	body io.ReadCloser,
) (int64, error) {
	reader := io.Reader(body)
	if handler.upstreamIdleTimeout > 0 {
		idle := newIdleReader(body, handler.upstreamIdleTimeout, cancel)
		defer idle.Close()
		reader = idle
	}
	responseWriter := io.Writer(writer)
	if handler.writeIdleTimeout > 0 {
		responseWriter = &idleWriter{writer: writer, timeout: handler.writeIdleTimeout, cancel: cancel}
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
		return io.Copy(responseWriter, reader)
	}
}

// idleReader turns a stalled upstream body read into cancellation and a bounded deadline error.
type idleReader struct {
	reader   io.ReadCloser
	timeout  time.Duration
	cancel   context.CancelFunc
	progress chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	timedOut bool
	close    sync.Once
}

// newIdleReader constructs one stream watchdog rather than one goroutine per read.
func newIdleReader(reader io.ReadCloser, timeout time.Duration, cancel context.CancelFunc) *idleReader {
	result := &idleReader{
		reader:   reader,
		timeout:  timeout,
		cancel:   cancel,
		progress: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	go result.watch()
	return result
}

// Read records upstream progress while the stream watchdog owns the idle deadline.
func (reader *idleReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		select {
		case reader.progress <- struct{}{}:
		default:
		}
	}
	reader.mu.Lock()
	timedOut := reader.timedOut
	reader.mu.Unlock()
	if timedOut && err == nil {
		return count, context.DeadlineExceeded
	}
	return count, err
}

// Close stops the watchdog and closes the underlying response body once.
func (reader *idleReader) Close() error {
	var result error
	reader.close.Do(func() {
		close(reader.done)
		result = reader.reader.Close()
	})
	return result
}

// watch cancels the S3 context and closes its body after one no-progress interval.
func (reader *idleReader) watch() {
	timer := time.NewTimer(reader.timeout)
	defer timer.Stop()
	for {
		select {
		case <-reader.done:
			return
		case <-reader.progress:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(reader.timeout)
		case <-timer.C:
			reader.mu.Lock()
			reader.timedOut = true
			reader.mu.Unlock()
			reader.cancel()
			_ = reader.Close()
			return
		}
	}
}

// idleWriter resets the response write deadline before every downstream progress attempt.
type idleWriter struct {
	writer  http.ResponseWriter
	timeout time.Duration
	cancel  context.CancelFunc
}

// Write sends one chunk while enforcing the configured no-progress deadline when supported.
func (writer *idleWriter) Write(data []byte) (int, error) {
	if err := http.NewResponseController(writer.writer).
		SetWriteDeadline(time.Now().Add(writer.timeout)); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		writer.cancel()
		return 0, err
	}
	count, err := writer.writer.Write(data)
	if err != nil {
		writer.cancel()
	}
	return count, err
}

// head returns only the exact S3 representation attributes.
func (handler *Handler) head(
	writer http.ResponseWriter,
	request *http.Request,
	requestID string,
	readRequest proxy.Request,
	started time.Time,
) {
	attributes, err := handler.proxy.Head(request.Context(), request.URL.EscapedPath(), readRequest)
	if err != nil {
		status, code := writeApplicationError(writer, request.Method, err, requestID)
		handler.record(request, requestID, "object", status, 0, code, readRequest.Range != "", started)
		return
	}
	writeAttributes(writer.Header(), attributes)
	status := http.StatusOK
	if attributes.ContentRange != "" {
		status = http.StatusPartialContent
	}
	writer.WriteHeader(status)
	handler.record(request, requestID, "object", status, 0, "", readRequest.Range != "", started)
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
	request *http.Request,
	requestID string,
	route string,
	status int,
	bodySize int64,
	errorKind string,
	rangeRequested bool,
	started time.Time,
) {
	duration := time.Since(started)
	handler.metrics.RecordRequest(request.Context(), proxy.RequestMetric{
		Method:          request.Method,
		Route:           route,
		StatusCode:      status,
		BodySize:        bodySize,
		Duration:        duration,
		Scheme:          requestScheme(request),
		ProtocolVersion: strconv.Itoa(request.ProtoMajor) + "." + strconv.Itoa(request.ProtoMinor),
		ErrorKind:       errorKind,
	})
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("http.request.method", request.Method),
		slog.String("http.route", route),
		slog.Int("http.response.status_code", status),
		slog.Int64("http.response.body.size", bodySize),
		slog.Bool("range_requested", rangeRequested),
	}
	if errorKind != "" {
		attributes = append(attributes, slog.String("error.kind", errorKind))
	}
	attributes = append(attributes, slog.Int64("duration_ms", duration.Milliseconds()))
	level := slog.LevelInfo
	if route == "health" || route == "readiness" {
		level = slog.LevelDebug
	}
	handler.logger.LogAttrs(request.Context(), level, "request completed", attributes...)
}

// requestScheme returns the bounded transport scheme used by standard HTTP metrics.
func requestScheme(request *http.Request) string {
	if request.TLS != nil {
		return "https"
	}
	return "http"
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
	if attributes.Expires != "" {
		header.Set("Expires", attributes.Expires)
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
		return nil, nil //nolint:nilnil // A nil time represents an absent HTTP condition.
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
		if weak, found := strings.CutPrefix(item, "W/"); found {
			item = weak
		}
		if len(item) < 2 || item[0] != '"' || item[len(item)-1] != '"' ||
			strings.ContainsAny(item[1:len(item)-1], "\"\\\r\n") {
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
	buffer := make([]byte, requestIDBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(buffer)
}

// Server runs the HTTP adapter until cancellation or listener failure.
type Server struct {
	server        *http.Server
	shutdownDelay time.Duration
	shutdownGrace time.Duration
	readiness     *Readiness
	logger        *slog.Logger
}

// ServerOptions configures listener limits and graceful draining behavior.
type ServerOptions struct {
	// ReadHeaderTimeout bounds client request-header reads.
	ReadHeaderTimeout time.Duration
	// IdleTimeout bounds idle keep-alive connections.
	IdleTimeout time.Duration
	// MaxHeaderBytes bounds incoming client request headers.
	MaxHeaderBytes int
	// ShutdownDelay waits for load balancers to observe unready state.
	ShutdownDelay time.Duration
	// ShutdownGrace bounds active stream draining.
	ShutdownGrace time.Duration
	// Readiness is marked draining before the listener stops accepting connections.
	Readiness *Readiness
	// Logger receives lifecycle records; nil discards them.
	Logger *slog.Logger
}

// NewServer constructs a plain-HTTP trusted-boundary listener.
func NewServer(listen string, handler http.Handler) *Server {
	return NewServerWithOptions(listen, handler, ServerOptions{ReadHeaderTimeout: phaseTwoReadHeaderTimeout})
}

// NewServerWithOptions constructs a listener with explicit production limits and drain timing.
func NewServerWithOptions(listen string, handler http.Handler, options ServerOptions) *Server {
	if options.ReadHeaderTimeout <= 0 {
		options.ReadHeaderTimeout = phaseTwoReadHeaderTimeout
	}
	if options.IdleTimeout <= 0 {
		options.IdleTimeout = time.Minute
	}
	if options.MaxHeaderBytes <= 0 {
		options.MaxHeaderBytes = defaultMaxHeaderBytes
	}
	if options.ShutdownGrace <= 0 {
		options.ShutdownGrace = defaultShutdownGrace
	}
	if options.Logger == nil {
		options.Logger = slog.New(slog.DiscardHandler)
	}
	options.Logger = options.Logger.With("component", "httpserver")
	return &Server{
		server: &http.Server{
			Addr:              listen,
			Handler:           handler,
			ReadHeaderTimeout: options.ReadHeaderTimeout,
			IdleTimeout:       options.IdleTimeout,
			MaxHeaderBytes:    options.MaxHeaderBytes,
			ErrorLog:          slog.NewLogLogger(options.Logger.Handler(), slog.LevelError),
		},
		shutdownDelay: options.ShutdownDelay,
		shutdownGrace: options.ShutdownGrace,
		readiness:     options.Readiness,
		logger:        options.Logger,
	}
}

// Run serves until ctx is canceled or the listener fails.
func (server *Server) Run(ctx context.Context) error {
	result := make(chan error, 1)
	readinessContext, stopReadiness := context.WithCancel(context.Background())
	defer stopReadiness()
	if server.readiness != nil {
		go server.readiness.Start(readinessContext)
	}
	server.logger.InfoContext(ctx, "server_starting")
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", server.server.Addr)
	if err != nil {
		return err
	}
	go func() { result <- server.server.Serve(listener) }()
	server.logger.InfoContext(ctx, "server_listening", "listen", listener.Addr().String())
	select {
	case <-ctx.Done():
		server.readiness.SetDraining()
		server.logger.InfoContext(ctx, "shutdown_started")
		if server.shutdownDelay > 0 {
			select {
			case <-time.After(server.shutdownDelay):
			case err := <-result:
				if !errors.Is(err, http.ErrServerClosed) {
					return err
				}
			}
		}
		shutdownContext, cancel := context.WithTimeout(context.Background(), server.shutdownGrace)
		defer cancel()
		if err := server.server.Shutdown(shutdownContext); err != nil {
			server.logger.ErrorContext(ctx, "shutdown_forced")
			if closeErr := server.server.Close(); closeErr != nil {
				return errors.Join(err, closeErr)
			}
			return err
		}
		server.logger.InfoContext(ctx, "shutdown_completed")
		return nil
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
