// Package cli constructs the thin Cobra command adapters.
package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	cliproxy "github.com/meigma/simplestreams-s3/internal/cli/proxy"
	clipublish "github.com/meigma/simplestreams-s3/internal/cli/publish"
)

const versionCommandName = "version"

// BuildInfo describes linker-injected build metadata.
type BuildInfo struct {
	// Version is the release version.
	Version string
	// Commit is the source commit used to build the binary.
	Commit string
	// Date is the build timestamp.
	Date string
}

// Options customizes root command construction and dependency wiring.
type Options struct {
	// In receives interactive command input.
	In io.Reader
	// Out receives command output.
	Out io.Writer
	// Err receives diagnostics.
	Err io.Writer
	// Build controls version output.
	Build BuildInfo
	// Publish executes the publication application service.
	Publish clipublish.Runner
	// Proxy executes the proxy application service.
	Proxy cliproxy.Runner
}

// NewRootCommand creates the simplestreams-s3 Cobra command tree.
func NewRootCommand(options Options) *cobra.Command {
	options = withDefaults(options)
	root := &cobra.Command{
		Use:           "simplestreams-s3",
		Short:         "Publish Incus VM images to private S3 and proxy their Simple Streams catalog",
		Version:       options.Build.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	root.SetVersionTemplate(versionLine(options.Build))
	root.SetIn(options.In)
	root.SetOut(options.Out)
	root.SetErr(options.Err)
	root.AddCommand(
		clipublish.NewCommand(options.Publish),
		cliproxy.NewCommand(options.Proxy),
		newVersionCommand(options.Build),
	)
	return root
}

// withDefaults supplies inert streams and development build metadata.
func withDefaults(options Options) Options {
	if options.In == nil {
		options.In = strings.NewReader("")
	}
	if options.Out == nil {
		options.Out = io.Discard
	}
	if options.Err == nil {
		options.Err = io.Discard
	}
	if strings.TrimSpace(options.Build.Version) == "" {
		options.Build.Version = "dev"
	}
	if strings.TrimSpace(options.Build.Commit) == "" {
		options.Build.Commit = "none"
	}
	if strings.TrimSpace(options.Build.Date) == "" {
		options.Build.Date = "unknown"
	}
	return options
}

// newVersionCommand constructs the explicit version subcommand.
func newVersionCommand(build BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   versionCommandName,
		Short: "Print build version information",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(command.OutOrStdout(), versionLine(build))
			return err
		},
	}
}

// versionLine renders the stable human-readable build identity.
func versionLine(build BuildInfo) string {
	return fmt.Sprintf("simplestreams-s3 %s (%s) built %s\n", build.Version, build.Commit, build.Date)
}
