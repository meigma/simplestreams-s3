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

// Proxy contains validated settings for the Phase 2 object-serving process.
type Proxy struct {
	// S3 contains the shared private-bucket settings.
	S3 S3
	// Listen is the plain-HTTP trusted-boundary listener address.
	Listen string
}

// rawConfig is the strict YAML, flag, and environment decoding target.
type rawConfig struct {
	S3      rawS3      `mapstructure:"s3"`
	Publish rawPublish `mapstructure:"publish"`
	Proxy   rawProxy   `mapstructure:"proxy"`
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
	Listen string `mapstructure:"listen"`
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

// LoadProxy resolves and validates the Phase 2 proxy configuration.
func LoadProxy(command *cobra.Command, vp *viper.Viper) (Proxy, error) {
	raw, err := load(command, vp, append(s3Bindings(), proxyBindings()...))
	if err != nil {
		return Proxy{}, err
	}
	s3Config, err := validateS3(raw.S3)
	if err != nil {
		return Proxy{}, err
	}
	listen := strings.TrimSpace(raw.Proxy.Listen)
	if listen == "" {
		return Proxy{}, failure.New(failure.KindInvalidInput, "validate proxy.listen", "listen address is required")
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return Proxy{}, failure.Wrap(failure.KindInvalidInput, "validate proxy.listen", err)
	}
	return Proxy{S3: s3Config, Listen: listen}, nil
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
	return []binding{{key: "proxy.listen", flag: "listen", env: "SIMPLESTREAMS_S3_LISTEN", defaultV: ":8080"}}
}
