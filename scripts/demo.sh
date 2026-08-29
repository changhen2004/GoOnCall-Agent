#!/usr/bin/env bash
# GoOnCall Agent v1.0 端到端 Demo
# 用法: BASE_URL=http://127.0.0.1:8080 ./scripts/demo.sh
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8080}"

echo "==> 1. 模拟 GoCommunity Prometheus 告警（Alertmanager webhook）"
CREATE=$(curl -s -X POST "${BASE_URL}/api/v1/alerts" -H 'Content-Type: application/json' -d '{
  "status": "firing",
  "alerts": [{
    "status": "firing",
    "labels": {"alertname": "ResourceCommunityHighErrorRate", "service": "resource_community_go", "severity": "warning"},
    "annotations": {"summary": "resource_community_go 5xx error rate is high", "description": "5xx error rate is high over the last minute."}
  }]
}')
echo "${CREATE}"

INCIDENT_ID=$(echo "${CREATE}" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d["created"][0] if d.get("created") else "")')
if [ -z "${INCIDENT_ID}" ]; then
  echo "未创建新 Incident（可能已去重）"
fi

echo ""
echo "==> 2. 查看 Incident 列表"
curl -s "${BASE_URL}/api/v1/incidents"; echo

echo ""
echo "==> 3. 分析 Incident（已配置 LLM 时触发诊断 Agent）"
if [ -n "${INCIDENT_ID}" ]; then
  curl -s -X POST "${BASE_URL}/api/v1/incidents/${INCIDENT_ID}/analyze"; echo
fi

echo ""
echo "==> 4. 告警恢复（关闭 Incident）"
curl -s -X POST "${BASE_URL}/api/v1/alerts" -H 'Content-Type: application/json' -d '{
  "status": "resolved",
  "alerts": [{
    "status": "resolved",
    "labels": {"alertname": "ResourceCommunityHighErrorRate", "service": "resource_community_go"},
    "annotations": {"summary": "resource_community_go 5xx error rate is high"}
  }]
}'; echo

echo ""
echo "==> 完成"
