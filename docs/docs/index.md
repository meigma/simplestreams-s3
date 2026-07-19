---
title: simplestreams-s3 Docs
---

# simplestreams-s3

`simplestreams-s3` publishes split Incus VM images into a private Amazon S3 mirror and serves the static Simple Streams layout through authenticated S3 reads.

The proxy listener is plain HTTP and unauthenticated. Deploy it behind trusted HTTPS termination and the downstream access-control policy appropriate for the environment.

The current Phase 2 slice supports publication into an empty mirror root and exact proxy `GET`/`HEAD` reads. Existing-catalog safety, production proxy behavior, telemetry, and complete operator reference material follow in the later approved implementation phases.
