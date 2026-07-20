# simplestreams-s3

`simplestreams-s3` publishes split Incus virtual-machine images to a private
Amazon S3 bucket and serves the resulting Simple Streams mirror through
authenticated S3 reads, so Incus clients can list and import images without any
public S3 access.

It provides three commands:

- `simplestreams-s3 publish METADATA_TARBALL DISK_QCOW2` validates and
  publishes one split VM image. Publication is idempotent and safe under
  concurrent publishers; conflicts fail closed without moving the active
  catalog.
- `simplestreams-s3 proxy` serves exact `GET` and `HEAD` reads with ranges,
  conditions, readiness, JSON logging, graceful draining, and optional OTLP
  metrics.
- `simplestreams-s3 version` prints build information.

## Installation

Releases publish static binaries for `linux` and `darwin` (`amd64` and
`arm64`), with checksums, SBOMs, and provenance attestations, on the
[releases page](https://github.com/meigma/simplestreams-s3/releases), plus a
signed multi-architecture container image at `ghcr.io/meigma/simplestreams-s3`
that runs `/usr/bin/simplestreams-s3` as non-root UID 65532.

To build from source with the Go toolchain pinned in `mise.toml`:

```sh
go build ./cmd/simplestreams-s3
```

## Usage

Publish one split VM image (metadata tarball plus QCOW2 disk):

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

Both commands authenticate through the AWS SDK default credential chain;
static access keys are not application settings.

### Security boundary

The S3 bucket must remain private, and the configured prefix must be dedicated
to one mirror. The proxy does not authenticate downstream clients and does not
terminate public TLS: deploy it behind an ingress or network boundary that
provides HTTPS and the required access-control policy. See
[Deploy the proxy](https://meigma.github.io/simplestreams-s3/deploy-the-proxy/)
for the complete bucket, IAM, ingress, KMS, and multipart-cleanup
requirements.

## Configuration

Configuration precedence is fixed, highest first:

1. command flags;
2. `SIMPLESTREAMS_S3_*` environment variables;
3. the YAML file explicitly selected by `--config` or `SIMPLESTREAMS_S3_CONFIG`;
4. defaults.

There is no implicit config-file search. Unknown YAML keys and invalid values
fail startup. Every setting is listed in the
[configuration reference](https://meigma.github.io/simplestreams-s3/configuration/).

## Documentation

The [documentation site](https://meigma.github.io/simplestreams-s3/) covers
publishing, deployment, the complete configuration and proxy interface
references, and the design of the mirror.

## Development

[mise](https://mise.jdx.dev) provisions the locked toolchain and
[Moon](https://moonrepo.dev) is the task front door:

```sh
mise install
moon run root:check
moon run root:integration
```

CI additionally runs the race detector and a real Incus listing/import
acceptance gate. See [CONTRIBUTING.md](CONTRIBUTING.md) for the contribution
workflow and [SECURITY.md](SECURITY.md) for private vulnerability reporting.

## License

Licensed under either of

- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE))
- MIT license ([LICENSE-MIT](LICENSE-MIT))

at your option.

Unless you explicitly state otherwise, any contribution intentionally
submitted for inclusion in the work by you, as defined in the Apache-2.0
license, shall be dual licensed as above, without any additional terms or
conditions.
