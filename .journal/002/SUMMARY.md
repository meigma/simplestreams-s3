---
id: 002
title: Incus compatibility proof
date: 2026-07-19
status: complete
repos_touched: [simplestreams-s3]
related_sessions: [001]
---

## Goal

Review session 001's approved V1 design and plan, then execute Phase 1: a disposable compatibility spike proving that the proposed two-item Simple Streams VM catalog works with a real Incus client and server.

## Outcome

The goal was met. The spike generated the designed catalog with `go-simplestreams` v0.1.0, validated it with `schema/incus.ValidateRuntimeProductFile`, served it through locally trusted HTTPS, and proved real Incus alias listing and VM import. No observed behavior conflicted with the design, so Phase 2 is unblocked.

The complete experiment and evidence were recorded in draft [PR #7](https://github.com/meigma/simplestreams-s3/pull/7), `test(spike): prove Incus VM catalog compatibility`, at exact head `f3217287deedf39b87573afb622030aaf384e37b`. CI, GitHub Pages, and Kusari passed on that head. Per the implementation plan, PR #7 was closed without merge and its branch, worktree, generated artifacts, HTTPS process, and Lima guest were removed. `master` received no spike code and remains aligned with `origin/master` at `8f5e65a`.

## Key Decisions

- Keep Phase 1 entirely disposable -> the purpose was to validate wire assumptions before permanent architecture or S3 work, so the spike lived only in a draft PR that was closed unmerged.
- Use Lima and `mkcert` for local functional proofs -> Lima supplied a disposable Ubuntu/Incus environment and `mkcert` supplied HTTPS trusted by the host and guest without introducing permanent project infrastructure.
- Treat design section 7.1's "first implementation slice" as Phase 2 -> sections 20 and the plan explicitly make Phase 1 disposable and assign template rebranding to the first non-disposable slice.
- Preserve the proposed wire contract -> Incus accepted the product name, default alias, compact UTC version, item names, file types, content-addressed paths, checksums, architecture mapping, and combined fingerprint as designed.
- Select fixed `golang.org/x/net` v0.57.0 while retaining `go-simplestreams` v0.1.0 -> Kusari flagged transitive v0.52.0 for CVE-2026-39821; the full proof was repeated successfully after the fixed selection.

## Changes

- [PR #7](https://github.com/meigma/simplestreams-s3/pull/7) — held the disposable Go catalog generator, HTTPS server, tests, exact reproduction commands, and observed Incus evidence; closed without merge.
- `.journal/TECH_NOTES.md` — records the availability and preferred use of Lima and `mkcert`, the successful Phase 1 wire proof, and the dependency selection needed for Phase 2.
- `.journal/002/NOTES.md` — records the design review, proof execution, exact fingerprint, hosted verification, cleanup, and handoff.

## Lessons

- The public Alpine 3.22 arm64 cloud split VM image produced fingerprint `3f16ca76d823d3ba62d2ca3d58de3e7909053bd569805aff45c9e2c3554fae25`; Incus reported that exact value after importing through the generated mirror.
- Incus listed the designed alias `alpinelinux/3.22/cloud` and also exposed the architecture-qualified alias `alpinelinux/3.22/cloud/arm64`.
- After installing the `mkcert` CA into a running Lima guest, restart the Incus service before import so its long-running Go process reloads the system certificate pool.
- Kusari's `github.com/opencontainers/go-digest` warning concerns Creative Commons licensing for that module's README and CONTRIBUTING documents; its Go source is Apache-2.0.

## Open Threads

- Begin Phase 2 from the fetched default branch: permanently rebrand the template and implement the thin private-S3 vertical slice defined in session 001's plan.
- When adding `go-simplestreams` v0.1.0 to the production module, retain a fixed `golang.org/x/net` selection at v0.57.0 or newer and confirm the dependency scan remains green.
- Reuse Lima and `mkcert` for the Phase 2 Incus functional path; no persistent local Incus environment is required.

## References

- [Draft PR #7 — Phase 1 evidence](https://github.com/meigma/simplestreams-s3/pull/7)
- [Session 001 V1 design](../001/DESIGN.md)
- [Session 001 implementation plan](../001/PLAN.md)
- [Session 001 summary](../001/SUMMARY.md)
- [`go-simplestreams` v0.1.0](https://github.com/meigma/go-simplestreams/releases/tag/v0.1.0)

