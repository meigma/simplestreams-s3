---
title: simplestreams-s3 documentation
---

# simplestreams-s3

`simplestreams-s3` publishes split Incus virtual-machine images to a private
Amazon S3 bucket and serves the resulting Simple Streams catalog over HTTP, so
Incus clients can list and import those images without any public S3 access.

One static binary provides two operational commands, plus
`simplestreams-s3 version` for build information:

- `simplestreams-s3 publish` validates one split VM image (metadata tarball
  plus QCOW2 disk) and publishes it into the mirror. Publication is idempotent
  and safe under concurrent publishers.
- `simplestreams-s3 proxy` serves the mirror as a read-only plain-HTTP
  endpoint backed by authenticated S3 reads.

## Security boundary

The bucket must stay private. The proxy authenticates to S3 but does not
authenticate its own HTTP clients and does not terminate TLS: any client that
can reach the listener can read the entire mirror. Always deploy the listener
behind an ingress or network boundary that provides HTTPS and access control.
[Design](design.md) explains the model; [Deploy the proxy](deploy-the-proxy.md)
covers the requirements.

## Install

Releases publish four static binaries (`linux` and `darwin`, `amd64` and
`arm64`) with checksums, SBOMs, and provenance attestations on the
[releases page](https://github.com/meigma/simplestreams-s3/releases), and a
signed multi-architecture container image at
`ghcr.io/meigma/simplestreams-s3`. The image runs
`/usr/bin/simplestreams-s3` as non-root UID 65532 and contains no shell.

To build from source, run `go build ./cmd/simplestreams-s3` with the Go
toolchain pinned in `mise.toml`.

## Guides and reference

| Document | Purpose |
|---|---|
| [Publish images](publish-images.md) | Put images into the mirror and keep them updated. |
| [Deploy the proxy](deploy-the-proxy.md) | Run the proxy in production and connect Incus. |
| [Configuration](configuration.md) | Every setting: flags, environment variables, YAML file. |
| [Proxy interface](proxy-interface.md) | HTTP contract, log records, and metrics. |
| [Design](design.md) | How the mirror works and what it refuses to do. |
