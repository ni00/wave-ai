#!/bin/bash
# Demo: multi-agent writing team. A coordinator agent delegates to writer and
# editor experts via explicit expert_ids, then returns the edited final draft.
# Verified: succeeded across a writer→editor chain.
set -e
: "${WAVE_API_KEY:?export WAVE_API_KEY first}"
BASE=${BASE:-http://localhost:8080}
api() { curl -fsS "$BASE$1" -H "Authorization: Bearer $WAVE_API_KEY" "${@:2}"; }

W=$(api /v1/agents -H 'Content-Type: application/json' -d '{"name":"demo-writer","model":"deepseek-chat","instructions":"你是技术写手，负责把要点扩写成 200 字以内的短文，语言干净直接。"}' | jq -er .id)
E=$(api /v1/agents -H 'Content-Type: application/json' -d '{"name":"demo-editor","model":"deepseek-chat","instructions":"你是编辑，负责审查稿件的事实性与表达，直接输出修改后的最终稿。"}' | jq -er .id)
C=$(api /v1/agents -H 'Content-Type: application/json' -d '{"name":"demo-writing-team","model":"deepseek-chat","instructions":"你是写作小组负责人：把任务交给写手初稿，再交给编辑终审，最后只输出编辑后的终稿。","expert_ids":["'"$W"'","'"$E"'"],"delegation_policy":"explicit"}' | jq -er .id)
S=$(api /v1/sessions -H 'Content-Type: application/json' -d '{"title":"demo-writing-team"}' | jq -er .id)
T=$(api "/v1/sessions/$S/tasks" -H 'Content-Type: application/json' -d '{"agent_id":"'"$C"'","budget":{"max_tokens":16000,"max_tool_calls":8,"max_seconds":300},"input":"写一段 150 字左右的短文，主题：给个人 VPS 部署自己的 Agent 服务值不值。"}' | jq -er .id)
echo "writer=$W editor=$E coordinator=$C task=$T"
for i in $(seq 1 80); do
  R=$(api "/v1/tasks/$T"); ST=$(jq -r .state <<<"$R")
  case $ST in succeeded|failed|partial|canceled) break;; esac
  sleep 3
done
echo "state=$ST tokens=$(jq -r .used_tokens <<<"$R")"
jq -r '.result // .error // ""' <<<"$R"
