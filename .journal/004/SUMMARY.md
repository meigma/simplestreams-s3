---
id: 004
title: Phase 3 publication updates
date: 2026-07-19
status: complete
repos_touched: [simplestreams-s3]
related_sessions: [001, 002, 003]
---

## Goal

Review session 001's approved design and plan, then complete Phase 3's safe repeat-publication and existing-catalog update path, including the required genuine-AWS conditional-write proof.

## Outcome

The goal was met. The publisher now safely adopts compatible existing catalogs, makes identical publication idempotent, repairs missing immutable objects, preserves concurrent compatible changes through bounded compare-and-swap retries, and fails closed on incompatible catalog or content state. This completes the V1 publish path defined by session 001.

[PR #9](https://github.com/meigma/simplestreams-s3/pull/9), `feat: add safe repeat publication`, passed CI, GitHub Pages, Kusari Inspector, race tests, MinIO integration, and a whzbox-backed real-AWS conformance window. It was squash-merged into `master` as `d6cc2a7697bdaecf16ef89f496606a3106f31927`. Local `master` was fast-forwarded and the implementation branch, remote branch, and Worktrunk worktree were removed.

## Key Decisions

- Load every compare-and-swap attempt through a fresh `simplestreams.Mirror` -> retries must never overwrite an index generation they did not observe.
- Keep `streams/v1/index.json` as the sole mutable publication pointer -> artifacts and product snapshots remain content-addressed, create-only, verifiable, and safe to leave behind after a lost race.
- Verify existing immutable objects by size, service SHA-256 metadata, and S3 full-object checksum -> object existence alone is insufficient evidence for idempotent reuse.
- Buffer only the small mutable index for its conditional `PutObject` -> the AWS SDK requires a rewindable body on this path, while large VM artifacts remain bounded streaming uploads through the transfer manager.
- Use MinIO for ordinary end-to-end repeat publication and whzbox for the AWS-specific conditional-write contract -> the two tests cover different compatibility boundaries without turning genuine AWS into the routine local gate.

## Changes

- `internal/catalog` — added strict adoption of the closed V1 VM catalog, metadata-preserving merge behavior, identical no-op detection, additive versions/products, monotonic timestamps, and typed conflicts.
- `internal/publish` — added fresh catalog reads, immutable verification and repair, conditional index commits, bounded compare-and-swap retry, cancellation protection, input-mutation detection, and lost-response convergence.
- `internal/adapter/s3store` and `internal/object` — added opaque catalog revisions, revision-aware reads, checksum-backed object attributes, absent-or-matches index writes, and typed AWS failure translation.
- `internal/config`, `internal/cli`, and `cmd/simplestreams-s3` — exposed `publish.catalog_attempts` through flags, environment, strict YAML, and its normative default.
- `internal/integration` — extended MinIO process coverage to repeat and second-version publication and added the opt-in real-AWS conditional-write conformance test.
- `README.md` and `docs/docs/index.md` — updated user-facing behavior and documented the local and genuine-AWS test entry points.

## Lessons

- A non-seekable index body fails through the AWS SDK retry/checksum stack; buffering the already bounded catalog index into a `bytes.Reader` fixes that requirement without compromising large-artifact streaming.
- MinIO can prove the ordinary `If-Match` repeat-publication path, but a disposable AWS bucket is still needed to establish genuine S3's exact conditional-write behavior.
- The checkout-built `whzbox` at `9851e3b` can provision, inject credentials for, and fully tear down a one-hour AWS sandbox suitable for bounded S3 conformance tests after an interactive session refresh.

## Open Threads

- Execute Phase 4's production proxy behavior: ranges and conditions, concurrency bounds, readiness, structured JSON logging, graceful draining, and network/S3 fault handling.
- Phase 5 still owns optional telemetry, complete operator documentation, final acceptance, and release readiness.
- Genuine-AWS multipart artifact behavior remains an optional conformance extension; the required Phase 3 AWS test covered conditional index creation and replacement.

## References

- [Merged PR #9 — safe repeat publication](https://github.com/meigma/simplestreams-s3/pull/9)
- [Session 001 design](../001/DESIGN.md)
- [Session 001 implementation plan](../001/PLAN.md)
- [Session 003 Phase 2 summary](../003/SUMMARY.md)
