#!/usr/bin/env bash

# Dispatch CI for a bot-created PR and mirror the real job conclusions to
# commit statuses. GitHub allows workflow_dispatch events created with
# GITHUB_TOKEN, but those check runs are not associated with the pull request
# for branch-protection purposes. The statuses below bridge that association;
# they never claim success unless the corresponding dispatched job succeeded.

set -euo pipefail

branch="${1:?usage: dispatch_protected_checks.sh BRANCH [MODULE_RELEASE_PR] [BASE_REF]}"
module_release_pr="${2:-false}"
base_ref="${3:-}"
repo="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"

head_sha="$(gh api "repos/${repo}/git/ref/heads/${branch}" --jq .object.sha)"

ci_args=(workflow run ci.yml --repo "${repo}" --ref "${branch}")
matrix_args=(workflow run matrix-build.yml --repo "${repo}" --ref "${branch}")
if [[ "${module_release_pr}" == "true" ]]; then
  ci_args+=(-f module_release_pr=true)
  matrix_args+=(-f module_release_pr=true)
  if [[ -n "${base_ref}" ]]; then
    ci_args+=(-f module_release_base_ref="${base_ref}")
    matrix_args+=(-f module_release_base_ref="${base_ref}")
  fi
fi

gh "${ci_args[@]}"
gh "${matrix_args[@]}"

run_id=""
for _ in $(seq 1 30); do
  run_id="$(
    gh run list \
      --repo "${repo}" \
      --workflow ci.yml \
      --branch "${branch}" \
      --event workflow_dispatch \
      --limit 10 \
      --json databaseId,headSha \
      | jq -r --arg sha "${head_sha}" \
        '[.[] | select(.headSha == $sha)] | sort_by(.databaseId) | last | .databaseId // empty'
  )"
  if [[ -n "${run_id}" ]]; then
    break
  fi
  sleep 2
done

if [[ -z "${run_id}" ]]; then
  echo "protected-checks: CI dispatch for ${head_sha} was not found" >&2
  exit 1
fi

# The repository currently has one known Chrome-startup flake. Retry failed
# jobs once; a deterministic failure remains failed and blocks the release.
if ! gh run watch "${run_id}" --repo "${repo}" --exit-status; then
  echo "protected-checks: retrying failed CI jobs once"
  gh run rerun "${run_id}" --repo "${repo}" --failed
  gh run watch "${run_id}" --repo "${repo}" --exit-status || true
fi

jobs="$(gh run view "${run_id}" --repo "${repo}" --json jobs)"
run_url="https://github.com/${repo}/actions/runs/${run_id}"
all_required_passed=true

for context in \
  'build & test (ubuntu-latest, 1.23.x)' \
  'build & test (macos-latest, 1.23.x)'; do
  conclusion="$(
    jq -r --arg context "${context}" \
      '[.jobs[] | select(.name == $context)] | last | .conclusion // "missing"' \
      <<< "${jobs}"
  )"
  state=failure
  if [[ "${conclusion}" == "success" ]]; then
    state=success
  else
    all_required_passed=false
  fi

  gh api \
    --method POST \
    "repos/${repo}/statuses/${head_sha}" \
    -f state="${state}" \
    -f context="${context}" \
    -f description="Protected CI ${conclusion} via workflow_dispatch" \
    -f target_url="${run_url}" \
    >/dev/null
done

if [[ "${all_required_passed}" != "true" ]]; then
  echo "protected-checks: one or more required CI jobs failed" >&2
  exit 1
fi

echo "protected-checks: required statuses passed for ${head_sha}"
