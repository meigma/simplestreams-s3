// Package config loads raw command settings and validates immutable runtime configuration.
package config

import (
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/meigma/simplestreams-s3/internal/catalog"
	"github.com/meigma/simplestreams-s3/internal/failure"
	"github.com/meigma/simplestreams-s3/internal/object"
)

const configEnvironment = "SIMPLESTREAMS_S3_CONFIG"

const (
	// DefaultS3MaxAttempts is the normative AWS SDK request-attempt limit.
	DefaultS3MaxAttempts = 3
	// DefaultS3MaxBackoff is the normative maximum retry delay.
	DefaultS3MaxBackoff = time.Second
	// DefaultS3DialTimeout is the normative connection-establishment limit.
	DefaultS3DialTimeout = 3 * time.Second
	// DefaultS3TLSHandshakeTimeout is the normative verified-TLS negotiation limit.
	DefaultS3TLSHandshakeTimeout = 5 * time.Second
	// DefaultS3ResponseHeaderTimeout is the normative upstream header wait limit.
	DefaultS3ResponseHeaderTimeout = 5 * time.Second
	// DefaultPublishTimeout is the normative whole-command deadline.
	DefaultPublishTimeout = 2 * time.Hour
	// DefaultCatalogTimeout is the normative catalog-operation and cleanup deadline.
	DefaultCatalogTimeout = 30 * time.Second
	// DefaultCatalogAttempts is the normative compare-and-swap attempt limit.
	DefaultCatalogAttempts = 4
	// DefaultProxyMaxStreams is the normative concurrent S3-backed stream limit.
	DefaultProxyMaxStreams = 64
	// DefaultProxyReadHeaderTimeout bounds client request-header reads.
	DefaultProxyReadHeaderTimeout = 5 * time.Second
	// DefaultProxyIdleTimeout bounds idle keep-alive connections.
	DefaultProxyIdleTimeout = 60 * time.Second
	// DefaultProxyUpstreamIdleTimeout bounds no-progress S3 body reads.
	DefaultProxyUpstreamIdleTimeout = 30 * time.Second
	// DefaultProxyWriteIdleTimeout bounds no-progress client writes.
	DefaultProxyWriteIdleTimeout = 30 * time.Second
	// DefaultProxyMaxHeaderBytes bounds incoming request headers.
	DefaultProxyMaxHeaderBytes = 32768
	// DefaultProxyShutdownDelay waits for load balancers to observe unready state.
	DefaultProxyShutdownDelay = 5 * time.Second
	// DefaultProxyShutdownGrace bounds draining after listener shutdown begins.
	DefaultProxyShutdownGrace = 30 * time.Second
	// DefaultProxyReadinessInterval controls background catalog probe cadence.
	DefaultProxyReadinessInterval = 10 * time.Second
	// DefaultProxyReadinessTimeout bounds one catalog readiness probe.
	DefaultProxyReadinessTimeout = 2 * time.Second
	// DefaultProxyReadinessStaleness is the longest age of a successful readiness probe.
	DefaultProxyReadinessStaleness = 30 * time.Second
	// DefaultMetricsInterval controls periodic OTLP metric export.
	DefaultMetricsInterval = 30 * time.Second
	// DefaultMetricsTimeout bounds each OTLP export and shutdown flush.
	DefaultMetricsTimeout = 10 * time.Second
)

// S3 contains validated settings shared by publisher and proxy adapters.
type S3 struct {
	// Bucket is the private general-purpose S3 bucket.
	Bucket object.BucketName
	// Prefix is the optional owned mirror-root key prefix.
	Prefix object.KeyPrefix
	// Region overrides AWS region discovery when non-empty.
	Region string
	// Profile selects an AWS shared-config profile when non-empty.
	Profile string
	// ExpectedBucketOwner adds the S3 confused-deputy ownership check when non-empty.
	ExpectedBucketOwner string
	// MaxAttempts bounds AWS SDK request attempts.
	MaxAttempts int
	// MaxBackoff bounds AWS SDK retry delay.
	MaxBackoff time.Duration
	// DialTimeout bounds network connection establishment.
	DialTimeout time.Duration
	// TLSHandshakeTimeout bounds verified TLS negotiation.
	TLSHandshakeTimeout time.Duration
	// ResponseHeaderTimeout bounds the wait for upstream response headers.
	ResponseHeaderTimeout time.Duration
}

// Publish contains validated settings for one publication invocation.
type Publish struct {
	// S3 contains the shared private-bucket settings.
	S3 S3
	// Aliases are additional normalized Incus aliases.
	Aliases []catalog.Alias
	// ReleaseTitle overrides the image release label when non-empty.
	ReleaseTitle string
	// Timeout bounds the entire publish command.
	Timeout time.Duration
	// CatalogTimeout bounds catalog writes and multipart failure cleanup.
	CatalogTimeout time.Duration
	// CatalogAttempts bounds compare-and-swap publication attempts.
	CatalogAttempts int
}

// Proxy contains validated settings for the production object-serving process.
type Proxy struct {
	// S3 contains the shared private-bucket settings.
	S3 S3
	// Listen is the plain-HTTP trusted-boundary listener address.
	Listen string
	// MaxStreams bounds concurrent S3-backed object streams.
	MaxStreams int
	// ReadHeaderTimeout bounds client request-header reads.
	ReadHeaderTimeout time.Duration
	// IdleTimeout bounds idle keep-alive connections.
	IdleTimeout time.Duration
	// UpstreamIdleTimeout bounds no-progress S3 body reads.
	UpstreamIdleTimeout time.Duration
	// WriteIdleTimeout bounds no-progress downstream writes.
	WriteIdleTimeout time.Duration
	// MaxHeaderBytes bounds client request header size.
	MaxHeaderBytes int
	// ShutdownDelay gives load balancers time to observe draining readiness.
	ShutdownDelay time.Duration
	// ShutdownGrace bounds active stream draining.
	ShutdownGrace time.Duration
	// ReadinessInterval controls catalog probe cadence.
	ReadinessInterval time.Duration
	// ReadinessTimeout bounds one catalog probe.
	ReadinessTimeout time.Duration
	// ReadinessStaleness bounds the age of a successful catalog probe.
	ReadinessStaleness time.Duration
	// LogLevel selects the structured proxy logging threshold.
	LogLevel string
	// Metrics contains the optional OTLP/HTTP exporter settings.
	Metrics Metrics
}

// Metrics contains validated optional OTLP/HTTP exporter settings.
type Metrics struct {
	// Endpoint is the collector host and port; empty disables metrics.
	Endpoint string
	// Interval controls periodic metric collection and export.
	Interval time.Duration
	// Timeout bounds one export and the shutdown flush.
	Timeout time.Duration
	// Insecure permits cleartext OTLP only for a loopback endpoint.
	Insecure bool
}

// rawConfig is the strict YAML, flag, and environment decoding target.
type rawConfig struct {
	S3      rawS3      `mapstructure:"s3"`
	Publish rawPublish `mapstructure:"publish"`
	Proxy   rawProxy   `mapstructure:"proxy"`
	Logging rawLogging `mapstructure:"logging"`
	Metrics rawMetrics `mapstructure:"metrics"`
}

// rawS3 contains unvalidated S3 boundary values.
type rawS3 struct {
	Bucket                string        `mapstructure:"bucket"`
	Prefix                string        `mapstructure:"prefix"`
	Region                string        `mapstructure:"region"`
	Profile               string        `mapstructure:"profile"`
	ExpectedBucketOwner   string        `mapstructure:"expected_bucket_owner"`
	MaxAttempts           int           `mapstructure:"max_attempts"`
	MaxBackoff            time.Duration `mapstructure:"max_backoff"`
	DialTimeout           time.Duration `mapstructure:"dial_timeout"`
	TLSHandshakeTimeout   time.Duration `mapstructure:"tls_handshake_timeout"`
	ResponseHeaderTimeout time.Duration `mapstructure:"response_header_timeout"`
}

// rawPublish contains unvalidated publication boundary values.
type rawPublish struct {
	Aliases         []string      `mapstructure:"aliases"`
	ReleaseTitle    string        `mapstructure:"release_title"`
	Timeout         time.Duration `mapstructure:"timeout"`
	CatalogTimeout  time.Duration `mapstructure:"catalog_timeout"`
	CatalogAttempts int           `mapstructure:"catalog_attempts"`
}

// rawProxy contains unvalidated proxy boundary values.
type rawProxy struct {
	Listen              string        `mapstructure:"listen"`
	MaxStreams          int           `mapstructure:"max_streams"`
	ReadHeaderTimeout   time.Duration `mapstructure:"read_header_timeout"`
	IdleTimeout         time.Duration `mapstructure:"idle_timeout"`
	UpstreamIdleTimeout time.Duration `mapstructure:"upstream_idle_timeout"`
	WriteIdleTimeout    time.Duration `mapstructure:"write_idle_timeout"`
	MaxHeaderBytes      int           `mapstructure:"max_header_bytes"`
	ShutdownDelay       time.Duration `mapstructure:"shutdown_delay"`
	ShutdownGrace       time.Duration `mapstructure:"shutdown_grace"`
	ReadinessInterval   time.Duration `mapstructure:"readiness_interval"`
	ReadinessTimeout    time.Duration `mapstructure:"readiness_timeout"`
	ReadinessStaleness  time.Duration `mapstructure:"readiness_staleness"`
}

// rawLogging contains unvalidated proxy logging boundary values.
type rawLogging struct {
	Level string `mapstructure:"level"`
}

// rawMetrics contains unvalidated OTLP exporter boundary values.
type rawMetrics struct {
	Endpoint string        `mapstructure:"endpoint"`
	Interval time.Duration `mapstructure:"interval"`
	Timeout  time.Duration `mapstructure:"timeout"`
	Insecure bool          `mapstructure:"insecure"`
}

// binding maps one Viper key to one Cobra flag and stable environment variable.
type binding struct {
	key      string
	flag     string
	env      string
	defaultV any
}

// LoadPublish resolves and validates the Phase 2 publisher configuration.
func LoadPublish(command *cobra.Command, vp *viper.Viper) (Publish, error) {
	raw, err := load(command, vp, append(s3Bindings(), publishBindings()...))
	if err != nil {
		return Publish{}, err
	}
	s3Config, err := validateS3(raw.S3)
	if err != nil {
		return Publish{}, err
	}
	aliases := make([]catalog.Alias, 0, len(raw.Publish.Aliases))
	for _, value := range raw.Publish.Aliases {
		alias, aliasErr := catalog.NewAlias(value)
		if aliasErr != nil {
			return Publish{}, failure.Wrap(failure.KindInvalidInput, "validate publish.aliases", aliasErr)
		}
		aliases = append(aliases, alias)
	}
	if err := validatePositiveDuration("publish.timeout", raw.Publish.Timeout); err != nil {
		return Publish{}, err
	}
	if err := validatePositiveDuration("publish.catalog_timeout", raw.Publish.CatalogTimeout); err != nil {
		return Publish{}, err
	}
	if raw.Publish.CatalogAttempts < 1 {
		return Publish{}, failure.New(
			failure.KindInvalidInput,
			"validate publish.catalog_attempts",
			"value must be positive",
		)
	}
	return Publish{
		S3:              s3Config,
		Aliases:         aliases,
		ReleaseTitle:    strings.TrimSpace(raw.Publish.ReleaseTitle),
		Timeout:         raw.Publish.Timeout,
		CatalogTimeout:  raw.Publish.CatalogTimeout,
		CatalogAttempts: raw.Publish.CatalogAttempts,
	}, nil
}

// LoadProxy resolves and validates the production proxy configuration.
func LoadProxy(command *cobra.Command, vp *viper.Viper) (Proxy, error) {
	bindings := append(s3Bindings(), proxyBindings()...)
	bindings = append(bindings, loggingBindings()...)
	bindings = append(bindings, metricsBindings()...)
	raw, loadErr := load(command, vp, bindings)
	if loadErr != nil {
		return Proxy{}, loadErr
	}
	s3Config, s3Err := validateS3(raw.S3)
	if s3Err != nil {
		return Proxy{}, s3Err
	}
	listen := strings.TrimSpace(raw.Proxy.Listen)
	if listen == "" {
		return Proxy{}, failure.New(failure.KindInvalidInput, "validate proxy.listen", "listen address is required")
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return Proxy{}, failure.Wrap(failure.KindInvalidInput, "validate proxy.listen", err)
	}
	if raw.Proxy.MaxStreams < 1 || raw.Proxy.MaxHeaderBytes < 1 {
		return Proxy{}, failure.New(
			failure.KindInvalidInput,
			"validate proxy limits",
			"stream and header limits must be positive",
		)
	}
	for name, duration := range map[string]time.Duration{
		"proxy.read_header_timeout":   raw.Proxy.ReadHeaderTimeout,
		"proxy.idle_timeout":          raw.Proxy.IdleTimeout,
		"proxy.upstream_idle_timeout": raw.Proxy.UpstreamIdleTimeout,
		"proxy.write_idle_timeout":    raw.Proxy.WriteIdleTimeout,
		"proxy.shutdown_delay":        raw.Proxy.ShutdownDelay,
		"proxy.shutdown_grace":        raw.Proxy.ShutdownGrace,
		"proxy.readiness_interval":    raw.Proxy.ReadinessInterval,
		"proxy.readiness_timeout":     raw.Proxy.ReadinessTimeout,
		"proxy.readiness_staleness":   raw.Proxy.ReadinessStaleness,
	} {
		if err := validatePositiveDuration(name, duration); err != nil {
			return Proxy{}, err
		}
	}
	if raw.Proxy.ReadinessStaleness < raw.Proxy.ReadinessInterval {
		return Proxy{}, failure.New(
			failure.KindInvalidInput,
			"validate proxy.readiness_staleness",
			"staleness must not be shorter than the probe interval",
		)
	}
	level := strings.ToLower(strings.TrimSpace(raw.Logging.Level))
	if level != "debug" && level != "info" && level != "warn" && level != "error" {
		return Proxy{}, failure.New(
			failure.KindInvalidInput,
			"validate logging.level",
			"level must be debug, info, warn, or error",
		)
	}
	metrics, metricsErr := validateMetrics(raw.Metrics)
	if metricsErr != nil {
		return Proxy{}, metricsErr
	}
	return Proxy{
		S3:                  s3Config,
		Listen:              listen,
		MaxStreams:          raw.Proxy.MaxStreams,
		ReadHeaderTimeout:   raw.Proxy.ReadHeaderTimeout,
		IdleTimeout:         raw.Proxy.IdleTimeout,
		UpstreamIdleTimeout: raw.Proxy.UpstreamIdleTimeout,
		WriteIdleTimeout:    raw.Proxy.WriteIdleTimeout,
		MaxHeaderBytes:      raw.Proxy.MaxHeaderBytes,
		ShutdownDelay:       raw.Proxy.ShutdownDelay,
		ShutdownGrace:       raw.Proxy.ShutdownGrace,
		ReadinessInterval:   raw.Proxy.ReadinessInterval,
		ReadinessTimeout:    raw.Proxy.ReadinessTimeout,
		ReadinessStaleness:  raw.Proxy.ReadinessStaleness,
		LogLevel:            level,
		Metrics:             metrics,
	}, nil
}

// load applies defaults, optional strict YAML, explicit environment keys, and command flags.
func load(command *cobra.Command, vp *viper.Viper, bindings []binding) (rawConfig, error) {
	if command == nil || vp == nil {
		return rawConfig{}, failure.New(failure.KindInternal, "load configuration", "command and Viper are required")
	}
	for _, item := range bindings {
		vp.SetDefault(item.key, item.defaultV)
		if err := vp.BindEnv(item.key, item.env); err != nil {
			return rawConfig{}, failure.Wrap(failure.KindInternal, "bind environment", err)
		}
		flag := command.Flags().Lookup(item.flag)
		if flag == nil {
			return rawConfig{}, failure.New(failure.KindInternal, "bind configuration flag", "missing flag "+item.flag)
		}
		if err := vp.BindPFlag(item.key, flag); err != nil {
			return rawConfig{}, failure.Wrap(failure.KindInternal, "bind configuration flag", err)
		}
	}
	configPath, err := selectedConfigPath(command.Flags())
	if err != nil {
		return rawConfig{}, err
	}
	if configPath != "" {
		vp.SetConfigFile(configPath)
		vp.SetConfigType("yaml")
		if err := vp.ReadInConfig(); err != nil {
			return rawConfig{}, failure.Wrap(failure.KindInvalidInput, "read configuration file", err)
		}
	}
	var raw rawConfig
	if err := vp.UnmarshalExact(&raw); err != nil {
		return rawConfig{}, failure.Wrap(failure.KindInvalidInput, "decode configuration", err)
	}
	return raw, nil
}

// selectedConfigPath resolves the config selector with flag-over-environment precedence.
func selectedConfigPath(flags *pflag.FlagSet) (string, error) {
	flag := flags.Lookup("config")
	if flag == nil {
		return "", failure.New(failure.KindInternal, "select configuration file", "config flag is missing")
	}
	if flag.Changed {
		value, err := flags.GetString("config")
		if err != nil {
			return "", failure.Wrap(failure.KindInternal, "read config flag", err)
		}
		return value, nil
	}
	return os.Getenv(configEnvironment), nil
}

// validateS3 constructs strong S3 identities and checks bounded transport settings.
func validateS3(raw rawS3) (S3, error) {
	bucket, err := object.NewBucketName(raw.Bucket)
	if err != nil {
		return S3{}, failure.Wrap(failure.KindInvalidInput, "validate s3.bucket", err)
	}
	prefix, err := object.ParseKeyPrefix(raw.Prefix)
	if err != nil {
		return S3{}, failure.Wrap(failure.KindInvalidInput, "validate s3.prefix", err)
	}
	if raw.MaxAttempts < 1 {
		return S3{}, failure.New(failure.KindInvalidInput, "validate s3.max_attempts", "value must be positive")
	}
	for name, duration := range map[string]time.Duration{
		"s3.max_backoff":             raw.MaxBackoff,
		"s3.dial_timeout":            raw.DialTimeout,
		"s3.tls_handshake_timeout":   raw.TLSHandshakeTimeout,
		"s3.response_header_timeout": raw.ResponseHeaderTimeout,
	} {
		if err := validatePositiveDuration(name, duration); err != nil {
			return S3{}, err
		}
	}
	return S3{
		Bucket:                bucket,
		Prefix:                prefix,
		Region:                strings.TrimSpace(raw.Region),
		Profile:               strings.TrimSpace(raw.Profile),
		ExpectedBucketOwner:   strings.TrimSpace(raw.ExpectedBucketOwner),
		MaxAttempts:           raw.MaxAttempts,
		MaxBackoff:            raw.MaxBackoff,
		DialTimeout:           raw.DialTimeout,
		TLSHandshakeTimeout:   raw.TLSHandshakeTimeout,
		ResponseHeaderTimeout: raw.ResponseHeaderTimeout,
	}, nil
}

// validatePositiveDuration rejects disabled or unbounded Phase 2 operation settings.
func validatePositiveDuration(name string, value time.Duration) error {
	if value <= 0 {
		return failure.New(failure.KindInvalidInput, "validate "+name, "duration must be positive")
	}
	return nil
}

// validateMetrics enforces bounded export and loopback-only cleartext transport.
func validateMetrics(raw rawMetrics) (Metrics, error) {
	if err := validatePositiveDuration("metrics.interval", raw.Interval); err != nil {
		return Metrics{}, err
	}
	if err := validatePositiveDuration("metrics.timeout", raw.Timeout); err != nil {
		return Metrics{}, err
	}
	endpoint := strings.TrimSpace(raw.Endpoint)
	if endpoint == "" {
		return Metrics{Interval: raw.Interval, Timeout: raw.Timeout, Insecure: raw.Insecure}, nil
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" || port == "" {
		return Metrics{}, failure.New(
			failure.KindInvalidInput,
			"validate metrics.endpoint",
			"endpoint must be a host:port without a scheme or path",
		)
	}
	if raw.Insecure && !loopbackHost(host) {
		return Metrics{}, failure.New(
			failure.KindInvalidInput,
			"validate metrics.insecure",
			"cleartext OTLP requires a loopback endpoint",
		)
	}
	return Metrics{Endpoint: endpoint, Interval: raw.Interval, Timeout: raw.Timeout, Insecure: raw.Insecure}, nil
}

// loopbackHost recognizes explicit local-development collector identities without DNS resolution.
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// s3Bindings returns the stable shared source mappings and normative defaults.
func s3Bindings() []binding {
	return []binding{
		{key: "s3.bucket", flag: "s3-bucket", env: "SIMPLESTREAMS_S3_BUCKET", defaultV: ""},
		{key: "s3.prefix", flag: "s3-prefix", env: "SIMPLESTREAMS_S3_PREFIX", defaultV: ""},
		{key: "s3.region", flag: "s3-region", env: "SIMPLESTREAMS_S3_REGION", defaultV: ""},
		{key: "s3.profile", flag: "s3-profile", env: "SIMPLESTREAMS_S3_PROFILE", defaultV: ""},
		{
			key:      "s3.expected_bucket_owner",
			flag:     "s3-expected-bucket-owner",
			env:      "SIMPLESTREAMS_S3_EXPECTED_BUCKET_OWNER",
			defaultV: "",
		},
		{
			key:      "s3.max_attempts",
			flag:     "s3-max-attempts",
			env:      "SIMPLESTREAMS_S3_MAX_ATTEMPTS",
			defaultV: DefaultS3MaxAttempts,
		},
		{
			key:      "s3.max_backoff",
			flag:     "s3-max-backoff",
			env:      "SIMPLESTREAMS_S3_MAX_BACKOFF",
			defaultV: DefaultS3MaxBackoff,
		},
		{
			key:      "s3.dial_timeout",
			flag:     "s3-dial-timeout",
			env:      "SIMPLESTREAMS_S3_DIAL_TIMEOUT",
			defaultV: DefaultS3DialTimeout,
		},
		{
			key:      "s3.tls_handshake_timeout",
			flag:     "s3-tls-handshake-timeout",
			env:      "SIMPLESTREAMS_S3_TLS_HANDSHAKE_TIMEOUT",
			defaultV: DefaultS3TLSHandshakeTimeout,
		},
		{
			key:      "s3.response_header_timeout",
			flag:     "s3-response-header-timeout",
			env:      "SIMPLESTREAMS_S3_RESPONSE_HEADER_TIMEOUT",
			defaultV: DefaultS3ResponseHeaderTimeout,
		},
	}
}

// publishBindings returns the stable Phase 2 publisher source mappings.
func publishBindings() []binding {
	return []binding{
		{key: "publish.aliases", flag: "alias", env: "SIMPLESTREAMS_S3_ALIASES", defaultV: []string{}},
		{key: "publish.release_title", flag: "release-title", env: "SIMPLESTREAMS_S3_RELEASE_TITLE", defaultV: ""},
		{
			key:      "publish.timeout",
			flag:     "publish-timeout",
			env:      "SIMPLESTREAMS_S3_PUBLISH_TIMEOUT",
			defaultV: DefaultPublishTimeout,
		},
		{
			key:      "publish.catalog_timeout",
			flag:     "catalog-timeout",
			env:      "SIMPLESTREAMS_S3_CATALOG_TIMEOUT",
			defaultV: DefaultCatalogTimeout,
		},
		{
			key:      "publish.catalog_attempts",
			flag:     "catalog-attempts",
			env:      "SIMPLESTREAMS_S3_CATALOG_ATTEMPTS",
			defaultV: DefaultCatalogAttempts,
		},
	}
}

// proxyBindings returns the stable Phase 2 proxy source mappings.
func proxyBindings() []binding {
	return []binding{
		{key: "proxy.listen", flag: "listen", env: "SIMPLESTREAMS_S3_LISTEN", defaultV: ":8080"},
		{
			key:      "proxy.max_streams",
			flag:     "max-streams",
			env:      "SIMPLESTREAMS_S3_MAX_STREAMS",
			defaultV: DefaultProxyMaxStreams,
		},
		{
			key:      "proxy.read_header_timeout",
			flag:     "read-header-timeout",
			env:      "SIMPLESTREAMS_S3_READ_HEADER_TIMEOUT",
			defaultV: DefaultProxyReadHeaderTimeout,
		},
		{
			key:      "proxy.idle_timeout",
			flag:     "idle-timeout",
			env:      "SIMPLESTREAMS_S3_IDLE_TIMEOUT",
			defaultV: DefaultProxyIdleTimeout,
		},
		{
			key:      "proxy.upstream_idle_timeout",
			flag:     "upstream-idle-timeout",
			env:      "SIMPLESTREAMS_S3_UPSTREAM_IDLE_TIMEOUT",
			defaultV: DefaultProxyUpstreamIdleTimeout,
		},
		{
			key:      "proxy.write_idle_timeout",
			flag:     "write-idle-timeout",
			env:      "SIMPLESTREAMS_S3_WRITE_IDLE_TIMEOUT",
			defaultV: DefaultProxyWriteIdleTimeout,
		},
		{
			key:      "proxy.max_header_bytes",
			flag:     "max-header-bytes",
			env:      "SIMPLESTREAMS_S3_MAX_HEADER_BYTES",
			defaultV: DefaultProxyMaxHeaderBytes,
		},
		{
			key:      "proxy.shutdown_delay",
			flag:     "shutdown-delay",
			env:      "SIMPLESTREAMS_S3_SHUTDOWN_DELAY",
			defaultV: DefaultProxyShutdownDelay,
		},
		{
			key:      "proxy.shutdown_grace",
			flag:     "shutdown-grace",
			env:      "SIMPLESTREAMS_S3_SHUTDOWN_GRACE",
			defaultV: DefaultProxyShutdownGrace,
		},
		{
			key:      "proxy.readiness_interval",
			flag:     "readiness-interval",
			env:      "SIMPLESTREAMS_S3_READINESS_INTERVAL",
			defaultV: DefaultProxyReadinessInterval,
		},
		{
			key:      "proxy.readiness_timeout",
			flag:     "readiness-timeout",
			env:      "SIMPLESTREAMS_S3_READINESS_TIMEOUT",
			defaultV: DefaultProxyReadinessTimeout,
		},
		{
			key:      "proxy.readiness_staleness",
			flag:     "readiness-staleness",
			env:      "SIMPLESTREAMS_S3_READINESS_STALENESS",
			defaultV: DefaultProxyReadinessStaleness,
		},
	}
}

// loggingBindings returns source mappings for structured proxy logging.
func loggingBindings() []binding {
	return []binding{{key: "logging.level", flag: "log-level", env: "SIMPLESTREAMS_S3_LOG_LEVEL", defaultV: "info"}}
}

// metricsBindings returns the stable optional OTLP source mappings and normative defaults.
func metricsBindings() []binding {
	return []binding{
		{key: "metrics.endpoint", flag: "metrics-endpoint", env: "SIMPLESTREAMS_S3_METRICS_ENDPOINT", defaultV: ""},
		{
			key:      "metrics.interval",
			flag:     "metrics-interval",
			env:      "SIMPLESTREAMS_S3_METRICS_INTERVAL",
			defaultV: DefaultMetricsInterval,
		},
		{
			key:      "metrics.timeout",
			flag:     "metrics-timeout",
			env:      "SIMPLESTREAMS_S3_METRICS_TIMEOUT",
			defaultV: DefaultMetricsTimeout,
		},
		{key: "metrics.insecure", flag: "metrics-insecure", env: "SIMPLESTREAMS_S3_METRICS_INSECURE", defaultV: false},
	}
}
