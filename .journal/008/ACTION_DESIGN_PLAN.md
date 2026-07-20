# Custom GitHub Action: Design and Delivery Plan

- Status: proposed for review
- Session: 008
- Date: 2026-07-19

## Goal

Add a custom TypeScript action under `action/` that installs a released
`simplestreams-s3` CLI and uses `simplestreams-s3 publish` to publish one split
Incus VM image to an existing private S3 mirror.

The action will not authenticate to AWS. Its caller must first use AWS's
standard authentication action (normally OIDC through
`aws-actions/configure-aws-credentials`) so the CLI can consume the AWS SDK
default credential and region chain.

## Delivery stance

Build this in short, reviewable slices:

1. import the canonical TypeScript-action scaffold and prove installation of an
   existing CLI release;
2. wrap the publish command and prove one real action invocation against MinIO;
3. add the action's independent CI and release lane;
4. refine documentation and controls from the working proof.

Do not design a general CLI runner or a new publishing API. The TypeScript layer
should remain a narrow adapter over the CLI's existing contract.

## Current evidence

- GitHub documents `actions/typescript-action` as its TypeScript action template.
  Its current `main` snapshot is commit
  `57b9acc0d972b482f0db345fa09703f3612fda95` and supplies Node 24, ESM,
  Rollup, Jest, ESLint, Prettier, committed `dist/`, local-action support, and
  CI patterns.
- The CLI release contract already supplies raw executable assets named
  `simplestreams-s3_<version>_<os>_<arch>` for Linux and macOS on amd64 and
  arm64, plus `checksums.txt`.
- `publish` accepts exactly two positional paths and already owns validation,
  S3 access, retries, timeouts, catalog compare-and-swap behavior, and result
  formatting. The action should translate inputs into these arguments, not
  reproduce this logic.
- The `v0.1.0` tag exists at `39df93839661498d48eaa12a81ce7dfca22d5d53`.
  Release workflow run `29719020137` succeeded and uploaded all four binaries,
  SBOMs, and checksums. At the time this document was written, GitHub still
  reported the release as a draft (`publishedAt: null`). The public installer
  smoke test can use `v0.1.0` as soon as that draft is published; production
  action code should not install draft releases.

## Source baseline and import policy

Implementation must start by pulling the official
[`actions/typescript-action`](https://github.com/actions/typescript-action)
repository, not by recreating a TypeScript scaffold from memory.

The first implementation slice will:

1. clone or download the upstream template at the pinned commit above into a
   temporary directory;
2. copy the action-project files into `action/`;
3. keep the template's Node 24/TypeScript, test, lint, format, bundle, and
   committed-`dist` structure;
4. adapt root-only workflows into this repository's `.github/workflows/` and
   pin every referenced third-party action to a full commit SHA;
5. replace the template's example implementation, metadata, fixtures, and
   release helper rather than carrying dead sample behavior;
6. record the upstream repository, commit, imported files, and deliberate
   deviations in `action/UPSTREAM.md`.

The template is a baseline, not a permanent fork. Later upstream changes are
reviewed and imported intentionally rather than merged wholesale.

## Proposed repository shape

```text
action/
  .node-version
  .release-please-manifest.json
  action.yml
  CHANGELOG.md
  README.md
  UPSTREAM.md
  package.json
  package-lock.json
  release-please-config.json
  rollup.config.ts
  tsconfig.json
  eslint.config.mjs
  jest.config.js
  src/
    index.ts
    main.ts
    inputs.ts
    install.ts
    publish.ts
  __fixtures__/
  __tests__/
  dist/
    index.js
    index.js.map

.github/workflows/
  action-ci.yml
  action-release-please.yml
```

Keep action-specific source, package metadata, release state, changelog, and
documentation inside `action/`. GitHub requires workflow files at the repository
root, so the two action workflows are the deliberate exception.

## Runtime design

The entry point performs one linear operation:

```text
read and validate inputs
  -> resolve latest or explicit CLI version
  -> find the exact OS/architecture binary in the runner tool cache
  -> on miss: download binary + checksums, verify SHA-256, cache the file
  -> build a direct argv array for `simplestreams-s3 publish`
  -> execute without a shell
  -> expose the resolved CLI version and publish result as outputs
```

### Installation

- Input `version` defaults to `latest` and accepts `latest`, `X.Y.Z`, or
  `vX.Y.Z`, including valid SemVer prerelease suffixes.
- `latest` resolves through GitHub's latest published release API. An explicit
  version resolves the matching `vX.Y.Z` release. Draft releases are rejected.
- An optional `github-token` may authenticate GitHub API/download requests and
  avoid anonymous rate limits. Public releases remain installable without it.
- Supported runner mappings are:
  - `linux` + `x64` -> `linux_amd64`;
  - `linux` + `arm64` -> `linux_arm64`;
  - `darwin` + `x64` -> `darwin_amd64`;
  - `darwin` + `arm64` -> `darwin_arm64`.
- Windows and other architectures fail early with an actionable unsupported
  platform message because the CLI does not publish those binaries.
- Use `@actions/tool-cache` directly:
  - resolve the concrete version before lookup;
  - `find("simplestreams-s3", version, runnerArch)`;
  - on a miss, download the exact release asset and `checksums.txt`;
  - verify the binary against the exact checksum entry before caching;
  - mark it executable, then `cacheFile(...)` and `core.addPath(...)`.
- Never cache an unverified download. Never silently fall back to source builds
  or containers.

The runner tool cache persists across invocations in the same job and may
persist across jobs on long-lived self-hosted runners. GitHub-hosted runners are
ephemeral, so this is intentionally not advertised as a cross-workflow cache and
does not add `actions/cache`.

### Publish execution

Use `@actions/exec` with a binary path and an argument array. Do not construct a
shell command, interpolate inputs into a command string, or accept arbitrary
extra arguments.

The action captures the CLI's stable success line:

```text
published <product> version <image-version>
```

It forwards useful CLI output to the job log, fails the step on a non-zero exit,
and exposes parsed values only after an exact successful match. The CLI remains
the authoritative validator for paths, durations, bucket names, prefixes,
aliases, and image contents.

### Input contract

Only `version` receives an action-level default. Empty optional inputs are not
passed, leaving the CLI's defaults authoritative and avoiding duplicated default
values that can drift.

| Input | Required | CLI mapping / behavior |
|---|---:|---|
| `version` | no | CLI release to install; default `latest` |
| `github-token` | no | GitHub release API/download authentication only |
| `metadata-path` | yes | first positional argument |
| `disk-path` | yes | second positional argument |
| `s3-bucket` | yes | `--s3-bucket` |
| `config-file` | no | `--config` |
| `s3-prefix` | no | `--s3-prefix` |
| `s3-region` | no | `--s3-region`; otherwise use AWS SDK discovery |
| `s3-expected-bucket-owner` | no | `--s3-expected-bucket-owner` |
| `aliases` | no | multiline values, each emitted as one `--alias` |
| `release-title` | no | `--release-title` |
| `publish-timeout` | no | `--publish-timeout` |
| `catalog-timeout` | no | `--catalog-timeout` |
| `catalog-attempts` | no | `--catalog-attempts` |
| `s3-max-attempts` | no | `--s3-max-attempts` |
| `s3-max-backoff` | no | `--s3-max-backoff` |
| `s3-dial-timeout` | no | `--s3-dial-timeout` |
| `s3-tls-handshake-timeout` | no | `--s3-tls-handshake-timeout` |
| `s3-response-header-timeout` | no | `--s3-response-header-timeout` |

Deliberately omit AWS access keys, session tokens, role ARNs, and an S3 profile
input. The caller's AWS authentication step establishes the default credential
chain. Environment variables and a config file remain available for advanced
CLI configuration, but the action does not become an authentication adapter.

### Outputs

| Output | Meaning |
|---|---|
| `cli-version` | concrete installed CLI version |
| `cli-path` | tool-cache path to the verified executable |
| `product` | published Simple Streams product name |
| `image-version` | published image version derived from `creation_date` |

## AWS and GitHub permissions boundary

The usage documentation will show AWS OIDC authentication before this action.
The caller grants `id-token: write` to the AWS authentication step and whatever
S3/KMS permissions the existing CLI documentation requires. This action itself:

- requests no AWS credential inputs;
- performs no STS calls;
- does not alter AWS credentials;
- requires no GitHub write permission;
- uses only public GitHub release reads unless the optional token is provided.

The example publisher policy remains the CLI's current least-privilege contract:
`s3:GetObject`, `s3:PutObject`, `s3:AbortMultipartUpload`, prefix-restricted
`s3:ListBucket`, and applicable KMS permissions.

## Independent action versioning and releases

The action must not share the CLI's `vX.Y.Z` sequence.

### Tag and consumer contract

- exact action releases: `action-vX.Y.Z`;
- moving compatibility tag: `action-vX`;
- typical consumer reference:
  `meigma/simplestreams-s3/action@action-v1`;
- security-sensitive consumers should pin the repository commit SHA while
  retaining an `action-vX.Y.Z` comment for Dependabot/readability.

Start the accepted public action contract at `action-v1.0.0`. Prototypes remain
unreleased on feature branches until the input/output contract and packaged
bundle pass review.

### Separate Release Please state

Create `action/release-please-config.json` and
`action/.release-please-manifest.json` with a Node release strategy for package
path `action`, component `action`, component-prefixed tags, and an initial
version of `1.0.0`. Release Please updates `action/package.json`, its lockfile,
`action/CHANGELOG.md`, and only the action manifest.

Create `.github/workflows/action-release-please.yml` using the existing Meigma
Release Please GitHub App credentials and full-SHA action pins. It runs
independently from `.github/workflows/release-please.yml`. After an exact action
release is created, the same App token moves `action-v<major>` to the released
commit. The App is already a tag-ruleset bypass actor; no personal token is
introduced.

Keep the CLI release lane independent in both directions:

- add action paths and `.github/workflows/action-*.yml` to the root Release
  Please package's `exclude-paths`, so an action feature does not bump the CLI;
- configure the action Release Please instance with only package path `action`,
  so CLI changes do not bump the action;
- leave the root manifest, `vX.Y.Z` tags, CLI changelog, draft asset workflow,
  and GoReleaser pipeline unchanged.

The action release contains no separately uploaded runtime asset. `dist/` is
committed and is executed from the tagged repository tree. Exact GitHub releases
remain immutable; only the documented major compatibility tag moves.

## Test strategy

### Fast tests on every action change

Adapt the canonical template's Jest fixtures and test at least:

- required and optional input parsing;
- multiline alias normalization and one-flag-per-alias behavior;
- deterministic argv construction with no shell;
- latest and explicit version normalization;
- OS/architecture asset selection and unsupported-platform failures;
- tool-cache hit without download;
- cache miss, release lookup, checksum verification, executable mode, and cache
  insertion;
- missing asset and checksum mismatch failures;
- CLI non-zero exit propagation;
- exact success-output parsing and action outputs;
- error-to-`core.setFailed` behavior without leaking tokens.

Run the canonical format, lint, Jest/coverage, and Rollup bundle commands. Add a
`check-dist` gate that rebuilds `action/dist/` and fails when the committed
bundle differs.

### Working-action integration proof

Add a path-focused `action-ci.yml` workflow using GitHub-hosted runners and
full-SHA action pins. The first proof should be small:

1. install the pinned public `v0.1.0` binary through the real GitHub release
   path and verify its reported version;
2. start MinIO with the existing hidden CLI test endpoint/path-style hooks;
3. generate or reuse the repository's tiny valid split-VM fixture;
4. invoke `uses: ./action` to publish it;
5. invoke the action a second time in the same job to prove both the tool-cache
   hit and the CLI's idempotent publication result;
6. inspect the resulting catalog/object set using existing repository test
   helpers rather than duplicating Simple Streams assertions in TypeScript.

No AWS secrets or write tokens run on pull requests. The existing CLI's genuine
AWS evidence remains valid for its S3 adapter; a protected, manually dispatched
real-AWS action test is a later option only if the wrapper exposes behavior that
MinIO and argument-level tests cannot prove.

### Dependency maintenance

Add an npm Dependabot entry for `/action` and retain the repository's existing
GitHub Actions update entry. Bundled dependency changes must pass tests,
licenses/security checks where adopted from the template, and the `check-dist`
gate.

## Delivery slices and review gates

### Slice 1: canonical import and installer proof

- Import the pinned upstream scaffold into `action/` and record provenance.
- Replace the example with version/platform resolution, checksum verification,
  and tool-cache installation.
- Add focused unit tests and a public `v0.1.0` download/version smoke test.
- Stop for review once the existing release installs successfully on Linux.

### Slice 2: publish wrapper

- Add `action.yml`, the complete input/output contract, direct argv building,
  and CLI result parsing.
- Add MinIO action integration and the same-job second invocation.
- Document AWS-auth-first usage and supported runners.
- Stop for review once a local action invocation publishes and repeats safely.

### Slice 3: independent CI and release lane

- Add action CI, npm Dependabot, bundle drift enforcement, and security pins.
- Add the action-specific Release Please config, manifest, workflow, changelog,
  and `action-vX` tag update.
- Exclude action-only paths from the CLI release package.
- Rehearse Release Please without merging its release PR.
- Stop for approval before creating `action-v1.0.0`.

### Slice 4: release and feedback

- After approval, merge the action release PR and verify the exact tag, GitHub
  release, and moving major tag.
- Run a consumer-style workflow using
  `meigma/simplestreams-s3/action@action-v1`.
- Record follow-up improvements learned from the first real consumer rather
  than expanding the first release spec preemptively.

## Acceptance criteria

- `action/` is visibly derived from the pinned canonical GitHub TypeScript
  action template and retains its useful quality gates.
- A caller can select `latest` or an explicit CLI version.
- The selected Linux/macOS amd64/arm64 binary is checksum-verified and installed
  through `@actions/tool-cache`; a same-job second run is a cache hit.
- The action publishes the two supplied image artifacts by directly invoking
  `simplestreams-s3 publish` with the documented controls.
- AWS authentication is entirely caller-owned and no credential values are
  action inputs or logs.
- Tests cover input/argument logic, install/cache integrity, failure paths,
  bundle drift, and a real local-action publication against MinIO.
- Action releases use independent `action-vX.Y.Z` / `action-vX` tags and cannot
  advance the CLI's `vX.Y.Z` version.
- The existing CLI release workflow, artifacts, and version state remain
  unchanged.

## Non-goals for the first release

- AWS authentication or role assumption;
- bucket creation, bucket policy management, or KMS provisioning;
- Windows support before a Windows CLI binary exists;
- proxy deployment or lifecycle management;
- arbitrary CLI subcommands or unvalidated free-form arguments;
- cross-workflow caching on ephemeral GitHub-hosted runners;
- Marketplace publication from this monorepo layout;
- automatic publication of the first action release without explicit approval.

## Primary references

- [GitHub's TypeScript action template](https://github.com/actions/typescript-action)
- [GitHub: creating a JavaScript action](https://docs.github.com/en/actions/tutorials/create-actions/create-a-javascript-action)
- [GitHub workflow syntax for a public action in a subdirectory](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax)
- [`@actions/tool-cache`](https://github.com/actions/toolkit/tree/main/packages/tool-cache)
- [Release Please manifest releaser](https://github.com/googleapis/release-please/blob/main/docs/manifest-releaser.md)
- [Release Please action](https://github.com/googleapis/release-please-action)
