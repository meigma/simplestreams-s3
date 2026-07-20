---
title: Deploy the proxy
---

# Deploy the proxy

Run `simplestreams-s3 proxy` as a long-lived service that serves the mirror
to Incus clients.

## Prerequisites

- A published mirror ([Publish images](publish-images.md)).
- AWS credentials for a read-only identity, resolved through the
  [SDK default credential chain](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html).
- An ingress or network boundary that terminates HTTPS and enforces your
  client-access policy. The proxy listens on plain HTTP and does not
  authenticate clients; never expose it directly.

### Proxy IAM

Use a separate read-only identity, not the publisher's:

- `s3:GetObject` on objects under the prefix.
- `s3:ListBucket` on the bucket, restricted to the prefix, so missing objects
  surface as 404 instead of an access denial.
- Decrypt access to the bucket's KMS key when it uses SSE-KMS.

## Run

```sh
SIMPLESTREAMS_S3_BUCKET=private-images \
SIMPLESTREAMS_S3_REGION=us-west-2 \
simplestreams-s3 proxy --listen :8080
```

Every setting is also available as a flag or YAML key; see
[Configuration](configuration.md). The container image's entrypoint is
`/usr/bin/simplestreams-s3`, so pass the same arguments to the container. It
runs as non-root UID 65532 and needs no filesystem state.

The process logs JSON to stdout and starts listening even while S3 is
unreachable; readiness is reported separately.

## Wire health checks

| Endpoint | Meaning |
|---|---|
| `GET /healthz` | Liveness: the process and listener are up. Never touches S3. |
| `GET /readyz` | Readiness: the cached catalog probe succeeded recently. |

Point load-balancer checks at `/readyz` and liveness probes at `/healthz`.
`/readyz` returns `200` while the cached catalog probe is fresh and `503`
otherwise; the
[proxy interface reference](proxy-interface.md#health-and-readiness) lists
the body shape and failure reasons.

Readiness is probed in the background every `proxy.readiness_interval`
(default `10s`) and a success stays valid for `proxy.readiness_staleness`
(default `30s`); requests never wait on a live S3 call.

## Shutdown

On `SIGTERM` or `SIGINT` the proxy immediately reports not-ready
(`draining`), keeps serving for `proxy.shutdown_delay` (default `5s`) so load
balancers can react, then stops accepting connections and drains active
streams for up to `proxy.shutdown_grace` (default `30s`). A clean drain exits
0. Give your orchestrator a termination grace period longer than
`shutdown_delay` plus `shutdown_grace`.

## Connect Incus

Add the HTTPS endpoint your ingress exposes as a `simplestreams` remote:

```sh
incus remote add private-images https://images.example.com --protocol simplestreams
incus image list private-images:
incus image copy private-images:docsos/1.0/cloud local: --vm --alias docsos
```

See the [Incus image documentation](https://linuxcontainers.org/incus/docs/main/images/)
for remote and image management.

## Capacity and overload

At most `proxy.max_streams` (default `64`) object downloads stream
concurrently; excess requests fail fast with `503` and `Retry-After: 1`
rather than queueing. Stalled transfers are cancelled by the
`upstream_idle_timeout` and `write_idle_timeout` progress bounds (default
`30s` each). There is deliberately no whole-download timeout, because VM
disks can be arbitrarily large.

## Enable metrics (optional)

Metrics are off unless an OTLP/HTTP collector endpoint is configured:

```sh
simplestreams-s3 proxy \
  --s3-bucket private-images \
  --metrics-endpoint collector.internal:4318
```

The endpoint is `host:port` with no scheme. Export uses verified TLS;
`--metrics-insecure` permits cleartext only for a loopback collector.
Provide collector authentication through the standard
`OTEL_EXPORTER_OTLP_HEADERS` environment variable. Export failures never
affect serving or readiness. The instrument set is listed in the
[proxy interface reference](proxy-interface.md#metrics).
