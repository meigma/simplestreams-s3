# simplestreams-s3

`simplestreams-s3` publishes split Incus virtual-machine images to a private
Amazon S3 bucket and serves the resulting Simple Streams mirror through
authenticated S3 reads, so Incus clients can list and import images without any
public S3 access.

It provides three commands:

- `simplestreams-s3 publish METADATA_TARBALL DISK_QCOW2` validates and
  publishes one split VM image. Publication is idempotent and safe under
  concurrent publishers; conflicts fail closed without moving the active
  catalog. An optional `attest-vm-image` evidence manifest publishes its
  content-addressed proofs alongside the image.
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

### GitHub Action

The repository is also a TypeScript action that installs the CLI release paired
with its Git ref and publishes one split image. Authenticate to AWS first, then
invoke the repository directly:

```yaml
- uses: aws-actions/configure-aws-credentials@517a711dbcd0e402f90c77e7e2f81e849156e31d # v6.2.2
  with:
    role-to-assume: ${{ secrets.PUBLISH_ROLE_ARN }}
    aws-region: us-west-2

- uses: meigma/simplestreams-s3@v0
  with:
    metadata-path: build/incus.tar.xz
    disk-path: build/disk.qcow2
    s3-bucket: private-images
```

The optional manifest input accepts the version-1 handoff emitted by
[`meigma/attest-vm-image`](https://github.com/meigma/attest-vm-image). The
publisher requires a passing result, verifies that the handoff binds the exact
disk and metadata bytes, uploads every proof, and advertises one custom
`evidence-manifest` item without changing what Incus downloads. See the
[action guide](action/README.md) for a complete attestation and publication
workflow.

The moving `v0` tag follows public compatible `v0.x.y` releases. An exact tag
such as `v0.2.0` selects that repository and CLI release together. For the
strongest supply-chain guarantee, pin the full release commit SHA and retain
the exact version in a comment. The moving tag becomes available with the first
public action-capable repository release. See the
[action reference](action/README.md) for every input, output, and supported
runner.

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
