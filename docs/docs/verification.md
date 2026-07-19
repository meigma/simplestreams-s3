---
title: Verification reference
---

# Verification reference

V1 verification is split by the environment needed to prove each contract.

## Local aggregate gate

```sh
moon run root:check
```

This runs formatting, lint, build, unit tests, declaration-comment enforcement, and the strict documentation build.

Run the MinIO adapter and process integration suite separately:

```sh
moon run root:integration
```

It proves initial, repeated, and second-version publication; immutable/create-only behavior; exact proxy reads; and sanitized failures through the real AWS adapter against a disposable S3-compatible service.

## CI gates

The required CI workflow runs:

- the aggregate Moon gate and MinIO integration suite;
- `go test -race ./...` for concurrent state and cancellation paths;
- a real Incus acceptance job on Ubuntu 24.04.

The Incus job creates a disposable private MinIO bucket, publishes a split VM through the production services, serves it from the proxy over locally trusted HTTPS, configures an Incus Simple Streams remote, lists the expected alias, imports the VM image, and compares the imported fingerprint with the metadata-first combined SHA-256.

VM launch is optional because hosted runners do not guarantee nested virtualization.

## Genuine AWS conformance

AWS-specific conditional index creation and replacement require a disposable real bucket:

```sh
SIMPLESTREAMS_S3_REAL_AWS_BUCKET=disposable-test-bucket \
SIMPLESTREAMS_S3_REAL_AWS_REGION=us-west-2 \
go test -count=1 -tags integration \
  -run TestRealAWSConditionalIndexWrite ./internal/integration
```

The test creates one uniquely prefixed object and registers exact cleanup. It proves create-only, matching-revision replacement, and stale-revision rejection. Genuine-AWS multipart artifact testing is an optional extension; it is not a V1 acceptance requirement.

## Release and container rehearsal

The `Release Dry Run` workflow uses the existing release toolchain without publishing:

- GoReleaser builds `darwin` and `linux` binaries for `amd64` and `arm64`, checks artifact names and checksums, and smoke-tests the host binary;
- melange builds per-architecture signed Wolfi apks on native runners;
- apko assembles the non-root image and smoke-tests both version command forms.

The tag-triggered release workflow additionally uploads draft release assets, publishes the multi-architecture image, generates SBOMs, signs the image, and records isolated provenance attestations.

## Acceptance mapping

| V1 criterion | Evidence source |
|---|---|
| Repeat publication, conflicts, concurrent compare-and-swap, input mutation, interruption convergence | Publisher unit/fault tests and MinIO integration |
| Incus listing, import, and fingerprint | Required `Incus Acceptance` CI job |
| Authenticated exact S3 reads, HTTP ranges/conditions, bounded outages, drain behavior, JSON logs | HTTP/S3 adapter tests, fault tests, MinIO integration |
| Optional fail-open OTLP, closed attributes, bounded shutdown | Protobuf collector and failure tests in `internal/adapter/telemetry` |
| Configuration defaults, validation, environment-only values, and precedence | CLI/configuration tests |
| Unsupported container, unified-image, and LXD-oriented inputs | Image and catalog unit tests |
| Declaration comments and package boundaries | AST declaration test and lint |
| Release binaries and non-root container | `Release Dry Run` workflow |
| Genuine S3 conditional writes | Opt-in real-AWS conformance test and Phase 3 evidence |

Deferred V1 features remain excluded from every gate: signing Simple Streams metadata, deletion and garbage collection, containers, LXD compatibility, unified input, proxy caching, alias mutation, multiple catalogs, downstream authentication, hot reload, OTLP traces/logs, and cross-process multipart resumption.
