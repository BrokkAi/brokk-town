#!/bin/sh
# One-command release driver for brokk-town. See RELEASING.md.
#
#   scripts/release.sh v0.3.1              # validate, preflight, gather evidence; stop before publishing
#   scripts/release.sh v0.3.1 --publish    # ...then tag, publish, and certify (asks for confirmation)
#   scripts/release.sh v0.3.1 --dry-run    # print the plan without running anything
#
# The publish phase never runs by default: it requires the explicit --publish
# flag, and confirmation by retyping the tag unless --yes is given.
set -eu

REPO="BrokkAi/brokk-town"
WORKFLOW="publish-packages.yml"

TAG="${1:-}"
PUBLISH=0
YES=0
DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --publish) PUBLISH=1 ;;
    --yes) YES=1 ;;
    --dry-run) DRY_RUN=1 ;;
  esac
done

fail() { echo "release.sh: $*" >&2; exit 1; }

case "$TAG" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) fail "usage: scripts/release.sh vX.Y.Z [--publish] [--yes] [--dry-run]" ;;
esac
case "$TAG" in
  *-*) fail "release tag must be a stable version, got '$TAG'" ;;
esac

if [ "$DRY_RUN" = 1 ]; then
  echo "plan for $TAG (no commands executed):"
  echo "  1. require clean tree at origin/master, Go/Node/Python toolchains"
  echo "  2. make check smoke && python3 scripts/licenses.py && actionlint"
  echo "  3. release_checks.py build + version into a fresh dist/candidate"
  echo "  4. push preflight tag $TAG-preflight.<sha> and dispatch $WORKFLOW with publish=false"
  echo "  5. watch the run, verify its exact SHA, collect build/authorization/version evidence"
  if [ "$PUBLISH" = 1 ]; then
    echo "  6. re-verify authorization evidence, push final tag $TAG (never move it)"
    echo "  7. dispatch $WORKFLOW from $TAG with publish=true, watch to completion"
    echo "  8. download the candidate and run release_checks.py published"
  else
    echo "  6. stop before any publishing write (re-run with --publish to continue)"
  fi
  exit 0
fi

command -v git >/dev/null 2>&1 || fail "git not found"
command -v gh >/dev/null 2>&1 || fail "gh not found"
git rev-parse --show-toplevel >/dev/null 2>&1 || fail "not inside a git checkout"
[ -n "$(git status --porcelain)" ] && fail "working tree is not clean; commit or stash first"

echo "fetching origin..."
git fetch origin
MASTER_SHA="$(git rev-parse origin/master)"
HEAD_SHA="$(git rev-parse HEAD)"
[ "$HEAD_SHA" = "$MASTER_SHA" ] || fail "HEAD ($HEAD_SHA) is not origin/master ($MASTER_SHA); checkout the merged commit first"
echo "base: origin/master at $MASTER_SHA"

export RELEASE_COMMIT="$MASTER_SHA"
export RELEASE_TAG="$TAG"
SHORT_SHA="$(git rev-parse --short=8 "$MASTER_SHA")"
PREFLIGHT_TAG="${TAG}-preflight.${SHORT_SHA}"

echo "running local validation..."
make check smoke
python3 scripts/licenses.py
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
rm -rf dist/candidate
python3 scripts/release_checks.py build --directory dist/candidate
python3 scripts/release_checks.py version --directory dist/candidate
echo "local validation passed."

# Find the workflow_dispatch run for a ref that started after $1 (UTC timestamp).
find_run() {
  ref="$1"; since="$2"; tries=0
  while [ "$tries" -lt 30 ]; do
    RUN_ID="$(gh run list --repo "$REPO" --workflow "$WORKFLOW" --branch "$ref" \
      --limit 10 --json databaseId,headSha,event,createdAt \
      --jq "[.[] | select(.event == \"workflow_dispatch\" and .headSha == \"$MASTER_SHA\" and .createdAt >= \"$since\") | .databaseId] | first // empty")"
    if [ -n "$RUN_ID" ]; then echo "$RUN_ID"; return 0; fi
    tries=$((tries + 1)); sleep 10
  done
  return 1
}

watch_run() {
  run_id="$1"; label="$2"
  echo "watching $label run $run_id..."
  gh run watch --repo "$REPO" "$run_id" --exit-status
  echo "$label run $run_id completed successfully."
}

git ls-remote --tags origin "$PREFLIGHT_TAG" | grep -q . \
  && fail "preflight tag $PREFLIGHT_TAG already exists; never reuse a preflight tag"
git tag "$PREFLIGHT_TAG" "$MASTER_SHA"
git push origin "$PREFLIGHT_TAG"
echo "pushed preflight tag $PREFLIGHT_TAG."

SINCE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
gh workflow run "$WORKFLOW" --repo "github.com/$REPO" --ref "$PREFLIGHT_TAG" -f tag="$TAG" -F publish=false
PREFLIGHT_RUN="$(find_run "$PREFLIGHT_TAG" "$SINCE")" \
  || fail "preflight run did not appear; check 'gh run list --workflow $WORKFLOW'"
watch_run "$PREFLIGHT_RUN" "preflight"
python3 scripts/release_checks.py remote --gate build --run-id "$PREFLIGHT_RUN"
python3 scripts/release_checks.py remote --gate authorization --run-id "$PREFLIGHT_RUN"
python3 scripts/release_checks.py remote --gate version --run-id "$PREFLIGHT_RUN"
echo "preflight evidence collected for $TAG."

if [ "$PUBLISH" = 0 ]; then
  echo "stopping before any publishing write. To publish, re-run with --publish"
  echo "after a separate publication request. Authorization evidence expires after 15 minutes."
  exit 0
fi

if [ "$YES" = 0 ]; then
  printf "publish %s from %s? Retype the tag to confirm: " "$TAG" "$MASTER_SHA"
  read -r CONFIRM
  [ "$CONFIRM" = "$TAG" ] || fail "confirmation did not match; aborting before any publishing write"
fi

# Authorization evidence expires after 15 minutes; refresh it right before publishing.
python3 scripts/release_checks.py remote --gate authorization --run-id "$PREFLIGHT_RUN"

EXISTING="$(git ls-remote --tags origin "refs/tags/$TAG" | awk '{print $1}')"
if [ -n "$EXISTING" ]; then
  [ "$EXISTING" = "$MASTER_SHA" ] \
    || fail "tag $TAG already exists at $EXISTING; never move a published version tag"
  echo "final tag $TAG already points at $MASTER_SHA; reusing it."
else
  git tag "$TAG" "$MASTER_SHA"
  git push origin "$TAG"
  echo "pushed final tag $TAG."
fi

SINCE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
gh workflow run "$WORKFLOW" --repo "github.com/$REPO" --ref "$TAG" -f tag="$TAG" -F publish=true
PUBLISH_RUN="$(find_run "$TAG" "$SINCE")" \
  || fail "publish run did not appear; check 'gh run list --workflow $WORKFLOW'"
watch_run "$PUBLISH_RUN" "publish"

rm -rf dist/candidate
gh run download "$PUBLISH_RUN" --repo "github.com/$REPO" \
  --name "release-candidate-$MASTER_SHA-$TAG" --dir dist/candidate
python3 scripts/release_checks.py published --directory dist/candidate
echo "release $TAG certified."
