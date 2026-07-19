---
title: Configuration reference
---

# Configuration reference

Configuration precedence is fixed, highest first:

1. command flags;
2. `SIMPLESTREAMS_S3_*` environment variables;
3. one YAML file selected by `--config` or `SIMPLESTREAMS_S3_CONFIG`;
4. defaults.

There is no implicit file search or hot reload. A selected file that is absent, unreadable, malformed, or contains an unknown key is fatal. Environment lists use comma-separated values; YAML uses native lists.

| YAML key | Flag | Environment variable | Default |
|---|---|---|---|
| `s3.bucket` | `--s3-bucket` | `SIMPLESTREAMS_S3_BUCKET` | Required |
| `s3.prefix` | `--s3-prefix` | `SIMPLESTREAMS_S3_PREFIX` | Empty |
| `s3.region` | `--s3-region` | `SIMPLESTREAMS_S3_REGION` | AWS chain |
| `s3.profile` | `--s3-profile` | `SIMPLESTREAMS_S3_PROFILE` | AWS chain |
| `s3.expected_bucket_owner` | `--s3-expected-bucket-owner` | `SIMPLESTREAMS_S3_EXPECTED_BUCKET_OWNER` | Empty |
| `s3.max_attempts` | `--s3-max-attempts` | `SIMPLESTREAMS_S3_MAX_ATTEMPTS` | `3` |
| `s3.max_backoff` | `--s3-max-backoff` | `SIMPLESTREAMS_S3_MAX_BACKOFF` | `1s` |
| `s3.dial_timeout` | `--s3-dial-timeout` | `SIMPLESTREAMS_S3_DIAL_TIMEOUT` | `3s` |
| `s3.tls_handshake_timeout` | `--s3-tls-handshake-timeout` | `SIMPLESTREAMS_S3_TLS_HANDSHAKE_TIMEOUT` | `5s` |
| `s3.response_header_timeout` | `--s3-response-header-timeout` | `SIMPLESTREAMS_S3_RESPONSE_HEADER_TIMEOUT` | `5s` |
| `publish.aliases` | Repeated `--alias` | `SIMPLESTREAMS_S3_ALIASES` | Empty |
| `publish.release_title` | `--release-title` | `SIMPLESTREAMS_S3_RELEASE_TITLE` | Image release |
| `publish.catalog_attempts` | `--catalog-attempts` | `SIMPLESTREAMS_S3_CATALOG_ATTEMPTS` | `4` |
| `publish.catalog_timeout` | `--catalog-timeout` | `SIMPLESTREAMS_S3_CATALOG_TIMEOUT` | `30s` |
| `publish.timeout` | `--publish-timeout` | `SIMPLESTREAMS_S3_PUBLISH_TIMEOUT` | `2h` |
| `proxy.listen` | `--listen` | `SIMPLESTREAMS_S3_LISTEN` | `:8080` |
| `proxy.max_streams` | `--max-streams` | `SIMPLESTREAMS_S3_MAX_STREAMS` | `64` |
| `proxy.read_header_timeout` | `--read-header-timeout` | `SIMPLESTREAMS_S3_READ_HEADER_TIMEOUT` | `5s` |
| `proxy.idle_timeout` | `--idle-timeout` | `SIMPLESTREAMS_S3_IDLE_TIMEOUT` | `60s` |
| `proxy.upstream_idle_timeout` | `--upstream-idle-timeout` | `SIMPLESTREAMS_S3_UPSTREAM_IDLE_TIMEOUT` | `30s` |
| `proxy.write_idle_timeout` | `--write-idle-timeout` | `SIMPLESTREAMS_S3_WRITE_IDLE_TIMEOUT` | `30s` |
| `proxy.max_header_bytes` | `--max-header-bytes` | `SIMPLESTREAMS_S3_MAX_HEADER_BYTES` | `32768` |
| `proxy.shutdown_delay` | `--shutdown-delay` | `SIMPLESTREAMS_S3_SHUTDOWN_DELAY` | `5s` |
| `proxy.shutdown_grace` | `--shutdown-grace` | `SIMPLESTREAMS_S3_SHUTDOWN_GRACE` | `30s` |
| `proxy.readiness_interval` | `--readiness-interval` | `SIMPLESTREAMS_S3_READINESS_INTERVAL` | `10s` |
| `proxy.readiness_timeout` | `--readiness-timeout` | `SIMPLESTREAMS_S3_READINESS_TIMEOUT` | `2s` |
| `proxy.readiness_staleness` | `--readiness-staleness` | `SIMPLESTREAMS_S3_READINESS_STALENESS` | `30s` |
| `logging.level` | `--log-level` | `SIMPLESTREAMS_S3_LOG_LEVEL` | `info` |
| `metrics.endpoint` | `--metrics-endpoint` | `SIMPLESTREAMS_S3_METRICS_ENDPOINT` | Empty; disabled |
| `metrics.interval` | `--metrics-interval` | `SIMPLESTREAMS_S3_METRICS_INTERVAL` | `30s` |
| `metrics.timeout` | `--metrics-timeout` | `SIMPLESTREAMS_S3_METRICS_TIMEOUT` | `10s` |
| `metrics.insecure` | `--metrics-insecure` | `SIMPLESTREAMS_S3_METRICS_INSECURE` | `false` |

## Validation rules

- `s3.bucket` must be a valid private general-purpose bucket name.
- `s3.prefix` is empty or a slash-separated relative key prefix. Leading or trailing slashes, empty components, dot segments, and backslashes are rejected rather than cleaned.
- Retry counts, stream/header limits, and all operational durations are positive.
- `proxy.listen` is a valid `host:port` listener.
- `proxy.readiness_staleness` is not shorter than `proxy.readiness_interval`.
- `logging.level` is `debug`, `info`, `warn`, or `error`.
- `metrics.endpoint` is a collector `host:port` without scheme or path.
- `metrics.insecure` permits cleartext only when the configured endpoint host is `localhost` or an explicit loopback IP.

The HTTP server has no whole-response write timeout. `proxy.write_idle_timeout` bounds stalled downstream progress without limiting healthy multi-gigabyte downloads.

AWS credentials use the SDK default chain. Static access keys are not accepted as flags or YAML fields. OTLP headers use the standard `OTEL_EXPORTER_OTLP_HEADERS` or `OTEL_EXPORTER_OTLP_METRICS_HEADERS` environment variable and are never logged.
