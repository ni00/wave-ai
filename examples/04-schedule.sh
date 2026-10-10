#!/bin/bash
# Demo: cron schedule. Registers a daily deployment, fires it once manually to
# prove the scheduling pipeline, then pauses it so the daily cron stops
# consuming provider quota. Verified: manual fire succeeded.
set -e
: "${WAVE_API_KEY:?export WAVE_API_KEY first}"
BASE=${BASE:-http://localhost:8080}
api() { curl -fsS "$BASE$1" -H "Authorization: Bearer $WAVE_API_KEY" "${@:2}"; }

A=$(api /v1/agents -H 'Content-Type: application/json' -d '{"name":"demo-digest","model":"deepseek-chat","instructions":"Reply with exactly: daily-digest-ok"}' | jq -er .id)
D=$(api /v1/deployments -H 'Content-Type: application/json' -d '{"name":"daily-site-digest","agent_id":"'"$A"'","input":"报数：回复 daily-digest-ok","cron":"0 9 * * *","timezone":"Asia/Shanghai","budget":{"max_tokens":2000,"max_tool_calls":2,"max_seconds":120}}' | jq -er .id)
pause() { curl -fsS -X PATCH "$BASE/v1/deployments/$D" -H "Authorization: Bearer $WAVE_API_KEY" -H 'Content-Type: application/json' -d '{"paused":true}' >/dev/null && echo "(deployment paused)" || true; }
trap 'pause' EXIT
echo "deployment=$D"
echo -n "next runs: "
api "/v1/deployments/$D/schedule" | jq -c '.times'
T=$(curl -fsS -X POST "$BASE/v1/deployments/$D/run" -H "Authorization: Bearer $WAVE_API_KEY" | jq -er '.task_id // .id')
echo "fired=$T"
sleep 12
api "/v1/tasks/$T" | jq -c '{state, result: (.result // .error)}'
pause
trap - EXIT
echo -n "paused: "
api "/v1/deployments/$D" | jq -c '{paused, next_at}'
