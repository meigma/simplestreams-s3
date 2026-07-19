---
id: 003
title: Phase 2 implementation
date: 2026-07-19
status: complete
repos_touched: [simplestreams-s3]
related_sessions: [001, 002]
---

## Goal

Review session 001's approved design and plan, then deliver Phase 2's smallest permanent private-S3 vertical slice from the compatibility contract proven in session 002.

## Outcome

The goal was met. The repository is now a working `simplestreams-s3` publisher and authenticated read-through proxy for an initially empty private AWS S3 mirror. The real CLI path was rehearsed through MinIO, trusted HTTPS, and Incus 6.0.0; Incus listed and imported the generated Alpine VM with the expected fingerprint.

[PR #8](https://github.com/meigma/simplestreams-s3/pull/8), `feat: add Phase 2 private S3 slice`, passed CI, GitHub Pages, and Kusari Inspector and was squash-merged into `master` as `868a29c99b89c90cc79c2a392d02b2b8a36efac3`. Local `master` was fast-forwarded and the implementation branch and worktree were removed.

## Key Decisions

- Keep Phase 2 limited to an empty mirror -> safe repeat publication and existing-catalog updates remain Phase 3 work rather than expanding the first permanent slice.
- Keep production compatibility AWS-only -> endpoint, path-style, and upload-threshold overrides are deliberately test-only environment hooks and are absent from the public configuration surface.
- Accept MinIO for Phase 2 integration -> no disposable AWS bucket exists, so one Testcontainers-managed MinIO instance proves application composition and S3 adapter behavior without external credentials or persistent infrastructure.
- Force single PUT only in the MinIO test profile -> both tested MinIO releases rejected multipart CRC-64/NVME completion with `InvalidPart`; the production AWS checksum and multipart behavior remains unchanged and should receive a future conformance test.
- Keep integration behavior in categorized tests -> `internal/integration` is build-tagged and uses external package imports, shared scenario infrastructure owns container lifecycle, and the complete publish/proxy procedure remains readable in its process test.

## Changes

- `cmd/simplestreams-s3` and `internal/cli` — replaced the template command with composed `publish` and `proxy` commands plus flag, environment, file, and default configuration precedence.
- `internal/object`, `internal/image`, and `internal/catalog` — added typed boundaries, bounded split-VM inspection, checksums, deterministic Simple Streams rendering, and Incus schema validation.
- `internal/publish` and `internal/adapter/s3store` — added create-only S3 publication, immutable content objects, product snapshots, index-last activation, and empty-mirror refusal.
- `internal/proxy` and `internal/adapter/httpserver` — added authenticated exact-key `GET` and `HEAD` reads with safe HTTP error mapping.
- `internal/integration` — added a build-tagged, single-container MinIO process test covering publication, mirror contents, proxy reads, unsafe and missing paths, repeat-publication refusal, and create-only collisions.
- Repository metadata, documentation, release configuration, and packaging — completed the rebrand from the Go template to `simplestreams-s3`.

## Lessons

- The complete Phase 2 path preserved the Phase 1 wire contract: Incus imported the Alpine 3.22 arm64 VM with fingerprint `3f16ca76d823d3ba62d2ca3d58de3e7909053bd569805aff45c9e2c3554fae25`.
- MinIO is useful for durable local integration coverage but is not a faithful proof of AWS multipart checksum behavior.
- Integration helpers should expose infrastructure and low-level plumbing without absorbing the behavioral specification from categorized process tests.

## Open Threads

- Execute Phase 3's safe repeat-publication and existing-catalog update behavior from the session 001 plan.
- Add an optional real-AWS conformance test for multipart CRC-64/NVME when a disposable private bucket is available.
- Phases 4 and 5 still own production proxy operations, telemetry, and final V1 acceptance.

## References

- [Merged PR #8 — Phase 2 private S3 slice](https://github.com/meigma/simplestreams-s3/pull/8)
- [Session 001 design](../001/DESIGN.md)
- [Session 001 implementation plan](../001/PLAN.md)
- [Session 002 compatibility proof](../002/SUMMARY.md)
