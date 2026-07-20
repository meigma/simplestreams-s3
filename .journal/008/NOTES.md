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

## 2026-07-20 00:02 — Slice 1 review checkpoint
Completed the installer-only proof on branch `feat/action-installer-slice-1` at commit `87a0a79169c3ee1959f30dfd883562dcae896fe9`; draft PR #18 is open for review. The `action/` scaffold was mechanically imported from GitHub's canonical `actions/typescript-action` repository at pinned commit `57b9acc0d972b482f0db345fa09703f3612fda95`, with provenance and the upstream license retained.

The TypeScript action now accepts `latest` or an explicit CLI version, resolves GitHub release assets, rejects draft releases, verifies the selected binary against `checksums.txt`, installs it through `@actions/tool-cache`, adds its directory to `PATH`, and exposes the resolved version and executable path as outputs. Unit tests cover release resolution, platform/architecture mapping, checksum validation, cache reuse, unsupported targets, and action input/output behavior.

The previously drafted `v0.1.0` release is now public. A clean Linux Node 24 smoke test downloaded the real arm64 release, verified and executed `simplestreams-s3 --version`, then proved an explicit `v0.1.0` install reused the tool cache. The committed `dist/index.js` independently passed the same public-release installation path and wrote the expected GitHub Action outputs. `npm run all`, clean host and Linux `npm ci`, `npm audit --audit-level=low`, and syntax/diff checks passed; every applicable hosted check on PR #18 is green.

Stopped at the agreed Slice 1 review gate. The publish wrapper, MinIO proof, and action release automation remain unstarted for later slices.

## 2026-07-20 10:00 — Slice 2 review checkpoint
PR #18 was re-pinned to reviewed head `87a0a79169c3ee1959f30dfd883562dcae896fe9`, all required checks remained green, and it was squash-merged as `67b94480ad78de52d5ed6c28c46ede43a27520a6`. Local and remote `master` were fast-forwarded to that commit and the integrated Slice 1 worktree was removed.

Completed the publish-wrapper proof on branch `feat/action-publish-wrapper-slice-2` at commit `1ebb4216ec26590e3f2761b397eab21e6eef8dae`; draft PR #20 is open for review. The action now reads the complete approved publish input surface, translates it into a fixed argv array, invokes the verified CLI through `@actions/exec` without a shell, requires one exact `published <product> version <image-version>` result line, and exposes the product and image version alongside the installed CLI outputs. AWS credentials, roles, and profiles remain outside the action boundary; the README demonstrates AWS OIDC authentication first and uses full-SHA action pins.

The packaged `dist/index.js` was invoked twice against one disposable MinIO scenario using the real public `v0.1.0` CLI. The first run installed and checksum-verified the release, published the fixture, and produced the expected four-object mirror; the second run reported a tool-cache hit, returned identical outputs, and left the exact object set unchanged. Focused tests cover required and optional inputs, multiline aliases, complete deterministic argv construction, non-zero exit propagation, exact result parsing, successful outputs, and failure handling.

Validation passed: 36 fast Jest tests, `npm run all`, zero `npm audit --audit-level=low` findings, clean Linux Node 24 `npm ci` plus build/tests/audit, `moon run root:check`, the two-run MinIO action integration, and every applicable hosted check on PR #20 including JavaScript/TypeScript CodeQL. Go quality tooling now explicitly ignores generated `action/node_modules` so the new project does not pollute repository-wide formatting, lint, or declaration scans.

Stopped at the agreed Slice 2 review gate. Action-specific CI, bundle drift enforcement, Dependabot, Release Please state/workflow, changelog/versioning, and `action-vX` tags remain unstarted for Slice 3.
