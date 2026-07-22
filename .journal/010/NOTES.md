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
