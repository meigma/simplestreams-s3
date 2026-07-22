# simplestreams-s3 action

This TypeScript action installs a checksum-verified `simplestreams-s3` release
through the GitHub runner tool cache, then publishes one split Incus VM image to
an existing private S3 mirror.

The action does not authenticate to AWS. Configure credentials first with AWS's
standard action, normally using GitHub OIDC, and grant only the S3 and KMS
permissions documented in the
[publisher guide](https://meigma.github.io/simplestreams-s3/publish-images/).

## Usage

```yaml
permissions:
  contents: read
  id-token: write

steps:
  - uses: actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0 # v7.0.0
    with:
      persist-credentials: false

  - uses: aws-actions/configure-aws-credentials@517a711dbcd0e402f90c77e7e2f81e849156e31d # v6.2.2
    with:
      role-to-assume: ${{ secrets.PUBLISH_ROLE_ARN }}
      aws-region: us-west-2

  - id: publish
    uses: meigma/simplestreams-s3@v0
    with:
      metadata-path: build/incus.tar.xz
      disk-path: build/disk.qcow2
      s3-bucket: private-images
      s3-prefix: mirrors/incus
      aliases: |
        example/stable
        example/latest
```

### Publish an attested image

Generate the image first, then pass the released `attest-vm-image` handoff
directly to this action:

```yaml
permissions:
  attestations: write
  contents: read
  id-token: write

steps:
  - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
    with:
      persist-credentials: false

  # Build build/incus.tar.xz and build/disk.qcow2 here.

  - id: attest
    uses: meigma/attest-vm-image@2646b5c7b0afc58f20a821ab44a5d0780733bd79 # v1.1.0
    with:
      disk-path: build/disk.qcow2
      metadata-path: build/incus.tar.xz
      signer: github

  - uses: aws-actions/configure-aws-credentials@517a711dbcd0e402f90c77e7e2f81e849156e31d # v6.2.2
    with:
      role-to-assume: ${{ secrets.PUBLISH_ROLE_ARN }}
      aws-region: us-west-2

  - uses: meigma/simplestreams-s3@v0
    with:
      metadata-path: build/incus.tar.xz
      disk-path: build/disk.qcow2
      evidence-manifest-path: ${{ steps.attest.outputs.evidence-manifest-path }}
      s3-bucket: private-images
      s3-region: us-west-2
```

`attest-vm-image` failures stop the job normally. As a second boundary, this
action rejects any supplied manifest whose result is not `pass`, whose disk or
metadata digest differs from the image, or whose proof files no longer match
their declared digests.

The moving `v0` tag selects the newest public compatible `v0.x.y` repository
release. Use an exact tag such as `v0.2.0` to select the action and CLI release
together, or pin the full release commit SHA for an immutable dependency. Use
`./` from a checkout for repository-local testing because `action.yml` lives at
the repository root. The moving tag becomes available with the first public
action-capable repository release.

## Interface

Required inputs are `metadata-path`, `disk-path`, and `s3-bucket`. `version`
defaults to the exact CLI version paired with the selected repository release;
it can be overridden with `latest`, `X.Y.Z`, or `vX.Y.Z`. `github-token` is an
optional read token for GitHub release downloads; public releases work without
it.

The publish controls map directly to the CLI and remain unset unless supplied:

- `evidence-manifest-path`, the optional version-1 `attest-vm-image` handoff;
- `config-file`, `s3-prefix`, `s3-region`, and `s3-expected-bucket-owner`;
- newline-separated `aliases` and `release-title`;
- `publish-timeout`, `catalog-timeout`, and `catalog-attempts`;
- `s3-max-attempts`, `s3-max-backoff`, `s3-dial-timeout`,
  `s3-tls-handshake-timeout`, and `s3-response-header-timeout`.

The action intentionally has no AWS access-key, session-token, role, or profile
input. The CLI consumes the AWS SDK default credential and region chains left by
the authentication step.

Successful publication exposes `cli-version`, `cli-path`, `product`, and
`image-version`. The action fails when installation, checksum verification, CLI
execution, or exact result parsing fails.

Linux and macOS runners on x64 and arm64 are supported. Windows is not supported
because the CLI does not publish a Windows binary.

## Development

The project is derived from GitHub's canonical TypeScript Action template; see
[`UPSTREAM.md`](UPSTREAM.md).

```sh
npm ci
npm run ci
```

The public release installer smoke test is opt-in:

```sh
npm run smoke:release
```

The repository's build-tagged Go integration test invokes the packaged action
twice against disposable MinIO. It proves real release installation, direct
publish execution, tool-cache reuse, and idempotent catalog state.

## Releases

The action and Go CLI share the repository's `vX.Y.Z` releases. Release Please
updates the root changelog and the paired default CLI version in `action.yml`.
The existing release workflow builds, attests, and stages the CLI artifacts in a
draft release. Publishing that inspected stable release moves the matching major
compatibility tag, such as `v0`, to the exact release commit.

The committed `action/dist/` bundle is the action artifact; no npm package or
separate action asset is published. An action-only fix therefore creates a new
repository patch release, deliberately keeping the wrapper and CLI versioned
together.

The repository CI rebuilds `dist/` and rejects uncommitted differences. A
release therefore contains only the bundle reviewed in its release PR.
