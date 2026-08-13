#!/usr/bin/env bash
# Build one reproducible module release commit, including future-tag go.sum data.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BASE_INPUT="${1:?usage: seal-module-release.sh <base-ref> <plan-json>}"
PLAN_JSON="${2:?usage: seal-module-release.sh <base-ref> <plan-json>}"
cd "${ROOT}"
BASE_REF="$(git rev-parse "${BASE_INPUT}^{commit}")"

if ! git diff --quiet "${BASE_REF}" -- .github/module-versions.json; then
  :
else
  echo "module version manifest did not change" >&2
  exit 1
fi

module_dirs=()
while IFS= read -r module_dir; do
  module_dirs+=("${module_dir}")
done < <(jq -r '.order[]' "${PLAN_JSON}")
module_tags=()
while IFS= read -r module_tag; do
  module_tags+=("${module_tag}")
done < <(jq -r '.modules[].tag' "${PLAN_JSON}")
if [[ "${#module_dirs[@]}" -eq 0 ]]; then
  echo "module release plan is empty" >&2
  exit 1
fi

release_count="${#module_dirs[@]}"
first_commit="chore(modules): prepare ${release_count} Go module releases"
final_commit="chore(modules): release ${release_count} Go modules"

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git add .github/module-versions.json cmd pkg
git commit --signoff -m "${first_commit}"

tmp_modcache="$(mktemp -d)"
tmp_gitconfig="$(mktemp)"
cleanup() {
  for tag in "${module_tags[@]}"; do
    git tag -d "${tag}" >/dev/null 2>&1 || true
  done
  chmod -R u+w "${tmp_modcache}" 2>/dev/null || true
  rm -rf "${tmp_modcache}"
  rm -f "${tmp_gitconfig}"
}
trap cleanup EXIT

export GIT_CONFIG_GLOBAL="${tmp_gitconfig}"
export GIT_CONFIG_NOSYSTEM=1
git config --global protocol.file.allow always
git config --global url."file://${ROOT}/".insteadOf "https://github.com/soyaos/soyaos"

export GIT_ALLOW_PROTOCOL="file:https:http:ssh:git"
export GONOPROXY="github.com/soyaos/soyaos/*"
export GONOSUMDB="github.com/soyaos/soyaos/*"
export GOPROXY="https://proxy.golang.org,direct"
export GOTOOLCHAIN=local
export GOMODCACHE="${tmp_modcache}"

# Seal dependencies from leaves to dependents. Each temporary tag points at a
# commit where that module directory already has its final content.
for module_dir in "${module_dirs[@]}"; do
  tag="$(jq -r --arg directory "${module_dir}" '.modules[] | select(.directory == $directory) | .tag' "${PLAN_JSON}")"
  (
    cd "${module_dir}"
    GOWORK=off go mod tidy
  )
  if ! git diff --quiet -- "${module_dir}/go.mod" "${module_dir}/go.sum"; then
    git add "${module_dir}/go.mod" "${module_dir}/go.sum"
    git commit --signoff -m "chore(modules): seal ${module_dir} dependencies"
  fi
  git tag -f "${tag}" HEAD
done

# Collapse implementation commits. GitHub may squash the PR again; module zip
# hashes stay stable because every tagged subdirectory has identical content.
git reset --soft "${BASE_REF}"
git commit --signoff -m "${final_commit}"
for tag in "${module_tags[@]}"; do
  git tag -f "${tag}" HEAD
done

# Recompute from a fresh cache against tags all pointing to the final tree.
chmod -R u+w "${tmp_modcache}" 2>/dev/null || true
rm -rf "${tmp_modcache}"
mkdir -p "${tmp_modcache}"
for module_dir in "${module_dirs[@]}"; do
  (
    cd "${module_dir}"
    GOWORK=off go mod tidy
    GOWORK=off go test -count=1 ./...
  )
done
git diff --exit-code -- ':(glob)**/go.mod' ':(glob)**/go.sum'

echo "sealed ${release_count} module release(s) in $(git rev-parse HEAD)"
