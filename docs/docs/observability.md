---
title: Observability reference
---

# Observability reference

## Health endpoints

`GET` and `HEAD /healthz` return `200`, `Content-Type: application/json`, and `Cache-Control: no-store` while the HTTP server is running. The GET body is `{"status":"ok"}`. Liveness never calls S3 or OTLP and remains available during drain.

`GET` and `HEAD /readyz` read cached catalog state:

- `200` with `{"status":"ready"}` when the last successful probe is within the staleness window;
- `503` with `{"status":"not_ready","reason":"<code>"}` otherwise.

Readiness reason codes are `starting`, `draining`, `catalog_missing`, `s3_unavailable`, and `s3_misconfigured`. The request handler does not wait on S3. OTLP availability does not affect readiness.

## JSON logging

Proxy stdout contains JSON records only. Every record includes `time`, `level`, `msg`, `service.name`, `service.version`, and `component`.

Object completion records include:

- `request_id`;
- `http.request.method`;
- low-cardinality `http.route` (`object`, `health`, `readiness`, or `unmatched`);
- `http.response.status_code`;
- `duration_ms`;
- `http.response.body.size`;
- `range_requested`;
- stable `error.kind` when unsuccessful.

Health and readiness completions are debug-level by default. Readiness transitions and `server_starting`, `server_listening`, `shutdown_started`, `shutdown_completed`, and `shutdown_forced` are explicit lifecycle records.

Logs never contain bucket names, full object keys, aliases, image fingerprints, authorization headers, credentials, OTLP headers, config-file contents, presigned material, metadata bodies, SDK wire data, or raw upstream error bodies.

## OTLP metrics

Metrics are disabled when `metrics.endpoint` is empty. Enabled export uses OTLP HTTP/protobuf, a periodic reader, and `metrics.timeout` for individual exports. Production uses verified TLS. Cleartext is restricted to an explicit loopback endpoint.

| Instrument | Kind | Attributes |
|---|---|---|
| `http.server.request.duration` | Histogram, seconds | `http.request.method`, `http.route`, `http.response.status_code`, `url.scheme`, `network.protocol.name`, `network.protocol.version` |
| `http.server.active_requests` | Observable up/down counter | None |
| `http.server.response.body.size` | Histogram, bytes | Same bounded HTTP attributes |
| `simplestreams_s3.s3.request.duration` | Histogram, seconds | `aws.operation`, `outcome`, optional `error.kind` |
| `simplestreams_s3.s3.requests` | Counter | Same bounded S3 attributes |
| `simplestreams_s3.s3.retries` | Counter | Same bounded S3 attributes |
| `simplestreams_s3.s3.transferred` | Counter, bytes | Same bounded S3 attributes |
| `simplestreams_s3.streams.rejected` | Counter | None |
| `simplestreams_s3.streams.incomplete` | Counter | None |
| `simplestreams_s3.readiness` | Gauge | `outcome`, optional stable `error.kind` |

Resource attributes are `service.name=simplestreams-s3` and `service.version=<binary version>`.

Metrics never use bucket, key, path, alias, image fingerprint, ETag, request ID, AWS request ID, or error text as attributes. Collector failures are fail-open and produce a sanitized warning at most once per minute. Shutdown performs a final flush within `metrics.timeout` without extending the HTTP drain grace period.
