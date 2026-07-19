---
title: Operator guide
---

# Operate a private mirror

This guide prepares one private S3 mirror, publishes a split Incus VM, and runs the proxy behind an existing ingress.

## Prepare the bucket

Use an existing AWS S3 general-purpose bucket. `simplestreams-s3` does not create the bucket or change its policy, encryption, versioning, or lifecycle configuration.

1. Enable S3 Block Public Access for the bucket and account.
2. Remove public bucket-policy statements and public ACL grants.
3. Reserve either the whole bucket or one prefix for this mirror. Do not place unrelated or sensitive objects below that prefix.
4. Allow the publisher identity to be the only routine non-administrative writer to the mirror prefix.
5. Configure a lifecycle rule that aborts incomplete multipart uploads after an operator-selected interval. A hard process termination can leave unfinished parts behind.
6. When the bucket uses a customer-managed KMS key, grant the publisher and proxy identities the key permissions required by their S3 operations. The proxy requires decryption access.

Bucket versioning and default encryption are operator choices. Uploads use the bucket's configured default encryption.

## Create separate IAM identities

Use separate publisher and proxy roles. Replace `BUCKET` and `PREFIX` in these examples. When no prefix is configured, use `arn:aws:s3:::BUCKET/*` for objects and an appropriate bucket-wide `s3:prefix` condition.

The read-only proxy needs object reads and a prefix-constrained bucket check:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "s3:GetObject",
      "Resource": "arn:aws:s3:::BUCKET/PREFIX/*"
    },
    {
      "Effect": "Allow",
      "Action": "s3:ListBucket",
      "Resource": "arn:aws:s3:::BUCKET",
      "Condition": {
        "StringLike": {
          "s3:prefix": ["PREFIX", "PREFIX/*"]
        }
      }
    }
  ]
}
```

The application does not call `ListObjects`. `s3:ListBucket` lets S3 distinguish a missing object from an object the caller cannot access.

The publisher needs the same reads plus writes and multipart cleanup within the owned prefix:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:AbortMultipartUpload"
      ],
      "Resource": "arn:aws:s3:::BUCKET/PREFIX/*"
    },
    {
      "Effect": "Allow",
      "Action": "s3:ListBucket",
      "Resource": "arn:aws:s3:::BUCKET",
      "Condition": {
        "StringLike": {
          "s3:prefix": ["PREFIX", "PREFIX/*"]
        }
      }
    }
  ]
}
```

Set `s3.expected_bucket_owner` when the AWS account ID is known. This adds S3's expected-owner check to every operation.

## Supply configuration

Credentials come only from the AWS SDK default credential chain. Use an instance, task, workload, or assumed role in production. Do not put static access keys in the YAML file or command arguments.

Create a strict YAML file such as:

```yaml
s3:
  bucket: private-images
  prefix: incus
  region: us-west-2
  expected_bucket_owner: "123456789012"
  max_attempts: 3
  max_backoff: 1s
  dial_timeout: 3s
  tls_handshake_timeout: 5s
  response_header_timeout: 5s

publish:
  aliases: []
  release_title: ""
  catalog_attempts: 4
  catalog_timeout: 30s
  timeout: 2h

proxy:
  listen: :8080
  max_streams: 64
  read_header_timeout: 5s
  idle_timeout: 60s
  upstream_idle_timeout: 30s
  write_idle_timeout: 30s
  max_header_bytes: 32768
  shutdown_delay: 5s
  shutdown_grace: 30s
  readiness_interval: 10s
  readiness_timeout: 2s
  readiness_staleness: 30s

logging:
  level: info

metrics:
  endpoint: otel-collector.internal:4318
  interval: 30s
  timeout: 10s
  insecure: false
```

Unknown keys and invalid values fail before startup. See the [configuration reference](configuration.md) for every source and default.

## Publish a VM image

The metadata tarball must contain one root `metadata.yaml`; the disk must be QCOW2. Publish exactly one split image per invocation:

```sh
simplestreams-s3 publish \
  --config /etc/simplestreams-s3/config.yaml \
  incus.tar.xz disk.qcow2
```

The command validates and hashes both local files before S3 mutation. It uploads content-addressed artifacts and an immutable product snapshot, then conditionally changes only `streams/v1/index.json`. Repeating identical input is a no-op after immutable objects are verified. A conflict or interrupted attempt does not expose a partial catalog; rerun the same command to converge after an unknown response.

Do not modify an input while publication is running.

## Deploy the proxy

Run the proxy with the read-only role:

```sh
simplestreams-s3 proxy --config /etc/simplestreams-s3/config.yaml
```

The listener is plain HTTP and has no downstream authentication. Any client that can reach it can read every valid mirror object. Place it behind an ingress or network boundary that:

- terminates trusted HTTPS;
- enforces the required client authentication and authorization policy;
- forwards `GET`, `HEAD`, ranges, and conditional request headers without rewriting mirror paths;
- removes the instance from traffic after `/readyz` becomes unavailable.

Do not expose the application listener directly to an untrusted network. The proxy never returns S3 URLs, bucket names, credentials, arbitrary metadata, or upstream error bodies.

## Configure probes and shutdown

Use `GET` or `HEAD /healthz` for process liveness. It never calls S3 or the OTLP collector.

Use `GET` or `HEAD /readyz` for traffic readiness. It reads cached state from bounded background checks of `streams/v1/index.json`; it never waits on S3 in the request handler. OTLP failure does not affect readiness.

On `SIGINT` or `SIGTERM`, the proxy becomes unready, waits `proxy.shutdown_delay`, then drains active streams for at most `proxy.shutdown_grace`. The independent OTLP shutdown flush is bounded by `metrics.timeout` and cannot extend the HTTP grace period.

## Enable metrics

Leave `metrics.endpoint` empty to avoid constructing an exporter. For production, set a collector `host:port` and keep `metrics.insecure: false`; transport uses verified TLS.

Cleartext is allowed only for `localhost` or an explicit loopback IP:

```sh
simplestreams-s3 proxy \
  --config /etc/simplestreams-s3/config.yaml \
  --metrics-endpoint localhost:4318 \
  --metrics-insecure
```

Supply OTLP authentication only through `OTEL_EXPORTER_OTLP_HEADERS` or `OTEL_EXPORTER_OTLP_METRICS_HEADERS`. Collector outages produce rate-limited warnings and remain independent of requests, health, and readiness. See the [observability reference](observability.md) for instruments and allowed attributes.
