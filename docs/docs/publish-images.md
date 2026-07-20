---
title: Publish images
---

# Publish images

Publish one split Incus VM image into the private S3 mirror, and republish
safely as new versions appear.

## Prerequisites

- A split Incus VM image: an xz-compressed metadata tarball containing
  `metadata.yaml`, plus a QCOW2 disk. See the
  [Incus image format](https://linuxcontainers.org/incus/docs/main/reference/image_format/).
  Only VM images for `amd64`/`x86_64` or `arm64`/`aarch64` are supported;
  container images and unified tarballs are rejected.
- A private S3 bucket with
  [Block Public Access](https://docs.aws.amazon.com/AmazonS3/latest/userguide/access-control-block-public-access.html)
  enabled. The application consumes an existing bucket; it never creates or
  reconfigures one. Dedicate the configured prefix to this mirror and make the
  publisher its only writer.
- AWS credentials from the
  [SDK default credential chain](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html)
  (role, profile, or environment). Static access keys are not application
  settings.

### Publisher IAM

Scope the publisher identity to the bucket and prefix:

- `s3:GetObject`, `s3:PutObject`, and `s3:AbortMultipartUpload` on objects
  under the prefix.
- `s3:ListBucket` on the bucket, restricted to the prefix. The application
  never lists objects, but without this permission S3 reports missing objects
  as access denials instead of 404s.
- The key permissions for the bucket's KMS key when it uses SSE-KMS. Uploads
  rely on the bucket's default encryption; the application sends no
  encryption headers.

Configure a
[lifecycle rule that aborts incomplete multipart uploads](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpu-abort-incomplete-mpu-lifecycle-config.html).
Interrupted uploads are aborted on failure, but a hard process kill can still
strand parts that the application never deletes.

## Publish an image

```sh
simplestreams-s3 publish \
  --s3-bucket private-images \
  --s3-region us-west-2 \
  incus.tar.xz disk.qcow2
```

On success the command prints the published product and version and exits 0:

```text
published docsos:1.0:cloud:amd64 version 202507191833
```

The product name is `<os>:<release>:<variant>:<arch>` from `metadata.yaml`,
and the version is the image's `creation_date` rendered as UTC
`YYYYMMDDHHMM`. The catalog records the alias `<os>/<release>/<variant>`;
Incus itself additionally lists an architecture-qualified form of it.

One invocation publishes exactly one image, activated by a single atomic
index write; [Design](design.md) explains why re-runs and concurrent
publishers stay safe.

## Add aliases

Pass `--alias` (repeatable) when the product is first published:

```sh
simplestreams-s3 publish \
  --s3-bucket private-images \
  --s3-region us-west-2 \
  --alias docsos/stable \
  incus.tar.xz disk.qcow2
```

Aliases are fixed at first publication. Publishing the same product again
with a different alias set is rejected as a catalog conflict; alias mutation
is deliberately unsupported in V1.

## Republish and add versions

- Re-running an identical publication is a safe no-op: it verifies the stored
  objects, repairs any that are missing, prints the same output, and exits 0.
  If a stored object's bytes do not match, the command fails instead of
  overwriting.
- A rebuilt image with a newer `creation_date` publishes as an additional
  version of the same product. Existing versions are preserved.
- Publishing the same product and version with different content is a
  conflict and fails without touching the active catalog.

Concurrent publishers are safe: the catalog commit is a conditional write,
retried from a fresh read up to `publish.catalog_attempts` times (default 4).
If a run fails with an ambiguous outcome — a timeout or lost response during
the final commit — re-run it; publication converges to the same state.

## Bounds and leftovers

`publish.timeout` (default `2h`) bounds the whole command, including large
disk uploads; `publish.catalog_timeout` (default `30s`) bounds each catalog
read and commit attempt. See [Configuration](configuration.md).

Deletion is out of scope: nothing removes images, versions, or unreferenced
objects. A failed attempt can leave unreferenced immutable objects under the
prefix; clients following the catalog never discover them.
