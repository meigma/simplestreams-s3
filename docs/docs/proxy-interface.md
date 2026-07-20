---
title: Proxy interface reference
---

# Proxy interface reference

The proxy's HTTP contract, log records, and metrics.

## Routes

| Path | Methods | Purpose |
|---|---|---|
| `/healthz` | `GET`, `HEAD` | Liveness; never touches S3 |
| `/readyz` | `GET`, `HEAD` | Cached readiness state |
| any other path | `GET`, `HEAD` | Object read from the mirror |

Any other method receives `405` with an `Allow: GET, HEAD` header. Every
response carries an `X-Request-ID` header: the client's value when it is
1–128 characters of `A–Z`, `a–z`, `0–9`, `.`, `_`, or `-`; otherwise a
generated identifier.

## Health and readiness

`GET /healthz` returns `200` with `{"status":"ok"}` while the process runs,
including during drain.

`GET /readyz` returns `200` with `{"status":"ready"}`, or `503` with
`{"status":"not_ready","reason":"<reason>"}`:

| Reason | Meaning |
|---|---|
| `starting` | no probe has succeeded yet |
| `s3_unavailable` | probes are failing on S3 or network errors |
| `catalog_missing` | the mirror has no `streams/v1/index.json` |
| `s3_misconfigured` | S3 rejects the proxy's access |
| `draining` | shutdown in progress |

## Object reads

The request path maps to an S3 key under the configured prefix. Traversal
sequences, backslashes, NUL bytes, and percent-encoded delimiters are
rejected with `400` before any S3 request.

Honored request headers:

- `Range` — a single `bytes=` range. Multi-range or malformed values are
  ignored and the full object is served. `Range` is ignored entirely when
  `If-Range` is present.
- `If-Match`, `If-None-Match` — entity-tag lists; malformed values are `400`.
- `If-Modified-Since`, `If-Unmodified-Since` — HTTP dates; malformed values
  are `400`.

Response headers are a fixed allowlist taken from the stored object:
`Content-Type`, `Content-Length`, `Content-Range`, `Accept-Ranges`, `ETag`,
`Last-Modified`, `Cache-Control`, `Content-Disposition`, `Content-Encoding`,
and `Expires`. No other S3 metadata is forwarded.

### Status codes

| Status | When | Body `code` |
|---|---|---|
| `200` | full object served | — |
| `206` | valid range served | — |
| `304` | conditional read not modified | no body |
| `400` | invalid path or malformed conditional header | `invalid_input` |
| `404` | object does not exist | `not_found` |
| `405` | method other than `GET`/`HEAD` | `method_not_allowed` |
| `412` | precondition failed | `precondition_failed` |
| `416` | range not satisfiable | `range_not_satisfiable` |
| `500` | unexpected local or non-retryable S3 failure | `internal_failure` |
| `502` | S3 rejected the proxy's credentials or signature | `unauthorized_upstream` |
| `503` | concurrent-stream limit reached; sent with `Retry-After: 1` | `stream_limit` |
| `503` | S3 unavailable after retries; sent with `Retry-After: 1` | `unavailable` |
| `504` | S3 timed out after retries | `deadline_exceeded` |

Error bodies are `{"code":"<code>","request_id":"<id>"}`, except for `HEAD`
responses and `304`, which carry none. If a download fails after headers were
sent, the connection closes abruptly instead of appending an error to the
payload.

## Logs

The proxy writes JSON records to stdout. Every record carries
`service.name`, `service.version`, and `component`.

One `request completed` record is written per request, with `request_id`,
`http.request.method`, `http.route` (`health`, `readiness`, `object`, or
`unmatched`), `http.response.status_code`, `http.response.body.size`,
`range_requested`, `duration_ms`, and `error.kind` when the request failed.
Health and readiness requests log at `debug`; everything else at `info`.
Object keys, URLs, query strings, and client addresses are never logged.

Other records: `server_starting`, `server_listening`, `shutdown_started`,
`shutdown_completed` (or `shutdown_forced`), a `readiness transition` record
with `ready` and `reason` on every state change, and a sanitized
metric-export warning at most once per minute.

## Metrics

Disabled unless `metrics.endpoint` is set; exported as OTLP over
HTTP/protobuf on the configured interval. Export failures are fail-open and
never affect serving or readiness. The shutdown flush is bounded by
`metrics.timeout` and cannot extend the HTTP drain window.

| Instrument | Type | Unit | Description |
|---|---|---|---|
| `http.server.request.duration` | histogram | s | inbound request duration |
| `http.server.response.body.size` | histogram | By | response body size |
| `http.server.active_requests` | up-down counter | {request} | in-flight object requests |
| `simplestreams_s3.s3.requests` | counter | {request} | S3 operations |
| `simplestreams_s3.s3.request.duration` | histogram | s | S3 operation duration |
| `simplestreams_s3.s3.retries` | counter | {retry} | S3 attempts after the first |
| `simplestreams_s3.s3.transferred` | counter | By | bytes moved by successful S3 operations |
| `simplestreams_s3.streams.rejected` | counter | {stream} | requests refused at the concurrency bound |
| `simplestreams_s3.streams.incomplete` | counter | {stream} | downloads terminated before completion |
| `simplestreams_s3.readiness` | gauge | 1 | cached readiness (0 or 1) |

The HTTP duration and body-size histograms carry `http.request.method`,
`http.route`, `http.response.status_code`, `url.scheme`, and
network-protocol attributes; the active-requests gauge carries none.
S3 instruments carry `aws.operation`, `outcome`, and `error.kind`. All
attribute values are fixed vocabularies: object keys, bucket names, and error
text never appear. Resource attributes are `service.name` and
`service.version`.

Collector authentication headers come from the standard
[OTLP exporter environment variables](https://opentelemetry.io/docs/languages/sdk-configuration/otlp-exporter/),
such as `OTEL_EXPORTER_OTLP_HEADERS`.
