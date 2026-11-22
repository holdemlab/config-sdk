#!/bin/sh
# Computes the next SemVer tag from Conventional Commits since the last vX.Y.Z
# tag and creates the tag + GitHub Release in one `gh release create` call.
#
# Bump rules (must match CONTRIBUTING.md, sections 3 and 7):
#   `type!:` / `type(scope)!:` header or `BREAKING CHANGE:` footer → major
#   feat                                                          → minor
#   fix / perf / revert                                           → patch
#   anything else (docs, style, refactor, test, chore, ci, ...)   → no tag
#
# Go modules: for major >= 2 the module path in go.mod must end in /vN,
# otherwise `go get` rejects the tag. The script fails instead of publishing
# an unusable version.
#
# Env: GH_TOKEN (required unless DRY_RUN=1), DRY_RUN=1 to only print the tag.
set -eu

LAST_TAG="$(git tag -l 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n1 || true)"
if [ -z "$LAST_TAG" ]; then
  echo "No vX.Y.Z tag found: create the first one manually." >&2
  exit 1
fi

RANGE="${LAST_TAG}..HEAD"
SUBJECTS="$(git log "$RANGE" --format=%s)"
BODIES="$(git log "$RANGE" --format=%b)"

BUMP=""
if printf '%s\n' "$SUBJECTS" | grep -Eq '^[a-z]+(\([^)]*\))?!:' ||
  printf '%s\n' "$BODIES" | grep -Eq '^BREAKING[ -]CHANGE:'; then
  BUMP=major
elif printf '%s\n' "$SUBJECTS" | grep -Eq '^feat(\([^)]*\))?:'; then
  BUMP=minor
elif printf '%s\n' "$SUBJECTS" | grep -Eq '^(fix|perf|revert)(\([^)]*\))?:'; then
  BUMP=patch
fi

echo "Last tag: $LAST_TAG"
if [ -z "$BUMP" ]; then
  echo "No feat/fix/perf/revert/breaking commits since $LAST_TAG: skipping."
  exit 0
fi

VERSION="${LAST_TAG#v}"
MAJOR="${VERSION%%.*}"
REST="${VERSION#*.}"
MINOR="${REST%%.*}"
PATCH="${REST#*.}"

case "$BUMP" in
  major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
  minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
  patch) PATCH=$((PATCH + 1)) ;;
esac
NEW_TAG="v${MAJOR}.${MINOR}.${PATCH}"
echo "New tag: $NEW_TAG (bump: $BUMP)"

MODULE="$(sed -n 's/^module[[:space:]]*//p' go.mod)"
ACTUAL_SUFFIX="$(printf '%s\n' "$MODULE" | grep -Eo '/v[0-9]+$' || true)"
EXPECTED_SUFFIX=""
[ "$MAJOR" -ge 2 ] && EXPECTED_SUFFIX="/v${MAJOR}"
if [ "$ACTUAL_SUFFIX" != "$EXPECTED_SUFFIX" ]; then
  echo "go.mod module path '$MODULE' does not match $NEW_TAG" >&2
  echo "(expected suffix '${EXPECTED_SUFFIX:-none}'). Go would reject this tag." >&2
  exit 1
fi

if [ "${DRY_RUN:-}" = "1" ]; then
  echo "DRY_RUN=1: not creating $NEW_TAG."
  exit 0
fi

gh release create "$NEW_TAG" --target "$(git rev-parse HEAD)" --title "$NEW_TAG" --generate-notes
echo "Created release $NEW_TAG"
