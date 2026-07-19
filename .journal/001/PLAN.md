# `simplestreams-s3` V1 Implementation Plan

Status: ready for execution after review

## 1. Authority and scope

[`DESIGN.md`](./DESIGN.md) is the sole source of product, protocol, architecture, security, configuration, operational, and testing requirements. This plan only sequences that design into reviewable work.

The following rules apply to every phase:

- Do not edit `DESIGN.md` in an implementation PR.
- Do not add behavior, configuration, commands, compatibility promises, or operational contracts that are absent from `DESIGN.md`.
- Do not implement anything listed in design section 22, Deferred decisions.
- If implementation evidence conflicts with or leaves ambiguity in the design, stop the phase and request a decision. Do not resolve the gap in code or in this plan.
- When this plan summarizes a requirement, the exact language in `DESIGN.md` prevails.

## 2. Delivery rules

Phases execute in order. Phase 1 is the disposable compatibility proof required by design section 20; it uses one draft PR and does not merge spike code. Phases 2 through 5 each produce exactly one mergeable PR. Begin a phase only after the previous phase has met its success criteria and its PR has been closed or merged as specified.

Each mergeable PR MUST:

- cite the design sections it implements;
- stay within its phase boundary;
- include the tests needed to prove its success criteria;
- preserve the package dependency rules in design section 7;
- satisfy the declaration-comment rule in design section 9;
- pass `moon run root:check` before merge;
- use a Conventional Commit PR title and be squash-merged through GitHub.

Do not combine phases. Do not split a phase silently. If a phase cannot fit one reviewable PR, stop and revise this plan with the user before continuing.

## 3. Phase overview

| Phase | PR outcome | V1 capability added |
|---|---|---|
| 1. Incus compatibility proof | Draft PR closed without merge | Confirms the catalog assumptions that gate implementation |
| 2. Thin private-S3 vertical slice | Merged PR | Establishes the foundation, publishes one VM, and imports it through the proxy |
| 3. Safe repeat publication | Merged PR | Completes idempotent, concurrent, interruption-safe publishing |
| 4. Production proxy | Merged PR | Completes the HTTP, resilience, health, and JSON logging contracts |
| 5. Telemetry and V1 acceptance | Merged PR | Adds optional metrics, operational documentation, release hardening, and proves V1 |

## 4. Phase 1 — Incus compatibility proof

Design references: sections 6.1, 6.2, 7.2, 19.3, and 20 item 1.

### Work

Use `go-simplestreams` v0.1.0 to generate one minimal catalog for a known split Incus VM image. Serve it through a temporary HTTPS file server with a trusted certificate. Configure an Incus Simple Streams remote, list the expected alias, import the image, and compare the imported fingerprint with the metadata-first combined SHA-256.

Use one draft PR as the review and evidence container. Keep the experiment disposable. Do not add S3, production packages, or permanent compatibility helpers in this phase.

### Success criteria

Phase 1 is complete only when:

- `schema/incus.ValidateRuntimeProductFile` accepts the generated product document;
- the generated product name, alias, version, item names, file types, paths, checksums, and fingerprint match design section 6;
- Incus lists the expected alias through the HTTPS endpoint;
- Incus imports the image and reports the expected fingerprint;
- the PR records reproducible commands and evidence sufficient for review;
- no observed behavior conflicts with `DESIGN.md`.

After approval, close the draft PR without merging spike code. Any design conflict blocks Phase 2.

## 5. Phase 2 — Thin private-S3 vertical slice

Design references: sections 1 through 10, section 11's empty-catalog path, section 12.1, section 13, sections 18 and 19, and section 20 item 2.

### Work

Rebrand every template module, binary, task, release, package, and image reference to `simplestreams-s3` while preserving the repository's mise, Moon, lint, release, melange, and apko conventions.

Implement only the foundation needed by the vertical slice: the package boundaries from section 7, validated strong types and VM-only model, application error kinds, consumer-owned ports, the Cobra/Viper precedence and validation mechanics, the minimum section 10 settings used by this slice, and declaration-comment enforcement. Do not create speculative ports or empty package shells. Later phases add only the settings needed by their design-defined behavior.

Implement local split-VM inspection, catalog projection, and deterministic rendering. Add the minimum AWS adapter needed to publish into an empty private general-purpose bucket using the designed immutable objects, checksums, and initial conditional index commit. Implement exact validated path mapping plus streaming `GET` and `HEAD` proxy reads, then wire both commands through the composition root.

Keep existing-catalog concurrency and the remaining production HTTP behavior in later phases.

### Success criteria

Phase 2 is complete only when:

- no active product, module, task, release, package, or image configuration still names `template-go`;
- the `publish`, `proxy`, and `version` commands are runnable through thin, signal-aware CLI wiring;
- the slice's design-specified settings work through flags, environment variables, and the optional strict YAML file with the precedence, defaults, environment-only rules, and invalid-value rejection from section 10;
- package imports, strong types, typed errors, and declaration-comment enforcement satisfy design sections 7 through 9;
- publishing one valid split VM into an empty private bucket creates exactly the namespace and metadata in section 6.2 after all local validation succeeds;
- artifact upload remains bounded in memory and S3 verifies the designed checksum, size, and create-only conditions;
- `streams/v1/index.json` becomes visible only after every referenced object exists;
- every non-health proxy read uses authenticated S3 `GetObject` or `HeadObject` and performs no parsing, rewriting, redirecting, or caching;
- unsafe paths are rejected rather than cleaned or remapped;
- Incus lists and imports the image through the proxy behind trusted test TLS and verifies the fingerprint;
- adapter integration tests and `moon run root:check` pass.

## 6. Phase 3 — Safe repeat publication

Design references: section 6.2, sections 7.2 and 11, sections 13, 14, and 18, sections 19.1 and 19.2, and section 20 item 3.

### Work

Complete the publisher's existing-catalog path. Load each attempt through a fresh `simplestreams.Mirror`, retain the opaque index revision, validate the closed VM-only catalog, merge without losing admitted metadata or other index entries, enforce alias and identity conflicts, write immutable snapshots, and conditionally replace only `streams/v1/index.json`.

Add bounded compare-and-swap retries, immutable-object verification and repair, input-mutation detection, cancellation, multipart abort cleanup, lost-response convergence, and typed failure translation. Add deterministic fault adapters and the real-S3 conditional-write conformance test required by section 19.2.

### Success criteria

Phase 3 is complete only when:

- publishing the same image twice succeeds without duplicating the catalog or changing the index unnecessarily;
- a missing immutable artifact referenced by an identical publication is safely restored;
- conflicting bytes, metadata, aliases, product identities, versions, timestamps, or incompatible catalog fields fail without moving the index;
- concurrent compatible publishers preserve both changes after bounded compare-and-swap retry, while incompatible changes return a typed conflict; neither silently overwrites an unobserved revision;
- changing an input between hashing and upload produces an integrity failure before catalog commit;
- cancellation, S3 failure, and a lost commit response leave the index at a complete old or new generation, and rerunning converges;
- application retries occur only for catalog compare-and-swap conflicts; SDK retries remain bounded and operation-aware;
- deterministic fault tests, the opt-in real-S3 conditional-write test, and `moon run root:check` pass.

At this point, the V1 publish path is complete.

## 7. Phase 4 — Production proxy

Design references: section 12, sections 13 through 16, section 17's instrument definitions, section 18, section 19, and section 20 item 4.

### Work

Complete the HTTP contract: safe escaped-path handling, single-range and conditional requests, required response headers, fixed status mapping, sanitized JSON errors, request IDs, streaming interruption behavior, concurrency limits, upstream and downstream progress bounds, and client cancellation.

Add cached liveness/readiness behavior, bounded AWS failure handling, graceful signal shutdown, and production JSON logging to stdout with the exact field, lifecycle-event, route-cardinality, and redaction rules in sections 15 and 16. Add the proxy settings required by these behaviors. Route the section 17 metric emission points through the designed metrics port with a no-op implementation; Phase 5 supplies the OTLP adapter.

### Success criteria

Phase 4 is complete only when:

- table-driven and adapter tests prove every method, path, range, condition, header, and status rule in section 12;
- a mid-stream upstream failure terminates the response without appending an error body or retrying inside the active response;
- stalled upstream reads, stalled downstream writes, client disconnects, and concurrency saturation terminate with the designed bounded behavior;
- `/healthz` remains independent of S3 and OTLP, while `/readyz` follows the cached catalog probe and draining rules;
- an S3 outage keeps the process alive, produces bounded `503` or `504` responses, makes readiness unavailable, and recovers without restart;
- successful signal handling marks readiness false, drains within the configured grace period, and exits normally; a forced shutdown is non-zero;
- proxy stdout contains valid JSON records only while running, required lifecycle and completion records are present, and prohibited data is absent;
- every required HTTP, S3, stream, and readiness metric emission point is exercised through the no-op metrics port without changing request behavior;
- the Incus listing/import regression and `moon run root:check` pass.

At this point, the V1 proxy path is complete except for optional metrics.

## 8. Phase 5 — Telemetry and V1 acceptance

Design references: sections 5, 10, 13, 17, 19, 20 item 5, and 21.

### Work

Implement the optional OTLP/HTTP metrics adapter and its section 10 settings exactly as specified in section 17. Preserve fail-open serving, bounded exporter shutdown, verified TLS rules, and the closed metric attribute sets.

Finish release and container hardening, real-S3 conformance, and operator documentation. Document only the behavior already defined in `DESIGN.md`, including private-bucket ownership, IAM separation, Block Public Access, external HTTPS and access control, KMS considerations, multipart lifecycle cleanup, configuration precedence, and the proxy's unauthenticated downstream boundary.

Run the complete V1 acceptance path and audit every criterion in design section 21.

### Success criteria

Phase 5 is complete only when:

- every setting in design section 10 has its specified source exposure, default, validation, and precedence behavior;
- no OTLP endpoint means no exporter and no effect on proxy behavior;
- a configured collector receives every required standard and custom metric with only the allowed resource and metric attributes;
- collector failure is rate-limited, fail-open, and independent of readiness and request success;
- exporter flush and shutdown never exceed the configured metrics timeout;
- release binaries and the non-root container build successfully through the repository's existing release toolchain;
- operator documentation states the exact security, IAM, bucket, ingress, configuration, health, logging, and telemetry contracts from the design without adding new guarantees;
- mandatory CI acceptance lists and imports the image through trusted HTTPS and verifies its fingerprint; VM launch passes in a capable environment when that gate is available;
- fault, race, adapter, real-S3 conformance, functional acceptance, and `moon run root:check` gates pass in their designated environments;
- every acceptance criterion in design section 21 has recorded evidence and no deferred feature from section 22 has entered the implementation.

When Phase 5 merges, V1 is implemented.

## 9. Design coverage

| Design area | Implemented in |
|---|---|
| Scope, security boundary, and VM input contract | Phases 1 and 2; documented and rechecked in Phase 5 |
| Hexagonal boundaries, strong types, comments, and CLI/configuration foundation | Phase 2 |
| Production proxy and metrics configuration | Phases 4 and 5 |
| Initial private-S3 publish and exact proxy vertical slice | Phase 2 |
| Publication atomicity, idempotency, concurrency, and interruption handling | Phase 3 |
| HTTP contract, retries, health/readiness, shutdown, and JSON logs | Phase 4 |
| Optional OTLP metrics, release hardening, documentation, and full acceptance | Phase 5 |
| Deferred decisions | Excluded from every phase |
