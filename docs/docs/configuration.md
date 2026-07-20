---
title: Configuration reference
---

# Configuration reference

Every public setting of `simplestreams-s3`.

## Sources and precedence

Each setting resolves from, highest first:

1. command-line flag;
2. environment variable;
3. the YAML file named by `--config` or `SIMPLESTREAMS_S3_CONFIG`;
4. built-in default.

No configuration file is read unless one is named explicitly, and the file is
parsed as YAML regardless of extension. Unknown keys and invalid values fail
startup.

Example file, usable by both commands:

```yaml
s3:
  bucket: private-images
  region: us-west-2
  prefix: mirrors/incus
proxy:
  listen: :8080
  max_streams: 128
logging:
  level: info
metrics:
  endpoint: collector.internal:4318
  interval: 30s
```

## S3 settings (both commands)

| YAML key | Flag | Environment variable | Default | Constraint |
|---|---|---|---|---|
| `s3.bucket` | `--s3-bucket` | `SIMPLESTREAMS_S3_BUCKET` | — | required; 3–63 characters |
| `s3.prefix` | `--s3-prefix` | `SIMPLESTREAMS_S3_PREFIX` | empty | relative path; no leading/trailing slash, empty or dot segments, or backslashes |
| `s3.region` | `--s3-region` | `SIMPLESTREAMS_S3_REGION` | SDK default | |
| `s3.profile` | `--s3-profile` | `SIMPLESTREAMS_S3_PROFILE` | SDK default | |
| `s3.expected_bucket_owner` | `--s3-expected-bucket-owner` | `SIMPLESTREAMS_S3_EXPECTED_BUCKET_OWNER` | empty | AWS account ID sent with every S3 request |
| `s3.max_attempts` | `--s3-max-attempts` | `SIMPLESTREAMS_S3_MAX_ATTEMPTS` | `3` | ≥ 1 |
| `s3.max_backoff` | `--s3-max-backoff` | `SIMPLESTREAMS_S3_MAX_BACKOFF` | `1s` | > 0 |
| `s3.dial_timeout` | `--s3-dial-timeout` | `SIMPLESTREAMS_S3_DIAL_TIMEOUT` | `3s` | > 0 |
| `s3.tls_handshake_timeout` | `--s3-tls-handshake-timeout` | `SIMPLESTREAMS_S3_TLS_HANDSHAKE_TIMEOUT` | `5s` | > 0 |
| `s3.response_header_timeout` | `--s3-response-header-timeout` | `SIMPLESTREAMS_S3_RESPONSE_HEADER_TIMEOUT` | `5s` | > 0 |

## Publish settings

| YAML key | Flag | Environment variable | Default | Constraint |
|---|---|---|---|---|
| `publish.aliases` | `--alias` (repeatable) | `SIMPLESTREAMS_S3_ALIASES` | none | no backslashes, commas, colons, or empty/dot path segments |
| `publish.release_title` | `--release-title` | `SIMPLESTREAMS_S3_RELEASE_TITLE` | the image's `release` value | |
| `publish.timeout` | `--publish-timeout` | `SIMPLESTREAMS_S3_PUBLISH_TIMEOUT` | `2h` | > 0 |
| `publish.catalog_timeout` | `--catalog-timeout` | `SIMPLESTREAMS_S3_CATALOG_TIMEOUT` | `30s` | > 0 |
| `publish.catalog_attempts` | `--catalog-attempts` | `SIMPLESTREAMS_S3_CATALOG_ATTEMPTS` | `4` | ≥ 1 |

## Proxy settings

| YAML key | Flag | Environment variable | Default | Constraint |
|---|---|---|---|---|
| `proxy.listen` | `--listen` | `SIMPLESTREAMS_S3_LISTEN` | `:8080` | `host:port` |
| `proxy.max_streams` | `--max-streams` | `SIMPLESTREAMS_S3_MAX_STREAMS` | `64` | ≥ 1 |
| `proxy.read_header_timeout` | `--read-header-timeout` | `SIMPLESTREAMS_S3_READ_HEADER_TIMEOUT` | `5s` | > 0 |
| `proxy.idle_timeout` | `--idle-timeout` | `SIMPLESTREAMS_S3_IDLE_TIMEOUT` | `1m` | > 0 |
| `proxy.upstream_idle_timeout` | `--upstream-idle-timeout` | `SIMPLESTREAMS_S3_UPSTREAM_IDLE_TIMEOUT` | `30s` | > 0 |
| `proxy.write_idle_timeout` | `--write-idle-timeout` | `SIMPLESTREAMS_S3_WRITE_IDLE_TIMEOUT` | `30s` | > 0 |
| `proxy.max_header_bytes` | `--max-header-bytes` | `SIMPLESTREAMS_S3_MAX_HEADER_BYTES` | `32768` | ≥ 1 |
| `proxy.shutdown_delay` | `--shutdown-delay` | `SIMPLESTREAMS_S3_SHUTDOWN_DELAY` | `5s` | > 0 |
| `proxy.shutdown_grace` | `--shutdown-grace` | `SIMPLESTREAMS_S3_SHUTDOWN_GRACE` | `30s` | > 0 |
| `proxy.readiness_interval` | `--readiness-interval` | `SIMPLESTREAMS_S3_READINESS_INTERVAL` | `10s` | > 0 |
| `proxy.readiness_timeout` | `--readiness-timeout` | `SIMPLESTREAMS_S3_READINESS_TIMEOUT` | `2s` | > 0 |
| `proxy.readiness_staleness` | `--readiness-staleness` | `SIMPLESTREAMS_S3_READINESS_STALENESS` | `30s` | ≥ `proxy.readiness_interval` |

## Logging and metrics settings (proxy only)

| YAML key | Flag | Environment variable | Default | Constraint |
|---|---|---|---|---|
| `logging.level` | `--log-level` | `SIMPLESTREAMS_S3_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `metrics.endpoint` | `--metrics-endpoint` | `SIMPLESTREAMS_S3_METRICS_ENDPOINT` | empty (disabled) | `host:port`, no scheme or path |
| `metrics.interval` | `--metrics-interval` | `SIMPLESTREAMS_S3_METRICS_INTERVAL` | `30s` | > 0 |
| `metrics.timeout` | `--metrics-timeout` | `SIMPLESTREAMS_S3_METRICS_TIMEOUT` | `10s` | > 0 |
| `metrics.insecure` | `--metrics-insecure` | `SIMPLESTREAMS_S3_METRICS_INSECURE` | `false` | `true` requires a loopback `metrics.endpoint` |

## Publish command

```text
simplestreams-s3 publish METADATA_TARBALL DISK_QCOW2
```

Exactly two positional arguments: the xz-compressed metadata tarball, then
the QCOW2 disk. The inputs must satisfy:

- The tarball is valid xz-compressed tar with exactly one root-level
  `metadata.yaml` (at most 1 MiB; at most 64 MiB total expanded archive) and
  no container or unified-image payloads.
- `architecture` and `properties.architecture` agree and resolve to
  `amd64`/`x86_64` or `arm64`/`aarch64`.
- `properties.os`, `properties.release`, `properties.variant`, and
  `properties.description` are non-empty; the first three contain no `:`,
  `/`, or `\` and no surrounding whitespace; `creation_date` is a positive
  Unix timestamp.
- The disk is QCOW2 (version 1–3) with a non-zero virtual size and a cluster
  size between 512 bytes and 2 MiB.

On success it prints `published <product> version <version>` and exits 0.
All errors exit 1 with the reason on stderr.

## Version command

`simplestreams-s3 version` (or `--version`) prints
`simplestreams-s3 <version> (<commit>) built <date>`. Release binaries carry
injected values; source builds print `dev (none) built unknown`.
