---
id: 010
title: New work intake
started: 2026-07-21
---

## 2026-07-21 18:48 — Kickoff
Goal for the session: Start a fresh journal session; the substantive work request has not yet been provided.
Current state of the world: V1 delivery and the custom GitHub Action are complete, the repository default branch is synchronized at `8bacc28`, and this session is ready for a new bounded objective.
Plan: Bind this session to the current task, await the work request, then proceed in small reviewable increments and record meaningful checkpoints.

## 2026-07-21 19:01 — Simple Streams evidence discovery
Goal: Determine whether an image built and attested with `attest-vm-image` can publish its complete proof set through the `simplestreams-s3` Action.
Findings: The generic Simple Streams format permits arbitrary metadata and additional version items, but the Incus consumer recognizes and downloads only its metadata and root-disk artifact vocabulary. Unknown catalog fields and item types are ignored rather than imported with the image. The current `simplestreams-s3` Action accepts only the metadata tarball and QCOW2 disk, while `attest-vm-image` emits checksums, SBOM, vulnerability and validation documents, plus three optional Sigstore bundles.
Direction: Keep proofs outside the Incus metadata tarball. Prototype one content-addressed evidence manifest advertised as a companion item on the same product version; have it bind the disk SHA-256, metadata SHA-256, combined Incus fingerprint, and paths/digests/media types for every proof object. Upload all evidence before the catalog activation and prove that Incus list/import behavior remains unchanged while a generic mirror consumer can discover and verify the manifest.
Next: Confirm the proposed companion-manifest direction, then implement the smallest disposable end-to-end proof before expanding the permanent Action interface.

## 2026-07-21 19:40 — attest-vm-image PR 20 verification
Target: Reviewed `meigma/attest-vm-image` PR #20 at exact head `61049a6f1d7aa2bd3883aaaeede77e7910cf277f` against base `aede997b7142bde3659bb7e6cd36cbf79fb02fe4`.
Result: No blocking findings. The change writes a versioned `evidence-manifest.json`, exposes `evidence-manifest-path`, records disk plus optional metadata/build-manifest digests, hashes the explicitly known unsigned evidence and successful signing bundles with stable roles/media types, carries the validation result and optional attestation URL, preserves `checksums.txt`, and writes no manifest or outputs on signing/manifest aborts. Evidence-complete validation failures still produce a `result: fail` handoff without signed bundles.
Verification: The detached exact-head checkout passed the pinned-runtime `npm run ci` gate: formatting, lint, 212 tests and 2 snapshots, committed `dist/` parity, and a clean advisory audit. Hosted exact-head CI, GitHub Pages, Kusari Inspector, real-image `build-image`, and real GitHub `sign-image` checks all passed; the PR is mergeable and CLEAN.
Boundary: PR #20 satisfies the producer-side handoff requested for `attest-vm-image`; it intentionally does not implement or prove the future `simplestreams-s3` consumer, mirror upload, digest reconciliation, or rejection of `result: fail` manifests.

## 2026-07-21 19:58 — attest-vm-image v1.1.0 release
Verified the stable `v1.1.0` release at commit `2646b5c7b0afc58f20a821ab44a5d0780733bd79`; the moving `v1` tag resolves to the same commit. The released `action.yml` exposes the `evidence-manifest-path` output. PR #20 merged as `f6752e7c746309d31593d33e88d108ccb43a4200` before the release.
Boundary: The producer-side evidence manifest is now available to consumers. The next increment is to implement and prove the `simplestreams-s3` integration against `meigma/attest-vm-image@v1.1.0`.
