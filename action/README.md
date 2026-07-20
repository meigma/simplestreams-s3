# simplestreams-s3 action

This directory contains the TypeScript GitHub Action for `simplestreams-s3`.

Slice 1 implements the release installer only. It resolves `latest` or an exact
CLI version, selects the Linux/macOS amd64/arm64 release binary, verifies it
against the release's `checksums.txt`, and installs it through the runner tool
cache. The publish wrapper is the next reviewed slice and is not implemented
yet.

The project is derived from GitHub's canonical TypeScript Action template; see
[`UPSTREAM.md`](UPSTREAM.md).

## Development

```sh
npm ci
npm run all
```

The public release smoke test performs real GitHub downloads and is opt-in:

```sh
npm run smoke:release
```

It resolves `latest` to `v0.1.0`, executes `simplestreams-s3 --version`, then
repeats with the explicit version to prove a tool-cache hit.
