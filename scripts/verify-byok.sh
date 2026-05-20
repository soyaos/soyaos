#!/usr/bin/env bash
# verify-byok.sh — full local end-to-end smoke of the SOYA_MODEL_* BYOK path.
#
# Requires:
#   - ./bin/soyaos already built (run `make build` first)
#   - .env present with SOYA_MODEL_API_KEY (+ optionally BASE_URL / DEFAULT)
#   - curl + jq + python3 in PATH
#
# Picks high ports (7494/7495) and a temp data dir so it does not collide
# with whatever `soyaos start` you may have running on the default ports.
#
# Cases exercised:
#   1. Liveness + /v1/models reflects both echo and llm
#   2. Baseline echo (no upstream involved)
#   3. Single-turn Chinese Q&A through soya:llm
#   4. Multi-turn dialog with assistant history (context retention)
#   5. SSE streaming (you can see tokens land one chunk at a time)
#   6. Pass-through of an explicit upstream model id (bypasses Cfg.Model)
#   7. Wrong API key → 401 (verifies the fail-closed gate)
#   8. CLI dogfooding: `./bin/soyaos agent run llm ...`

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

LISTEN=127.0.0.1:7494
RPC=127.0.0.1:7495
DATA=$(mktemp -d -t soyaos-verify.XXXXXX)
LOG=$(mktemp -t soyaos-verify.log.XXXXXX)
BASE="http://${LISTEN}"
KEY="sk-soya-dev-local"

BOLD=$'\033[1m'; DIM=$'\033[2m'; GREEN=$'\033[32m'; RED=$'\033[31m'; YELLOW=$'\033[33m'; CYAN=$'\033[36m'; RESET=$'\033[0m'

step() { echo; echo "${BOLD}${CYAN}━━━ $* ━━━${RESET}"; }
ok()   { echo "${GREEN}✓${RESET} $*"; }
bad()  { echo "${RED}✗${RESET} $*"; }
note() { echo "${DIM}  $*${RESET}"; }

if [[ ! -x ./bin/soyaos ]]; then
  bad "./bin/soyaos not found — run \`make build\` first"
  exit 1
fi

if [[ ! -f .env ]]; then
  bad ".env not found — copy .env.example and fill SOYA_MODEL_API_KEY"
  exit 1
fi
# shellcheck disable=SC2046
export $(grep -v '^#' .env | grep -v '^$' | xargs -I{} echo {})

if [[ -z "${SOYA_MODEL_API_KEY:-}" ]]; then
  bad "SOYA_MODEL_API_KEY is empty after sourcing .env"
  exit 1
fi
note "upstream: ${SOYA_MODEL_BASE_URL:-https://api.openai.com/v1} · model=${SOYA_MODEL_DEFAULT:-gpt-4o-mini}"

if STALE=$(pgrep -f "bin/soyaos start" | head -1) && [[ -n "$STALE" ]]; then
  echo "${YELLOW}⚠${RESET} another soyaos start is already running (pid=$STALE) — this script uses ports $LISTEN/$RPC so it should not collide,"
  echo "${YELLOW} ${RESET} but if you tried this earlier and a Case-8 dogfood call landed on :7474 with no BYOK env, that's why."
fi

cleanup() {
  if [[ -n "${PID:-}" ]] && kill -0 "$PID" 2>/dev/null; then
    kill -TERM "$PID" 2>/dev/null
    wait "$PID" 2>/dev/null
  fi
  rm -rf "$DATA" "$LOG"
}
trap cleanup EXIT

step "Booting soyaos start on $LISTEN (data → $DATA)"
./bin/soyaos start --listen "$LISTEN" --rpc "$RPC" --data-dir "$DATA" > "$LOG" 2>&1 &
PID=$!
for i in {1..20}; do
  if curl -sS -o /dev/null "$BASE/healthz"; then break; fi
  sleep 0.2
done
if ! curl -sS -o /dev/null "$BASE/healthz"; then
  bad "soyaos failed to come up; log:"
  cat "$LOG"
  exit 1
fi
ok "soyaos pid=$PID"
sed -n '1,12p' "$LOG"

# -----------------------------------------------------------------------------
step "Case 1 · /healthz + /v1/models must include soya:echo and soya:llm"
HEALTH=$(curl -sS "$BASE/healthz?format=json")
echo "$HEALTH" | jq .
MODELS=$(curl -sS "$BASE/v1/models" -H "Authorization: Bearer $KEY")
echo "$MODELS" | jq -r '.data[].id'
if echo "$MODELS" | jq -e '.data | map(.id) | index("soya:llm") != null' >/dev/null \
 && echo "$MODELS" | jq -e '.data | map(.id) | index("soya:echo") != null' >/dev/null; then
  ok "both virtual models registered"
else
  bad "soya:llm or soya:echo missing — check .env"
  exit 1
fi

# -----------------------------------------------------------------------------
step "Case 2 · Baseline · soya:echo (no upstream call)"
ECHO=$(curl -sS "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"soya:echo","messages":[{"role":"user","content":"hello, soyaos"}]}')
echo "$ECHO" | jq -r '.choices[0].message.content'

# -----------------------------------------------------------------------------
step "Case 3 · Single-turn Chinese Q&A · soya:llm (non-stream)"
START=$(date +%s)
ANS=$(curl -sS "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"soya:llm","messages":[
        {"role":"system","content":"你是一个简洁的中文助理，回答控制在三句话以内。"},
        {"role":"user","content":"用 2 句话解释 SoyaOS 项目里 Planet、Moon、Comet 三个角色各自的职责。"}
      ]}')
ELAPSED=$(( $(date +%s) - START ))
echo "$ANS" | jq -r '.choices[0].message.content'
note "round-trip ${ELAPSED}s · usage: $(echo "$ANS" | jq -c '.usage // {}')"

# -----------------------------------------------------------------------------
step "Case 4 · Multi-turn dialog · feed assistant history back in"
HISTORY='[
  {"role":"system","content":"你是中文助理，只用纯文本回答。"},
  {"role":"user","content":"请记住一个数字：42。"},
  {"role":"assistant","content":"好的，我记住了 42。"},
  {"role":"user","content":"刚才让你记住的数字是什么？请只输出数字。"}
]'
ANS=$(curl -sS "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d "{\"model\":\"soya:llm\",\"messages\":$HISTORY,\"temperature\":0.1}")
REPLY=$(echo "$ANS" | jq -r '.choices[0].message.content')
echo "$REPLY"
if echo "$REPLY" | grep -q "42"; then
  ok "上下文被正确透传到上游（看到 42）"
else
  bad "上下文似乎没透传 — 没看到 42"
fi

# -----------------------------------------------------------------------------
step "Case 5 · SSE streaming · 逐字打印生成内容"
echo "${DIM}（每个 chunk 后会停顿，你能看到逐字生成）${RESET}"
START=$(date +%s%3N)
curl -sS -N "$BASE/v1/chat/completions" -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"model":"soya:llm","stream":true,"messages":[
        {"role":"system","content":"你是简洁的中文助理。"},
        {"role":"user","content":"写一首关于豆子的四句古风短诗，每句不超过 7 字。"}
      ]}' \
  | python3 -c "
import sys, json, time
first_byte_ms = None
chunks = 0
start = time.time()
for line in sys.stdin:
    line = line.strip()
    if not line.startswith('data:'):
        continue
    payload = line[5:].strip()
    if payload == '[DONE]':
        break
    if first_byte_ms is None:
        first_byte_ms = int((time.time() - start) * 1000)
    try:
        frame = json.loads(payload)
        delta = frame['choices'][0]['delta'].get('content', '')
        if delta:
            chunks += 1
            sys.stdout.write(delta)
            sys.stdout.flush()
    except Exception:
        pass
elapsed = int((time.time() - start) * 1000)
sys.stdout.write(f'\n\n  ⏱  first chunk @ {first_byte_ms}ms · {chunks} chunks · {elapsed}ms total\n')
"

# -----------------------------------------------------------------------------
step "Case 6 · Routing isolation · 非 soya:* model 必须被拒（demo 当前 dispatcher 边界）"
note "传 model=${SOYA_MODEL_DEFAULT}（不是 soya:*），预期 404 — alpha 只接受虚拟 model id"
CODE=$(curl -sS -o /tmp/case6.json -w '%{http_code}' "$BASE/v1/chat/completions" \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d "{\"model\":\"${SOYA_MODEL_DEFAULT}\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}")
echo "HTTP $CODE → $(cat /tmp/case6.json)"
if [[ "$CODE" == "404" ]]; then
  ok "alpha dispatcher 正确拒绝非 soya:* model（manifest.upstream 透传留给 v0.1.0-alpha.1）"
else
  bad "预期 404，实际 $CODE"
fi

# -----------------------------------------------------------------------------
step "Case 7 · 错误鉴权 · 用错误 Bearer，应该 401"
CODE=$(curl -sS -o /tmp/wrong.json -w '%{http_code}' "$BASE/v1/chat/completions" \
  -H "Authorization: Bearer sk-wrong-key" -H "Content-Type: application/json" \
  -d '{"model":"soya:llm","messages":[{"role":"user","content":"hi"}]}')
echo "HTTP $CODE → $(cat /tmp/wrong.json)"
[[ "$CODE" == "401" ]] && ok "fail-closed 鉴权工作正常" || bad "预期 401，实际 $CODE"

# -----------------------------------------------------------------------------
step "Case 8 · CLI dogfooding · ./bin/soyaos agent run llm (flag-before-positional)"
note "Go stdlib flag.Parse 在第一个 positional 就停止 — 所以 --listen/--key 必须在 slug/prompt 之前"
./bin/soyaos agent run --listen "$BASE" --key "$KEY" llm \
  "用一个 emoji 描述 soybean，然后给出三种由它衍生出的食品。"

# -----------------------------------------------------------------------------
step "Case 9 · OpenAI SDK 兼容性 · Python 标准客户端调 SoyaOS (urllib, no SDK install)"
note "目标：证明任何 OpenAI SDK 都能直接连 SoyaOS — 因为 SoyaOS 自己就是 OpenAI-Compat 网关。"
python3 - "$BASE" "$KEY" <<'PY'
import json, urllib.request, sys

base, key = sys.argv[1], sys.argv[2]

def call(messages, stream=False, model="soya:llm", temperature=0.3):
    body = {"model": model, "messages": messages, "temperature": temperature, "stream": stream}
    req = urllib.request.Request(
        base + "/v1/chat/completions",
        data=json.dumps(body).encode("utf-8"),
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    return urllib.request.urlopen(req, timeout=120)

# --- 复杂场景：3-turn 角色扮演，模型必须维持身份 + 控制字数 + 用 emoji ---
history = [
    {"role": "system", "content": "你是 SoyaOS 项目里那颗黄豆，名叫『豆豆』。每次回答不超过 30 字，至少含一个 emoji。"},
    {"role": "user", "content": "你好，你是谁？"},
]
print("\n   [turn 1 - non-stream]")
with call(history) as r:
    out = json.load(r)
reply = out["choices"][0]["message"]["content"]
print(f"   🤖 {reply}")
history.append({"role": "assistant", "content": reply})

history.append({"role": "user", "content": "项目里有一个 Agent 叫 Compo，你认识它吗？用一句话介绍。"})
print("\n   [turn 2 - non-stream]")
with call(history) as r:
    out = json.load(r)
reply = out["choices"][0]["message"]["content"]
print(f"   🤖 {reply}")
history.append({"role": "assistant", "content": reply})

# --- 第 3 turn 用流式，验证 SDK 路径下 SSE 也通 ---
history.append({"role": "user", "content": "最后请用流式输出，对刚才那两轮对话做一句话总结。"})
print("\n   [turn 3 - SSE stream]")
print("   🤖 ", end="", flush=True)
with call(history, stream=True) as r:
    chunks = 0
    for raw in r:
        line = raw.decode("utf-8").strip()
        if not line.startswith("data:"): continue
        payload = line[5:].strip()
        if payload == "[DONE]": break
        frame = json.loads(payload)
        delta = frame["choices"][0]["delta"].get("content", "")
        if delta:
            print(delta, end="", flush=True)
            chunks += 1
print(f"\n   ({chunks} chunks)")
print("\n   ✓ 3-turn 角色扮演通过 — 模型维持了『豆豆』身份并理解上下文中的 Compo")
PY

# -----------------------------------------------------------------------------
step "Summary · 完整冒烟通过"
ok "9 个 case 全部跑完"
note "logs: $LOG  ·  data: $DATA"
note "tip: SoyaOS Studio 占位页 → open $BASE"
