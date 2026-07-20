---
title: Design
---

# Design

Why `simplestreams-s3` looks the way it does, and what it deliberately
refuses to do.

## The problem

Incus imports images from Simple Streams mirrors: static JSON catalogs and
image files fetched over HTTPS. S3 can store those files, but a private
bucket cannot serve unauthenticated Simple Streams clients directly.
`simplestreams-s3` splits the problem in two: `publish` maintains a valid
catalog inside the bucket, and `proxy` translates unauthenticated HTTP reads
into authenticated S3 reads. Nothing is generated or rewritten at serve time;
the proxy is deliberately an exact-object pipe.

## Mirror layout

The configured bucket and prefix hold exactly four kinds of keys:

```text
streams/v1/index.json
streams/v1/images-<document-sha256>.json
images/<metadata-sha256>.incus.tar.xz
images/<disk-sha256>.qcow2
```

Artifacts and catalog snapshots are content-addressed and immutable: each key
embeds the SHA-256 of its bytes, so an object is either absent, correct, or
provably wrong. `streams/v1/index.json` is the single mutable object. Because
activating a new catalog is one atomic write to one fixed key, readers
observe either the previous complete catalog or the new complete catalog,
never a partial state.

Checksums are computed from artifact bytes; S3 ETags are never used. Incus
verifies the published fingerprint, which is the SHA-256 of the metadata
tarball followed immediately by the disk.

## Publication

Publication is optimistic and idempotent. Each attempt reads the current
index, merges the new image into a validated catalog, uploads the immutable
objects create-only, and finally replaces the index with a conditional write
— `If-Match` on the previously observed revision, or `If-None-Match: *` for a
fresh mirror. If another publisher won the race, the attempt retries from a
fresh read; an unobserved catalog is never overwritten.

The consequences operators rely on:

- Re-running a publication verifies and repairs rather than duplicating.
- Concurrent publishers cannot corrupt the catalog or lose each other's
  images.
- Every failure lands before the index write, so a failed publication leaves
  at most unreferenced immutable objects that catalog followers never
  discover.
- Conflicts — the same version with different bytes, an alias owned by
  another product, an incompatible existing catalog — fail closed instead of
  being merged heuristically.

Published images are permanent: V1 has no deletion or garbage collection,
and a product's aliases cannot change after first publication.

## Security model

The bucket stays private and the proxy holds the only read path, so image
distribution reduces to controlling access to the proxy's listener. That
control is deliberately out of scope: TLS termination and client
authentication belong to the ingress in front of the proxy, which is why the
listener speaks plain HTTP and trusts its network boundary.

Exposure is bounded on both sides. The proxy's IAM identity is read-only and
prefix-scoped; it forwards only a fixed allowlist of response headers; and it
emits no credentials, bucket names, presigned URLs, or object keys in
responses, logs, or metrics. The publisher is the prefix's only writer, which
is what makes stored checksums trustworthy evidence for idempotent
republication. Setting `s3.expected_bucket_owner` applies AWS
[bucket-owner condition checks](https://docs.aws.amazon.com/AmazonS3/latest/userguide/bucket-owner-condition.html)
to every request.

When S3 is unavailable the proxy fails and reports itself unready rather
than serving stale data; it caches nothing.

## Non-goals

V1 deliberately does not: serve container images or unified tarballs, claim
LXD compatibility, sign metadata, delete or garbage-collect images, cache
objects, terminate TLS, authenticate downstream clients, serve multiple
buckets or catalogs per process, or promise compatibility with non-AWS S3
implementations.

Metadata signing, deletion and garbage collection, proxy caching, and
downstream authentication are deferred rather than rejected; they are
candidates for later versions.
