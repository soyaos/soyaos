#!/usr/bin/env bash
# verify-compo-e2e.sh — full local end-to-end smoke of the SoyaPack lifecycle:
#                       validate → build → deploy → invoke → restart-reload.
#
# Walks the entire publisher path against an isolated soyaos instance:
#
#   1. ensure ./bin/soyaos is built (make build)
#   2. require .env with SOYA_MODEL_API_KEY (real LLM call in step 9)
#   3. boot soyaos on 127.0.0.1:7494 / :7495 with a mktemp data-dir
#   4. wait for /healthz
#   5. soyaos pack validate <example-essay-tutor>
#   6. soyaos agent build <example-essay-tutor> → ./dist/*.spk + .sha256
#   7. soyaos agent deploy <dist/*.spk> --rpc 127.0.0.1:7495
#   8. curl /v1/models — the deployed virtual_model_id must appear
#   9. curl /v1/chat/completions — fire a real prompt at the deployed agent
#  10. restart soyaos (kill+start, same data-dir) → boot-time reload registers
#      the deployed pack again, /v1/models still lists it
#  11. teardown + ✅ summary
#
# Exit code is 0 only when every numbered step succeeds; any failure halts
# the script with a non-zero status.
#
# The example pack at $EXAMPLE_PACK declares virtual_model_id: soya:compo,
# so the deployed agent surfaces under that id. If you point this script at
# a different SoyaPack repo, the deployed slug will mirror that manifest's
# expose.virtual_model_id.

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

LISTEN=127.0.0.1:7494
RPC=127.0.0.1:7495
DATA=$(mktemp -d -t soyaos-compo.XXXXXX)
LOG=$(mktemp -t soyaos-compo.log.XXXXXX)
BASE="http://${LISTEN}"
RPC_BASE="http://${RPC}"
KEY="sk-soya-dev-local"
EXAMPLE_PACK="${EXAMPLE_PACK:-/Users/zealot/workspace/soyaos/example-essay-tutor}"
LLM_TIMEOUT_S="${LLM_TIMEOUT_S:-300}"

BOLD=$'\033[1m'; DIM=$'\033[2m'; GREEN=$'\033[32m'; RED=$'\033[31m'; YELLOW=$'\033[33m'; CYAN=$'\033[36m'; RESET=$'\033[0m'

step() { echo; echo "${BOLD}${CYAN}━━━ $* ━━━${RESET}"; }
ok()   { echo "${GREEN}✓${RESET} $*"; }
bad()  { echo "${RED}✗${RESET} $*"; exit 1; }
note() { echo "${DIM}  $*${RESET}"; }
warn() { echo "${YELLOW}!${RESET} $*"; }

PID=""
cleanup() {
  if [[ -n "${PID:-}" ]] && kill -0 "$PID" 2>/dev/null; then
    kill -TERM "$PID" 2>/dev/null
    wait "$PID" 2>/dev/null
  fi
  rm -rf "$DATA" "$LOG"
}
trap cleanup EXIT

start_soyaos() {
  ./bin/soyaos start --listen "$LISTEN" --rpc "$RPC" --data-dir "$DATA" >> "$LOG" 2>&1 &
  PID=$!
  for _ in {1..40}; do
    if curl -sS -o /dev/null "$BASE/healthz"; then return 0; fi
    sleep 0.2
  done
  bad "soyaos failed to come up on $BASE; tail of log:\n$(tail -40 "$LOG")"
}

stop_soyaos() {
  if [[ -n "${PID:-}" ]] && kill -0 "$PID" 2>/dev/null; then
    kill -TERM "$PID" 2>/dev/null
    wait "$PID" 2>/dev/null
  fi
  PID=""
}

# -----------------------------------------------------------------------------
step "Step 1 · Ensure ./bin/soyaos is built"
if [[ ! -x ./bin/soyaos ]]; then
  note "binary missing — running make build"
  make build || bad "make build failed"
fi
ok "binary: $(./bin/soyaos version 2>&1 | head -1)"

# -----------------------------------------------------------------------------
step "Step 2 · .env must define SOYA_MODEL_API_KEY"
if [[ ! -f .env ]]; then
  bad ".env not found — copy .env.example and fill SOYA_MODEL_API_KEY"
fi
# shellcheck disable=SC2046
export $(grep -v '^#' .env | grep -v '^$' | xargs -I{} echo {})
if [[ -z "${SOYA_MODEL_API_KEY:-}" ]]; then
  bad "SOYA_MODEL_API_KEY is empty after sourcing .env"
fi
note "upstream: ${SOYA_MODEL_BASE_URL:-https://api.openai.com/v1} · model=${SOYA_MODEL_DEFAULT:-gpt-4o-mini}"
ok "BYOK env loaded"

# -----------------------------------------------------------------------------
step "Step 3 · Example pack: $EXAMPLE_PACK"
if [[ ! -f "$EXAMPLE_PACK/soyapack.yaml" ]]; then
  bad "$EXAMPLE_PACK/soyapack.yaml not found — set EXAMPLE_PACK env to override"
fi
VIRTUAL_MODEL_ID=$(grep -E '^[[:space:]]*virtual_model_id:' "$EXAMPLE_PACK/soyapack.yaml" | head -1 | sed 's/.*virtual_model_id:[[:space:]]*//')
PACK_NAME=$(grep -E '^name:' "$EXAMPLE_PACK/soyapack.yaml" | head -1 | sed 's/^name:[[:space:]]*//')
PACK_VERSION=$(grep -E '^version:' "$EXAMPLE_PACK/soyapack.yaml" | head -1 | sed 's/^version:[[:space:]]*//')
[[ -n "$VIRTUAL_MODEL_ID" ]] || bad "could not parse virtual_model_id from manifest"
note "manifest declares name=$PACK_NAME version=$PACK_VERSION virtual_model_id=$VIRTUAL_MODEL_ID"

# -----------------------------------------------------------------------------
step "Step 4 · Boot soyaos on $LISTEN (data → $DATA)"
start_soyaos
ok "soyaos pid=$PID"
sed -n '1,12p' "$LOG"

# -----------------------------------------------------------------------------
step "Step 5 · soyaos pack validate $EXAMPLE_PACK"
./bin/soyaos pack validate "$EXAMPLE_PACK" || bad "pack validate failed"
ok "manifest validates"

# -----------------------------------------------------------------------------
step "Step 6 · soyaos agent build $EXAMPLE_PACK"
BUILD_OUT=$(./bin/soyaos agent build "$EXAMPLE_PACK" 2>&1) || bad "agent build failed:\n$BUILD_OUT"
echo "$BUILD_OUT"
SPK_PATH=$(ls -1 "$EXAMPLE_PACK"/dist/${PACK_NAME}-${PACK_VERSION}.spk 2>/dev/null | head -1)
[[ -f "$SPK_PATH" ]] || bad ".spk not found at $EXAMPLE_PACK/dist/${PACK_NAME}-${PACK_VERSION}.spk"
SPK_SHA_FILE="$SPK_PATH.sha256"
[[ -f "$SPK_SHA_FILE" ]] || bad "sidecar .sha256 not next to $SPK_PATH"
SPK_BYTES=$(wc -c < "$SPK_PATH" | tr -d ' ')
SPK_SHA=$(awk '{print $1}' "$SPK_SHA_FILE")
note "spk: $SPK_PATH · size=${SPK_BYTES}B · sha256=$SPK_SHA"
ok "build emitted .spk + .sha256"

# -----------------------------------------------------------------------------
step "Step 7 · soyaos agent deploy $SPK_PATH --rpc $RPC_BASE"
DEPLOY_OUT=$(./bin/soyaos agent deploy "$SPK_PATH" --rpc "$RPC_BASE" 2>&1) || bad "agent deploy failed:\n$DEPLOY_OUT"
echo "$DEPLOY_OUT"
echo "$DEPLOY_OUT" | grep -q "deployed $VIRTUAL_MODEL_ID" || bad "deploy stdout missing 'deployed $VIRTUAL_MODEL_ID'"
ok "agent deployed"

# -----------------------------------------------------------------------------
step "Step 8 · /v1/models must include $VIRTUAL_MODEL_ID"
MODELS=$(curl -sS "$BASE/v1/models" -H "Authorization: Bearer $KEY")
echo "$MODELS" | jq -r '.data[].id'
if echo "$MODELS" | jq -e --arg id "$VIRTUAL_MODEL_ID" '.data | map(.id) | index($id) != null' >/dev/null; then
  ok "$VIRTUAL_MODEL_ID listed on /v1/models"
else
  bad "$VIRTUAL_MODEL_ID missing from /v1/models"
fi

# -----------------------------------------------------------------------------
step "Step 9 · /v1/chat/completions · real LLM round-trip through $VIRTUAL_MODEL_ID"
START=$(date +%s)
PROMPT_BODY=$(cat <<JSON
{
  "model": "$VIRTUAL_MODEL_ID",
  "stream": false,
  "messages": [
    {"role": "user", "content": "下面是一段范文：\\n\\n春天来了，校园里的玉兰花开了，白色的花瓣像小船一样停在枝头。我每天上学都会绕到花树下走一圈。\\n\\n请你按系统提示给出结构化分析。"}
  ]
}
JSON
)
ANSWER=$(curl -sS --max-time "$LLM_TIMEOUT_S" "$BASE/v1/chat/completions" \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d "$PROMPT_BODY") || bad "curl /v1/chat/completions failed (timeout=${LLM_TIMEOUT_S}s)"
ELAPSED=$(( $(date +%s) - START ))
echo "$ANSWER" | jq .
CONTENT=$(echo "$ANSWER" | jq -r '.choices[0].message.content // empty')
if [[ -z "$CONTENT" ]]; then
  bad "empty content in response — check upstream BYOK config:\n$ANSWER"
fi
echo
echo "${BOLD}--- response content (truncated to 400 chars) ---${RESET}"
echo "${CONTENT:0:400}"
echo
# Verify it actually went through an LLM (contains CJK characters).
if echo "$CONTENT" | LC_ALL=C grep -q '[^[:print:][:space:]]'; then
  ok "round-trip ${ELAPSED}s · response contains non-ASCII (CJK) bytes — real LLM"
else
  warn "response is all ASCII — upstream may have returned English; spot-check manually"
fi

# -----------------------------------------------------------------------------
step "Step 10 · Restart soyaos · deployed pack must re-load from $DATA/packs/"
stop_soyaos
note "old pid stopped — relaunching against the same data-dir"
start_soyaos
ok "soyaos restarted (pid=$PID)"
# Print the boot log lines mentioning pack reload.
grep -E "Re-loaded|Registered agents|warn:" "$LOG" | tail -20 || true

MODELS_AFTER=$(curl -sS "$BASE/v1/models" -H "Authorization: Bearer $KEY")
echo "$MODELS_AFTER" | jq -r '.data[].id'
if echo "$MODELS_AFTER" | jq -e --arg id "$VIRTUAL_MODEL_ID" '.data | map(.id) | index($id) != null' >/dev/null; then
  ok "$VIRTUAL_MODEL_ID survives restart (boot-time reload OK)"
else
  bad "$VIRTUAL_MODEL_ID disappeared after restart — boot reload broken"
fi

# -----------------------------------------------------------------------------
step "Summary"
ok "SoyaPack lifecycle E2E passed"
note "logs: $LOG"
note "data: $DATA (will be deleted on exit)"
note "spk:  $SPK_PATH ($SPK_BYTES bytes, sha256=$SPK_SHA)"
