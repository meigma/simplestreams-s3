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

## 2026-07-20 10:25 — Slice 3 review checkpoint
PR #20 was re-pinned to reviewed head `1ebb4216ec26590e3f2761b397eab21e6eef8dae`, all required checks remained green, and it was squash-merged as `971dedcbe2060fbaf99154991b51b36ea7bc9e78`. Local `master` was fast-forwarded and the integrated Slice 2 worktree was removed.

Completed the independent CI and release-lane slice on branch `feat/action-release-automation-slice-3` at head `461571907a316734f0279533bb8317d8be15572e`; draft PR #21 is open for review. Action CI now runs the canonical Node 24 format, lint, Jest, audit, and committed-`dist/` drift gates. Its hosted integration job starts digest-pinned MinIO, invokes `uses: ./action` twice with the public `v0.1.0` CLI, proves identical outputs and tool-cache reuse, and verifies the unchanged four-object catalog through existing Go helpers. The first hosted attempt exposed a MinIO startup race; retrying all transient health-check errors fixed that exact issue, and the job passed in 1m03s.

The action now has its own Node Release Please config, empty bootstrap manifest, changelog, and workflow. A dry run proposes the intended `1.0.0` release from Slices 1 and 2 and updates only action-owned release state. The release workflow uses the existing Meigma App and full-SHA pins, then moves `action-v<major>` only after the exact `action-vX.Y.Z` release exists. npm Dependabot coverage was added for `/action`; releases execute the reviewed committed bundle and publish no npm package or generated asset.

Live rehearsal found that root Release Please had already opened CLI PR #19 solely from action commits. The root config now uses the Slice 2 merge as its transitional `last-release-sha` and excludes action, GitHub workflow, integration-test, and quality-test directories. Its dry run opens zero CLI release PRs; the stale PR #19 was closed with that evidence. No `action-v*` tag or action GitHub release exists.

Validation passed: `npm run ci` with 36 fast Jest tests and zero audit findings, `moon run root:check`, `actionlint` for both new workflows, the local two-run MinIO action integration, Release Please 17.10.3 action/root dry runs, and every applicable hosted PR #21 check including the two action CI jobs, Go/Actions/JavaScript CodeQL, race detection, Incus acceptance, and repository CI. Stopped at the agreed Slice 3 review gate before merging PR #21 or creating `action-v1.0.0`.

## 2026-07-20 10:44 — Real AWS action proof
Extended the Slice 3 proof at the user's request using a fresh one-hour whzbox AWS playground. Commit `8ce91724ef87d6e1de8e71abe03c00d3ae3bc971` adds a variable-gated `Real AWS S3 publish` job to Action CI plus a fixture-only Go helper. The job uses the full-SHA-pinned standard AWS credentials action, creates a run-unique bucket, invokes `uses: ./action` twice with the public `v0.1.0` CLI, verifies identical action outputs, the shared tool-cache path, four S3 objects, and `streams/v1/index.json`, then deletes the objects and bucket in an always-run cleanup step. It is reusable by temporarily installing the two documented repository secrets, setting `SIMPLESTREAMS_S3_REAL_AWS_E2E=1`, and using `workflow_dispatch`; same-repository PR execution is supported for review proofs while the variable is enabled.

Action CI run `29764554868` passed on the exact commit; the real-AWS job `88427376763` completed in 1m10s. Logs show the first install and publication, the second cache hit and identical publication result, successful catalog assertions, and deletion of all four objects. A follow-up AWS query found zero matching buckets. The temporary GitHub secrets and enable variable were deleted, `whzbox destroy --yes` completed, and `whzbox list --json` returned `[]`. All other PR #21 checks are green, the action Release Please dry run still proposes `1.0.0`, the CLI dry run still proposes zero releases, and no action tag or release was created.
