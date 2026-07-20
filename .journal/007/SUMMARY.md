---
id: 007
title: Operator documentation, licensing, and ghd removal
date: 2026-07-19
status: complete
repos_touched: [simplestreams-s3]
related_sessions: [001, 002, 003, 005, 006]
---

## Goal

Replace the V1 documentation with a small set of mature, validated,
Diátaxis-structured documents for operators deploying simplestreams-s3, then
(added during the session) refresh the README to the readme-writer standard
and remove the ghd distribution tooling.

## Outcome

Met, through three approved squash merges. [PR #13](https://github.com/meigma/simplestreams-s3/pull/13)
(`767c966`) replaced the docs with six pages — home, two how-to guides
(publish images, deploy the proxy), two references (configuration, proxy
interface), and one explanation (design) — every example validated against
the built binary and a live MinIO mirror; the site is live at
https://meigma.github.io/simplestreams-s3/. [PR #14](https://github.com/meigma/simplestreams-s3/pull/14)
(`a70a95e`) restructured the README and added Apache-2.0 OR MIT dual
licensing (`LICENSE-APACHE`, `LICENSE-MIT`) to a previously unlicensed
repository. [PR #15](https://github.com/meigma/simplestreams-s3/pull/15)
(`b259a47`) removed `ghd.toml` and all ghd validation while retaining the
load-bearing GoReleaser staging and integrity checks in the renamed
`.github/scripts/stage_release_assets.py`. Merge-triggered CI passed on every
merge commit.

## Key Decisions

- Skip a tutorial quadrant -> operators are practitioners, and no public
  configuration exists for a validatable sandbox walkthrough; Diátaxis
  structure emerges from need rather than four-part scaffolding.
- Validate examples with a hand-crafted split-image fixture published through
  the real binary into throwaway MinIO (via the hidden test hooks) -> proves
  output formats, mirror layout, and live HTTP semantics without documenting
  unsupported configuration.
- Show no version-specific install commands -> no release, tag, or GHCR
  package exists yet; the docs describe what the release pipeline produces
  instead.
- Adversarially verify docs against code with pinned Sonnet 5/Opus 4.8
  agents -> caught three real factual errors (503 `stream_limit` body code,
  no drain 503 on object routes, 502 limited to credential rejection).
- Trust session 003's real-Incus evidence over code-only review -> the
  architecture-qualified alias is genuine Incus client behavior, so the claim
  was reworded, not removed.
- Dual license Apache-2.0 OR MIT per user choice -> the repo had no license
  anywhere; GitHub now detects Apache-2.0 as primary.
- Keep `stage_release_assets.py` rather than deleting the ghd script
  wholesale -> its staging of `dist/release-assets/`, 9-asset completeness,
  executable bits, and checksum verification feed the smoke-test and upload
  steps.

## Changes

- `docs/docs/` - replaced five template-era pages with six validated
  Diátaxis pages; updated `docs/mkdocs.yml` nav.
- `README.md` - restructured per readme-writer; links out to the docs site;
  added the License section.
- `LICENSE-APACHE`, `LICENSE-MIT` - new dual-license files (copyright 2026
  Meigma).
- `.github/scripts/stage_release_assets.py` (+ test) - renamed from
  `stage_ghd_release_assets.py`; ghd.toml validation removed, staging and
  integrity checks retained (5 tests pass).
- `.github/workflows/release.yml`, `release-dry-run.yml`, `moon.yml`,
  `ghd.toml` - ghd references and config removed; dry-run keeps
  artifacts.json and checksum assertions.

## Open Threads

- `melange.yaml` carries no license metadata for the apk; align it with
  Apache-2.0 OR MIT in a follow-up.
- No release has ever been published; Release Please PR #11 (0.1.2) remains
  open from earlier sessions.
- Genuine-AWS multipart artifact behavior remains an optional conformance
  extension (inherited thread).

## References

- [PR #13 - Diátaxis operator docs](https://github.com/meigma/simplestreams-s3/pull/13)
- [PR #14 - README and dual license](https://github.com/meigma/simplestreams-s3/pull/14)
- [PR #15 - ghd removal](https://github.com/meigma/simplestreams-s3/pull/15)
- [Branch-targeted Release Dry Run 29715038341](https://github.com/meigma/simplestreams-s3/actions/runs/29715038341)
- [Live docs site](https://meigma.github.io/simplestreams-s3/)
- [Session 006 summary](../006/SUMMARY.md)

## Lessons

- The proxy's concurrency-limit 503 uses body code `stream_limit`, distinct
  from the `unavailable` S3-outage 503; documentation review against code
  caught what live probing alone had not distinguished.
- GitHub default-setup CodeQL runs (`dynamic/github-code-scanning/codeql`)
  cannot be rerun via `gh run rerun` or check-suite rerequest; an empty
  commit is the practical retrigger, and it disappears in the squash merge.
- golangci-lint's shared cache can poison runs in sibling worktrees after a
  worktree is removed; `golangci-lint cache clean` resolves phantom findings
  that reference deleted paths.
- A version ID like `202507191833` derives from the image's `creation_date`,
  not the publication wall clock — relevant when documenting or debugging
  version identities.
