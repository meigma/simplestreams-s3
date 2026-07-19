# simplestreams-s3

`simplestreams-s3` publishes split Incus virtual-machine images to a private Amazon S3 bucket and serves the resulting Simple Streams mirror through authenticated S3 reads.

It provides three commands:

- `simplestreams-s3 publish METADATA_TARBALL DISK_QCOW2` validates and conditionally publishes one split VM image;
- `simplestreams-s3 proxy` serves exact `GET` and `HEAD` reads with ranges, conditions, readiness, JSON logging, graceful draining, and optional OTLP metrics;
- `simplestreams-s3 version` prints linker-injected build information.

Publication is idempotent and safe under compatible concurrent updates. Conflicting aliases, metadata, catalog generations, or immutable objects fail closed without moving the active index.

## Security boundary

The S3 bucket must remain private. Both commands authenticate through the AWS SDK default credential chain; static access keys are not application settings. The proxy does not authenticate downstream clients and does not terminate public TLS. Deploy it behind an ingress or network boundary that provides HTTPS and the required access-control policy.

The configured bucket prefix is dedicated to one mirror and must not contain unrelated or sensitive objects. See the [operator guide](https://meigma.github.io/simplestreams-s3/operator-guide/) for the complete bucket, IAM, ingress, KMS, and multipart-cleanup requirements.

## Quick start

Publish one split VM image:

```sh
simplestreams-s3 publish \
  --s3-bucket private-images \
  --s3-region us-west-2 \
  incus.tar.xz disk.qcow2
```

Start the plain-HTTP proxy inside its trusted deployment boundary:

```sh
SIMPLESTREAMS_S3_BUCKET=private-images \
SIMPLESTREAMS_S3_REGION=us-west-2 \
simplestreams-s3 proxy --listen :8080
```

Enable OTLP/HTTP metrics for a local cleartext collector:

```sh
simplestreams-s3 proxy \
  --s3-bucket private-images \
  --metrics-endpoint localhost:4318 \
  --metrics-insecure
```

Cleartext OTLP is accepted only for an explicit loopback endpoint. Production collectors use verified TLS. Standard `OTEL_EXPORTER_OTLP_HEADERS` and `OTEL_EXPORTER_OTLP_METRICS_HEADERS` provide collector authentication headers.

## Inputs

`publish` accepts exactly:

1. an xz-compressed Incus metadata tarball containing one root `metadata.yaml`; and
2. a QCOW2 virtual-machine disk.

V1 supports `amd64`/`x86_64` and `arm64`/`aarch64`. Container images, unified images, LXD compatibility, format conversion, deletion, and garbage collection are intentionally unsupported.

## Configuration

Configuration precedence is fixed, highest first:

1. command flags;
2. `SIMPLESTREAMS_S3_*` environment variables;
3. the YAML file explicitly selected by `--config` or `SIMPLESTREAMS_S3_CONFIG`;
4. defaults.

There is no implicit config-file search. Unknown YAML keys and invalid values fail startup. See the complete [configuration reference](https://meigma.github.io/simplestreams-s3/configuration/).

## Development and verification

[mise](https://mise.jdx.dev) provisions the locked toolchain and [Moon](https://moonrepo.dev) is the task front door:

```sh
mise install
moon run root:check
moon run root:integration
```

CI additionally runs the race detector and a real Incus listing/import acceptance gate. The opt-in genuine-AWS conditional-write conformance test is:

```sh
SIMPLESTREAMS_S3_REAL_AWS_BUCKET=disposable-test-bucket \
SIMPLESTREAMS_S3_REAL_AWS_REGION=us-west-2 \
go test -count=1 -tags integration \
  -run TestRealAWSConditionalIndexWrite ./internal/integration
```

See [verification](https://meigma.github.io/simplestreams-s3/verification/) for the complete V1 gate map.

## Release artifacts

GoReleaser builds static `darwin` and `linux` binaries for `amd64` and `arm64`. Melange builds a signed Wolfi apk and apko assembles the multi-architecture, non-root container image without a Dockerfile. Release workflows generate checksums, SBOMs, signatures, and provenance attestations.

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution workflow and [SECURITY.md](SECURITY.md) for private vulnerability reporting.
