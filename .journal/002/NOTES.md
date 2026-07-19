---
id: 002
title: Begin Phase 1 compatibility proof
started: 2026-07-18
---

## 2026-07-18 23:13 — Kickoff
Goal for the session: Review session 001's design and plan, then begin with the first implementation phase.
Current state of the world: Session 001 completed the approved V1 design and five-phase plan; no service implementation has started, and Phase 1 is the disposable Incus compatibility proof.
Plan: Re-read the design and plan, extract the Phase 1 contract and completion evidence, then take the smallest proof-driven implementation slice after confirming the intended starting point.

## 2026-07-18 23:16 — Session 001 review complete
Reviewed `001/DESIGN.md`, `001/PLAN.md`, the pinned `go-simplestreams` v0.1.0 surface, the repository baseline, and local proof prerequisites. Phase 1 is a disposable wire-compatibility experiment only: generate the exact two-item VM catalog, validate it with the Incus schema, serve it over trusted HTTPS, and prove alias listing, import, and the metadata-first combined fingerprint through a real Incus server. It must use a draft PR as the evidence container and close without merging; S3, template rebranding, and permanent application architecture begin in Phase 2.

No blocking design/plan conflict was found. The design's phrase "first implementation slice" at section 7.1 is best read as the first non-disposable slice because section 20 and the plan explicitly make Phase 1 disposable and place rebranding in Phase 2. The local Incus 7.2 client is installed, as are `mkcert` and Lima, but the configured local Incus server is unreachable and no existing Lima or Multipass VM is available. The smallest execution start is therefore a disposable Linux/Incus environment followed by the minimal catalog generator and evidence commands.

## 2026-07-18 23:38 — Phase 1 compatibility proof complete
Completed the disposable Phase 1 proof in draft PR #7 (`test(spike): prove Incus VM catalog compatibility`) at exact head `f3217287deedf39b87573afb622030aaf384e37b`. A nested Go spike used `go-simplestreams` v0.1.0 to generate the designed two-item catalog from the public Alpine 3.22 arm64 cloud split VM image, and `schema/incus.ValidateRuntimeProductFile` accepted the generated product document.

Lima 2.0.3 provided a disposable Ubuntu/Incus 6.0 environment, while `mkcert` provided a certificate trusted by both the host and guest. Incus listed alias `alpinelinux/3.22/cloud` as a virtual-machine image, imported it through `https://host.lima.internal:8443`, and reported fingerprint `3f16ca76d823d3ba62d2ca3d58de3e7909053bd569805aff45c9e2c3554fae25`, exactly matching the metadata-first combined SHA-256. Product name, compact UTC version, item names, file types, content-addressed paths, checksums, alias behavior, architecture mapping, and fingerprint all matched the design; no wire-contract conflict was found.

Kusari initially flagged transitive `golang.org/x/net` v0.52.0 through CUE. The spike retained `go-simplestreams` v0.1.0 while selecting fixed `x/net` v0.57.0, then repeated unit tests and the full Incus listing/import proof with identical results. Its `go-digest` license warning applies Creative Commons terms only to that module's README and CONTRIBUTING documents; the Go source is Apache-2.0. Hosted CI, GitHub Pages, and Kusari all passed on the final head.

PR #7 was closed without merge as required. Its remote branch, local spike worktree, HTTPS process, generated artifacts, and Lima guest were removed; `master` remains unchanged. Phase 2 is unblocked and should begin with the permanent template rebrand and thin private-S3 vertical slice, retaining the fixed `x/net` selection when the protocol library enters the production module.
