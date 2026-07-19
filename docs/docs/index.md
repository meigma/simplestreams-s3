---
title: simplestreams-s3 documentation
---

# simplestreams-s3

`simplestreams-s3` publishes split Incus VM images into a private Amazon S3 mirror and serves the static Simple Streams layout through authenticated S3 reads.

The proxy listener is plain HTTP and unauthenticated. Deploy it behind trusted HTTPS termination and the downstream access-control policy appropriate for the environment.

Use these documents while operating V1:

- [Operator guide](operator-guide.md) — prepare a private bucket, separate IAM identities, publish an image, and deploy the proxy.
- [Configuration reference](configuration.md) — look up every YAML key, flag, environment variable, default, and validation rule.
- [Observability reference](observability.md) — configure health probes, JSON logging, and optional OTLP metrics.
- [Verification reference](verification.md) — run the local, CI, real-AWS, Incus, release, and container gates.

V1 supports split virtual-machine images for `amd64` and `arm64`. It does not support containers, unified images, LXD compatibility, image deletion, metadata signing, proxy caching, downstream authentication, or public TLS termination.
