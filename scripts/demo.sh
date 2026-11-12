#!/usr/bin/env bash
# 通用 Agent 最小验证脚本。
# 用法:
#   AGENT_USERNAME=alice AGENT_PASSWORD='your-password' ./scripts/demo.sh
# 先通过 cmd/agentadmin 创建账号。
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8091}"
USERNAME="${AGENT_USERNAME:?请设置 AGENT_USERNAME}"
PASSWORD="${AGENT_PASSWORD:?请设置 AGENT_PASSWORD}"

json_field() {
  python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get(sys.argv[1], ""))' "$1"
}

echo "==> 1. 登录"
LOGIN=$(curl -sS -X POST "${BASE_URL}/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "$(python3 -c 'import json,os; print(json.dumps({"username": os.environ["AGENT_USERNAME"], "password": os.environ["AGENT_PASSWORD"]}))')")
TOKEN=$(printf '%s' "${LOGIN}" | json_field token)
if [ -z "${TOKEN}" ]; then
  echo "登录失败: ${LOGIN}"
  exit 1
fi
AUTH="Authorization: Bearer ${TOKEN}"

echo "==> 2. 创建会话"
CONV=$(curl -sS -X POST "${BASE_URL}/v1/conversations" -H "${AUTH}")
CONV_ID=$(printf '%s' "${CONV}" | json_field id)
echo "conversation=${CONV_ID}"

echo "==> 3. 提交问题"
RUN=$(curl -sS -X POST "${BASE_URL}/v1/conversations/${CONV_ID}/messages" \
  -H "${AUTH}" -H 'Content-Type: application/json' \
  -d '{"content":"请查看当前有哪些 Prometheus 告警，并检索相关 Runbook。"}')
RUN_ID=$(printf '%s' "${RUN}" | json_field run_id)
echo "run=${RUN_ID}"

echo "==> 4. 订阅实时事件（最多 60 秒，Ctrl-C 只断开展示，不取消运行）"
curl -sS -N --max-time 60 "${BASE_URL}/v1/runs/${RUN_ID}/events" -H "${AUTH}" || true

echo ""
echo "==> 5. 查询运行与待审批项"
curl -sS "${BASE_URL}/v1/runs/${RUN_ID}" -H "${AUTH}"; echo
curl -sS "${BASE_URL}/v1/runs/${RUN_ID}/approvals" -H "${AUTH}"; echo

echo "==> 6. 查询完整会话记录"
curl -sS "${BASE_URL}/v1/conversations/${CONV_ID}" -H "${AUTH}"; echo

echo "==> 完成"
