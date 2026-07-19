---
title: simplestreams-s3 Docs
---

# simplestreams-s3

`simplestreams-s3` publishes split Incus VM images into a private Amazon S3 mirror and serves the static Simple Streams layout through authenticated S3 reads.

The proxy listener is plain HTTP and unauthenticated. Deploy it behind trusted HTTPS termination and the downstream access-control policy appropriate for the environment.

The current slice safely adopts compatible catalogs, makes repeated publication a no-op, preserves concurrent compatible updates through bounded conditional writes, and serves exact proxy `GET`/`HEAD` reads. Conflicting catalog or immutable-object state fails closed. Production proxy behavior, telemetry, and complete operator reference material follow in the later approved implementation phases.
