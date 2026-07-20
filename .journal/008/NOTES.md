---
id: 008
title: Custom GitHub Action
started: 2026-07-19
---

## 2026-07-19 21:07 — Kickoff
Goal for the session: Create a custom GitHub Action for simplestreams-s3.
Current state of the world: The V1 implementation is complete; the custom Action's intended interface and first working proof have not yet been defined.
Plan: Clarify the smallest useful Action contract, build a working prototype, validate it in-repository, and refine from what the proof teaches us.

## 2026-07-19 22:43 — Proposed action design and plan
Created `ACTION_DESIGN_PLAN.md` as the standalone review document. The proposal imports GitHub's canonical `actions/typescript-action` template at a pinned upstream commit, installs checksum-verified CLI releases through `@actions/tool-cache`, maps the complete useful publish control surface without owning AWS authentication, and releases the action independently under `action-vX.Y.Z` plus `action-vX` tags. The delivery sequence is intentionally prototype-first: prove the existing CLI release installer, then the publish wrapper against MinIO, then the separate CI/Release Please lane, with explicit review gates before the first action release.

Live verification found that tag `v0.1.0`, its successful release workflow, and all expected binary/checksum assets exist, but GitHub still reported the release as a draft at this checkpoint. The plan therefore rejects drafts in production and starts the public download smoke test after the release is promoted.
