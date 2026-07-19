// Package publish adapts the publish application service to Cobra and Viper.
package publish

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/meigma/simplestreams-s3/internal/config"
	application "github.com/meigma/simplestreams-s3/internal/publish"
)

const publishArgumentCount = 2

// Runner executes one validated publish command.
type Runner func(context.Context, config.Publish, application.Request) (application.Result, error)

// NewCommand constructs the publish METADATA_TARBALL DISK_QCOW2 adapter.
func NewCommand(run Runner) *cobra.Command {
	vp := viper.New()
	command := &cobra.Command{
		Use:   "publish METADATA_TARBALL DISK_QCOW2",
		Short: "Publish one split Incus VM image into an empty private S3 mirror",
		Args:  cobra.ExactArgs(publishArgumentCount),
		RunE: func(command *cobra.Command, args []string) error {
			if run == nil {
				return errors.New("publish command is not configured")
			}
			runtime, err := config.LoadPublish(command, vp)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(command.Context(), runtime.Timeout)
			defer cancel()
			result, err := run(ctx, runtime, application.Request{
				MetadataPath: args[0],
				DiskPath:     args[1],
				Aliases:      runtime.Aliases,
				ReleaseTitle: runtime.ReleaseTitle,
			})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(
				command.OutOrStdout(),
				"published %s version %s\n",
				result.ProductName.String(),
				result.VersionID.String(),
			)
			return err
		},
	}
	addConfigFlag(command.Flags())
	addS3Flags(command.Flags())
	command.Flags().StringSlice("alias", nil, "additional Incus alias (repeatable)")
	command.Flags().String("release-title", "", "release title override")
	command.Flags().Duration("publish-timeout", config.DefaultPublishTimeout, "overall publication deadline")
	command.Flags().Duration("catalog-timeout", config.DefaultCatalogTimeout, "catalog operation and cleanup deadline")
	return command
}

// addConfigFlag adds the explicit optional YAML selector.
func addConfigFlag(flags *pflag.FlagSet) {
	flags.String("config", "", "optional YAML configuration file")
}

// addS3Flags adds the shared Phase 2 private-bucket settings.
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
