package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/meigma/simplestreams-s3/internal/adapter/httpserver"
	"github.com/meigma/simplestreams-s3/internal/adapter/s3store"
	"github.com/meigma/simplestreams-s3/internal/cli"
	"github.com/meigma/simplestreams-s3/internal/config"
	applicationproxy "github.com/meigma/simplestreams-s3/internal/proxy"
	applicationpublish "github.com/meigma/simplestreams-s3/internal/publish"
)

//nolint:gochecknoglobals // GoReleaser injects these values with ldflags during releases.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// main maps the composition-root exit status to the process.
func main() {
	os.Exit(run())
}

// run establishes signal cancellation, composes adapters, and executes the root command.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCommand(cli.Options{
		In: os.Stdin,
		Build: cli.BuildInfo{
			Version: version,
			Commit:  commit,
			Date:    date,
		},
		Out:     os.Stdout,
		Err:     os.Stderr,
		Publish: publishImage,
		Proxy:   serveProxy,
	})
	if err := root.ExecuteContext(ctx); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, err); writeErr != nil {
			return 1
		}

		return 1
	}

	return 0
}

// publishImage composes the AWS adapter with the optimistic publication service.
func publishImage(
	ctx context.Context,
	runtime config.Publish,
	request applicationpublish.Request,
) (applicationpublish.Result, error) {
	store, err := s3store.New(ctx, runtime.S3, runtime.CatalogTimeout)
	if err != nil {
		return applicationpublish.Result{}, err
	}
	service := applicationpublish.NewService(store, runtime.S3.Prefix, applicationpublish.Options{
		CatalogAttempts: runtime.CatalogAttempts,
		CatalogTimeout:  runtime.CatalogTimeout,
	})
	return service.Publish(ctx, request)
}

// serveProxy composes the AWS adapter, proxy service, and plain-HTTP server.
func serveProxy(ctx context.Context, runtime config.Proxy) error {
	store, err := s3store.New(ctx, runtime.S3, config.DefaultCatalogTimeout)
	if err != nil {
		return err
	}
	service := applicationproxy.NewService(store, runtime.S3.Prefix)
	handler := httpserver.NewHandler(service)
	return httpserver.NewServer(runtime.Listen, handler).Run(ctx)
}
