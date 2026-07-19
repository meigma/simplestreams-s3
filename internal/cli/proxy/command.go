// Package proxy adapts the proxy application service to Cobra and Viper.
package proxy

import (
	"context"
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/meigma/simplestreams-s3/internal/config"
)

// Runner executes the validated proxy process until cancellation or listener failure.
type Runner func(context.Context, config.Proxy) error

// NewCommand constructs the long-running proxy adapter.
func NewCommand(run Runner) *cobra.Command {
	vp := viper.New()
	command := &cobra.Command{
		Use:   "proxy",
		Short: "Serve the configured private S3 mirror over plain HTTP",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if run == nil {
				return errors.New("proxy command is not configured")
			}
			runtime, err := config.LoadProxy(command, vp)
			if err != nil {
				return err
			}
			return run(command.Context(), runtime)
		},
	}
	addConfigFlag(command.Flags())
	addS3Flags(command.Flags())
	command.Flags().String("listen", ":8080", "plain-HTTP listener address")
	command.Flags().Int("max-streams", config.DefaultProxyMaxStreams, "maximum concurrent S3-backed object streams")
	command.Flags().
		Duration("read-header-timeout", config.DefaultProxyReadHeaderTimeout, "client request header timeout")
	command.Flags().Duration("idle-timeout", config.DefaultProxyIdleTimeout, "idle keep-alive connection timeout")
	command.Flags().
		Duration("upstream-idle-timeout", config.DefaultProxyUpstreamIdleTimeout, "upstream stream no-progress timeout")
	command.Flags().
		Duration("write-idle-timeout", config.DefaultProxyWriteIdleTimeout, "downstream write no-progress timeout")
	command.Flags().Int("max-header-bytes", config.DefaultProxyMaxHeaderBytes, "maximum client request header bytes")
	command.Flags().
		Duration("shutdown-delay", config.DefaultProxyShutdownDelay, "readiness propagation delay before shutdown")
	command.Flags().Duration("shutdown-grace", config.DefaultProxyShutdownGrace, "graceful stream drain timeout")
	command.Flags().
		Duration("readiness-interval", config.DefaultProxyReadinessInterval, "catalog readiness probe interval")
	command.Flags().
		Duration("readiness-timeout", config.DefaultProxyReadinessTimeout, "catalog readiness probe timeout")
	command.Flags().
		Duration("readiness-staleness", config.DefaultProxyReadinessStaleness, "maximum age of successful readiness probe")
	command.Flags().String("log-level", "info", "proxy JSON logging level")
	command.Flags().String("metrics-endpoint", "", "OTLP/HTTP collector host and port")
	command.Flags().Duration("metrics-interval", config.DefaultMetricsInterval, "OTLP metric export interval")
	command.Flags().Duration("metrics-timeout", config.DefaultMetricsTimeout, "OTLP export and shutdown timeout")
	command.Flags().Bool("metrics-insecure", false, "allow cleartext OTLP to a loopback collector")
	return command
}

// addConfigFlag adds the explicit optional YAML selector.
func addConfigFlag(flags *pflag.FlagSet) {
	flags.String("config", "", "optional YAML configuration file")
}

// addS3Flags adds the shared private-bucket settings.
func addS3Flags(flags *pflag.FlagSet) {
	flags.String("s3-bucket", "", "private S3 bucket name")
	flags.String("s3-prefix", "", "owned mirror-root key prefix")
	flags.String("s3-region", "", "AWS region override")
	flags.String("s3-profile", "", "AWS shared-config profile")
	flags.String("s3-expected-bucket-owner", "", "expected AWS account ID for the bucket")
	flags.Int("s3-max-attempts", config.DefaultS3MaxAttempts, "maximum AWS SDK request attempts")
	flags.Duration("s3-max-backoff", config.DefaultS3MaxBackoff, "maximum AWS SDK retry backoff")
	flags.Duration("s3-dial-timeout", config.DefaultS3DialTimeout, "S3 connection establishment timeout")
	flags.Duration("s3-tls-handshake-timeout", config.DefaultS3TLSHandshakeTimeout, "S3 TLS handshake timeout")
	flags.Duration(
		"s3-response-header-timeout",
		config.DefaultS3ResponseHeaderTimeout,
		"S3 response header timeout",
	)
}
