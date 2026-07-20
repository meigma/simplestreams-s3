#!/usr/bin/env bash

set -euo pipefail

exact_tag="${1:?usage: update_major_version_tag.sh vX.Y.Z}"
repository="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"
dry_run="${DRY_RUN:-false}"

if [[ ! "$exact_tag" =~ ^v([0-9]+)\.[0-9]+\.[0-9]+$ ]]; then
  echo "Exact release tag must match stable vX.Y.Z: $exact_tag" >&2
  exit 1
fi

major_tag="v${BASH_REMATCH[1]}"
release_json="$(gh release view "$exact_tag" \
  --repo "$repository" \
  --json isDraft,isPrerelease,publishedAt,tagName)"

jq -e --arg tag "$exact_tag" '
  .tagName == $tag
  and .isDraft == false
  and .isPrerelease == false
  and .publishedAt != null
' <<< "$release_json" >/dev/null

target_sha="$(gh api "repos/${repository}/commits/${exact_tag}" --jq .sha)"
if [[ ! "$target_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Failed to resolve $exact_tag to an exact commit: $target_sha" >&2
  exit 1
fi

if [ "$dry_run" = true ]; then
  echo "Would update $major_tag to $target_sha from public release $exact_tag"
  exit 0
fi

if [ "$dry_run" != false ]; then
  echo "DRY_RUN must be true or false" >&2
  exit 1
fi

if gh api "repos/${repository}/git/ref/tags/${major_tag}" >/dev/null 2>&1; then
  gh api --method PATCH \
    "repos/${repository}/git/refs/tags/${major_tag}" \
    --field sha="$target_sha" \
    --field force=true >/dev/null
else
  gh api --method POST \
    "repos/${repository}/git/refs" \
    --field ref="refs/tags/${major_tag}" \
    --field sha="$target_sha" >/dev/null
fi

echo "Updated $major_tag to $target_sha from public release $exact_tag"
