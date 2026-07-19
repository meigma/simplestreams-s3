# simplestreams-s3

`simplestreams-s3` publishes split Incus virtual-machine images to a private Amazon S3 bucket and serves the resulting Simple Streams mirror through authenticated S3 reads.

The Phase 2 implementation provides the first permanent vertical slice:

- `simplestreams-s3 publish METADATA_TARBALL DISK_QCOW2` validates one split VM image and publishes it into an empty mirror root;
- `simplestreams-s3 proxy` exposes exact `GET` and `HEAD` reads from that mirror over plain HTTP inside a trusted deployment boundary;
- `simplestreams-s3 version` prints linker-injected build information.

Existing-catalog merge and concurrency safety arrive in Phase 3. Production HTTP behavior, readiness, structured logging, and graceful draining arrive in Phase 4. Optional telemetry and complete operator guidance arrive in Phase 5.

## Security boundary

The bucket must remain private. Both commands authenticate to AWS through the SDK's default credential chain; static access keys are not application settings. The proxy does not authenticate downstream clients and does not terminate public TLS. Put it behind an ingress or network boundary that supplies HTTPS and the required access-control policy.

The configured bucket prefix is owned exclusively by this mirror and must not contain unrelated or sensitive objects.

## Inputs

`publish` accepts exactly:

1. an xz-compressed Incus metadata tarball containing one root `metadata.yaml`; and
2. a QCOW2 virtual-machine disk.

V1 supports `amd64`/`x86_64` and `arm64`/`aarch64`. Container images, unified images, LXD compatibility, format conversion, and catalog deletion are intentionally unsupported.

## Configuration

Settings use this precedence, highest first:

1. command flags;
2. `SIMPLESTREAMS_S3_*` environment variables;
3. the YAML file explicitly selected by `--config` or `SIMPLESTREAMS_S3_CONFIG`;
4. defaults.

There is no implicit config-file search. Unknown YAML keys and invalid values fail startup. Run command help for the settings implemented in the current slice:

```sh
go run ./cmd/simplestreams-s3 publish --help
go run ./cmd/simplestreams-s3 proxy --help
```

Example publication:

```sh
go run ./cmd/simplestreams-s3 publish \
  --s3-bucket private-images \
  --s3-region us-west-2 \
  incus.tar.xz disk.qcow2
```

Example proxy:

```sh
SIMPLESTREAMS_S3_BUCKET=private-images \
SIMPLESTREAMS_S3_REGION=us-west-2 \
go run ./cmd/simplestreams-s3 proxy --listen :8080
```

## Development

[mise](https://mise.jdx.dev) provisions the pinned toolchain from `mise.toml` and `mise.lock`. [Moon](https://moonrepo.dev) remains the task front door:

```sh
mise install
moon run root:format
moon run root:lint
moon run root:test
moon run root:check
```

The containerized S3 adapter test is opt-in and uses Testcontainers with MinIO:

```sh
SIMPLESTREAMS_S3_INTEGRATION=1 \
go test -count=1 -run TestStoreIntegrationExercisesCreateHeadAndGet ./internal/adapter/s3store
```

## Container image

The release image has no Dockerfile. Melange builds the Go binary into a signed Wolfi apk, and apko assembles the minimal non-root, multi-architecture runtime image.

```sh
mise run image-local
docker run --rm simplestreams-s3:dev version
```

## Release and verification

Release Please, GoReleaser, melange, apko, cosign, SBOM attestation, and the isolated provenance workflow remain intact under the `simplestreams-s3` product, package, binary, and image names. The aggregate local and CI gate is:

```sh
moon run root:check
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the contribution workflow and [SECURITY.md](SECURITY.md) for vulnerability reporting.
