---
id: 010
title: Attested image evidence publication
date: 2026-07-21
status: complete
repos_touched: [attest-vm-image, simplestreams-s3]
related_sessions: ["008", "009"]
---

## Goal
Make the `attest-vm-image` and `simplestreams-s3` Actions compose into one supported image pipeline: build and attest a VM image, then publish the image and its complete proof set through Simple Streams without breaking Incus consumers.

## Outcome
The goal was met. `attest-vm-image` v1.1.0 now emits the versioned evidence handoff, `simplestreams-s3` validates and publishes it as a content-addressed companion item, real Incus acceptance remained unchanged, and the integration shipped publicly in `simplestreams-s3` v0.1.1 with `v0` pointing at the same release commit.

## Key Decisions
- Use a custom `evidence-manifest` version item rather than inventing standard attestation fields: generic Simple Streams accepts additional metadata/items, while Incus ignores unknown item types instead of importing proof material.
- Keep proof objects outside the Incus metadata tarball: the companion manifest binds the disk, metadata, optional build manifest, validation documents, SBOM, vulnerability report, and signing bundles without changing the image fingerprint contract.
- Validate the producer handoff before publication: require a passing result, exact image and metadata digests, a closed re-hashed proof set, and all three bundles plus the attestation URL for signed evidence.
- Upload immutable evidence before catalog activation and allow one compatible enrichment of an existing version: this preserves index-last publication and rejects conflicting evidence for the same image version.
- Prove compatibility with a real Incus list/import/fingerprint gate and generic mirror discovery rather than relying on schema tolerance alone.

## Changes
- `meigma/attest-vm-image` - verified PR #20 and public v1.1.0 as the producer contract for `evidence-manifest.json` and the `evidence-manifest-path` Action output.
- `internal/evidence/` - added version-1 handoff parsing, validation, digest reconciliation, path rewriting, and proof-object preparation.
- `internal/catalog/` and `internal/publish/` - added companion-item merge semantics, content-addressed uploads, enrichment preservation, and conflict rejection.
- `internal/cli/publish/`, `action.yml`, and `action/` - exposed the evidence-manifest input through the CLI and packaged TypeScript Action.
- `.github/workflows/action-ci.yml` and integration tests - proved packaged-Action forwarding, generic proof discovery, and unchanged real-Incus import behavior.
- `README.md` and `docs/docs/` - documented evidence publication and the custom companion-item boundary.
- Release Please PR #31 published the combined CLI/Action release as v0.1.1 and advanced the stable `v0` compatibility tag.

## Open Threads
- Incus deliberately ignores and does not verify the companion evidence; consumers that need proofs must discover the custom item and process its manifest.
- `actions/create-github-app-token` warns that its `app-id` input is deprecated; migrate the release and major-tag workflows to `client-id` in a maintenance change.

## References
- [attest-vm-image PR #20](https://github.com/meigma/attest-vm-image/pull/20)
- [attest-vm-image v1.1.0](https://github.com/meigma/attest-vm-image/releases/tag/v1.1.0)
- [simplestreams-s3 PR #33](https://github.com/meigma/simplestreams-s3/pull/33)
- [simplestreams-s3 release PR #31](https://github.com/meigma/simplestreams-s3/pull/31)
- [simplestreams-s3 v0.1.1](https://github.com/meigma/simplestreams-s3/releases/tag/v0.1.1)
- [Production release run 29890562730](https://github.com/meigma/simplestreams-s3/actions/runs/29890562730)
- [Major compatibility tag run 29890792515](https://github.com/meigma/simplestreams-s3/actions/runs/29890792515)

## Lessons
- Simple Streams extensibility and consumer support are separate questions: arbitrary catalog data can be safely carried, but only a real consumer proof establishes that an unknown companion item does not disturb image listing, import, or fingerprinting.
