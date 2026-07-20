---
id: 008
title: Custom GitHub Action
date: 2026-07-20
status: complete
repos_touched: [simplestreams-s3]
related_sessions: [009]
---

## Goal

Create and validate a custom TypeScript GitHub Action that installs a released
`simplestreams-s3` CLI and publishes an Incus image to authenticated AWS S3.

## Outcome

The goal was met. The repository now exposes a root-level custom Action backed
by TypeScript source and tests under `action/`. It installs `latest` or an
explicit checksum-verified CLI release through the GitHub tool cache, maps the
supported publish controls to a shell-free CLI invocation, and leaves AWS
authentication to the caller. MinIO, the public `v0.1.0` release, and a
disposable genuine-AWS workflow run proved installation, publication, cache
reuse, repeatability, and cleanup.

The original independent `action-v*` release lane was replaced with unified
repository versioning. Consumers will use `meigma/simplestreams-s3@v0` after the
first action-capable unified release. Green release PR #31 remains open for
`v0.1.1` by explicit user direction; this session did not merge it, publish a
new release, or create the moving `v0` tag.

## Key Decisions

- Import GitHub's canonical `actions/typescript-action` scaffold at a pinned commit -> retain upstream provenance and established Action structure.
- Verify release checksums before installing with `@actions/tool-cache` -> support fast cache reuse without trusting an unauthenticated download.
- Invoke the CLI with a fixed argv array through `@actions/exec` -> avoid shell interpolation and keep publish behavior aligned with the public CLI.
- Keep AWS authentication outside the Action -> callers use AWS's standard credential/OIDC actions before publication.
- Move `action.yml` to the repository root and pair its default CLI version with the root release -> one tag now identifies compatible CLI and Action code.
- Move `vX` only after a stable exact `vX.Y.Z` GitHub release becomes public -> the consumer alias never points at an unpublished release commit.
- Test `latest` during a pending release PR, then reuse its resolved exact version -> a not-yet-published metadata default cannot be installed before the release PR merges.

## Changes

- `action.yml` - defines the root Action interface, paired CLI default, outputs, branding, and Node 24 bundle entrypoint.
- `action/` - contains the TypeScript installer/publish wrapper, 36 Jest tests, dependencies, committed distribution bundle, upstream provenance, and user documentation.
- `.github/workflows/action-ci.yml` - validates formatting, lint, tests, audit, bundle drift, public release installation, MinIO publication, tool-cache reuse, and optional real-AWS publication.
- `.github/workflows/major-version-tag.yml` and `.github/scripts/update_major_version_tag.sh` - update the moving major tag only from a stable public release.
- `.github/workflows/release.yml` and root Release Please configuration - couple Action and CLI versions while preventing a moving major tag from retriggering exact release publication.
- `.github/dependabot.yml`, repository quality checks, and integration helpers - cover Action dependencies and keep generated Node content out of Go-oriented scans.
- `README.md`, `action/README.md`, and `action/UPSTREAM.md` - document `@v0`, exact-tag/SHA pinning, AWS authentication, inputs, outputs, and canonical-template provenance.

## Open Threads

- [Release PR #31](https://github.com/meigma/simplestreams-s3/pull/31) is green and intentionally open for a later `v0.1.1` release decision.
- When PR #31 is approved, verify the full draft-to-public release pipeline and confirm that the release-published workflow creates `v0` at the exact `v0.1.1` commit.
- The optional real-AWS CI gate is disabled after its successful disposable proof; temporary credentials, repository settings, bucket, and whzbox environment were removed.

## References

- [PR #18 - verified CLI installer](https://github.com/meigma/simplestreams-s3/pull/18)
- [PR #20 - publish wrapper](https://github.com/meigma/simplestreams-s3/pull/20)
- [PR #21 - Action CI, release automation, and AWS proof](https://github.com/meigma/simplestreams-s3/pull/21)
- [PR #29 - generated-file formatting prerequisite](https://github.com/meigma/simplestreams-s3/pull/29)
- [PR #30 - unified repository release versioning](https://github.com/meigma/simplestreams-s3/pull/30)
- [PR #32 - pending-release CI fix](https://github.com/meigma/simplestreams-s3/pull/32)
- [Unified release PR #31](https://github.com/meigma/simplestreams-s3/pull/31)
- [Real AWS Action CI run 29764554868](https://github.com/meigma/simplestreams-s3/actions/runs/29764554868)
- [Unified release rehearsal 29770624798](https://github.com/meigma/simplestreams-s3/actions/runs/29770624798)

## Lessons

- A release PR that advances an Action's default CLI version cannot install that pending version before publication; release-PR integration must exercise the latest public version while separately validating metadata pairing.
- A moving major Action tag should be a post-publication operation, not part of version calculation or artifact publication.
- A two-run functional proof catches both publication behavior and tool-cache identity; unit tests alone do not prove the GitHub runner contract.
