---
id: 006
title: Phase 5 delivery
date: 2026-07-19
status: complete
repos_touched: [simplestreams-s3]
related_sessions: [001, 002, 003, 004, 005]
---

## Goal
Review session 001's governing V1 design and plan against the merged Phase 1-4 implementation, then deliver the complete Phase 5 telemetry, operator-reference, release-rehearsal, and final-acceptance scope.

## Outcome
The goal was met. PR #12 delivered optional OTLP metrics, transition-only readiness logs, the complete V1 operator and reference documentation, mandatory race and real-Incus acceptance gates, and security-driven dependency/runtime hardening. It was squash-merged as `45c019ffef508b47adccb84437220db1b3d48047`; local and remote `master` are synchronized, the feature worktree and branches are removed, and merge-triggered CI passed on the squash commit.

## Key Decisions
- Keep metrics completely disabled when no endpoint is configured, and make enabled OTLP export fail-open and bounded -> collector outages must not affect object delivery, health, readiness, or HTTP drain time.
- Permit insecure OTLP only for explicit loopback endpoints -> production collectors retain verified TLS while local development remains practical.
- Make real Incus listing, import, and exact-fingerprint validation a required Ubuntu CI job -> unit and MinIO tests alone do not prove the consumer contract.
- Treat Phase 3's real-AWS conditional-write evidence as the required AWS-specific proof and leave genuine-AWS multipart as optional -> this follows design section 21 without expanding Phase 5 unnecessarily.
- Upgrade OpenTelemetry to v1.44.0, Go to checksum-locked v1.26.5, and encode readiness through structured JSON -> hosted security feedback and `govulncheck` identified newly published issues before merge.
- Stop at one review-ready Phase 5 PR and merge only after explicit approval -> preserved the session's review gate and kept deferred design section 22 features out of scope.

## Changes
- `internal/adapter/telemetry/` and `internal/proxy/metrics.go` - added the fixed OTLP HTTP/protobuf metric contract, low-cardinality attributes, exporter failure handling, and bounded shutdown.
- `internal/adapter/httpserver/`, `internal/adapter/s3store/`, and `cmd/simplestreams-s3/main.go` - wired HTTP, readiness, stream, and S3 observations plus transition-only readiness logs.
- `internal/config/config.go` and `internal/cli/proxy/` - added strict metrics defaults, flags, environment/YAML sources, validation, and precedence coverage.
- `internal/integration/incus_acceptance_test.go` and `.github/workflows/ci.yml` - added required race and architecture-aware real-Incus list/import/fingerprint acceptance jobs.
- `README.md`, `SECURITY.md`, and `docs/docs/` - replaced template material with V1 operator, configuration, observability, verification, and vulnerability-reporting guidance.
- `go.mod`, `go.sum`, `mise.toml`, and `mise.lock` - updated OpenTelemetry and pinned Go 1.26.5 with verified four-platform URLs and checksums.

## Open Threads
- Design section 22 remains intentionally deferred: metadata signing, deletion/garbage collection, containers, LXD compatibility, unified input, proxy caching, downstream authentication, hot reload, and OTLP traces/logs.
- Genuine-AWS multipart artifact behavior remains an optional extension; MinIO cannot prove the transfer-manager CRC-64/NVME completion path.
- Release Please PR #11 is open for version 0.1.2. This session did not merge it or create a tag or release.

## References
- [PR #12: Phase 5 telemetry and V1 acceptance](https://github.com/meigma/simplestreams-s3/pull/12)
- [Exact-head CI run 29705750099](https://github.com/meigma/simplestreams-s3/actions/runs/29705750099)
- [Exact-head Release Dry Run 29705753221](https://github.com/meigma/simplestreams-s3/actions/runs/29705753221)
- [Post-merge CI run 29708279167](https://github.com/meigma/simplestreams-s3/actions/runs/29708279167)
- [Release Please PR #11](https://github.com/meigma/simplestreams-s3/pull/11)
- [Session 001 design](../001/DESIGN.md) and [plan](../001/PLAN.md)

## Lessons
- A real Incus job must publish a fixture matching the runner architecture; an arm64-only fixture can pass local Apple Silicon/Lima rehearsal while being filtered out by an amd64 hosted client.
- Re-run current vulnerability analysis immediately before merge. The Phase 5 security gate caught newly published OpenTelemetry advisories and the Go 1.26.4 standard-library issue after the initial implementation was already green.
