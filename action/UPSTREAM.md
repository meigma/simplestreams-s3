# Upstream template provenance

This action started from GitHub's canonical TypeScript action template:

- Repository: <https://github.com/actions/typescript-action>
- Commit: `57b9acc0d972b482f0db345fa09703f3612fda95`
- Imported: 2026-07-20
- License notice: [`LICENSE.upstream`](LICENSE.upstream)

The initial import retained the template's Node 24 ESM runtime, TypeScript and
Rollup configuration, Jest setup, ESLint/Prettier policy, package lock, action
entrypoint shape, fixture pattern, and committed distribution-bundle model.

The template's wait example, sample metadata, repository-level workflows,
release script, local-action development utility, and repository administration
files were not imported. The unused local-action dependency also carried
avoidable vulnerable transitive packages at import time. The example
implementation was replaced by the `simplestreams-s3` release installer.
Repository-level CI and the unified repository release workflows are adapted
under the root `.github/workflows/` directory because GitHub does not load
workflows from `action/.github/workflows/`.

This is a pinned source baseline, not a merge-tracked fork. Future template
updates should be reviewed and imported deliberately.
