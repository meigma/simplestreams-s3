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
    uses: meigma/simplestreams-s3/action@<full-commit-sha> # action-v1.0.0 after release
    with:
      version: v0.1.0
      metadata-path: build/incus.tar.xz
      disk-path: build/disk.qcow2
      s3-bucket: private-images
      s3-prefix: mirrors/incus
      aliases: |
        example/stable
        example/latest
```

Until the independently versioned action is released, use `./action` from a
checkout for repository-local testing. Consumers should pin a full repository
commit SHA even after `action-v1` becomes available.

## Interface

Required inputs are `metadata-path`, `disk-path`, and `s3-bucket`. `version`
defaults to `latest` and also accepts `X.Y.Z` or `vX.Y.Z`. `github-token` is an
optional read token for GitHub release downloads; public releases work without
it.

The publish controls map directly to the CLI and remain unset unless supplied:

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
npm run all
```

The public release installer smoke test is opt-in:

```sh
npm run smoke:release
```

The repository's build-tagged Go integration test invokes the packaged action
twice against disposable MinIO. It proves real release installation, direct
publish execution, tool-cache reuse, and idempotent catalog state.
