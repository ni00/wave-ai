#!/bin/bash
# Demo: cron schedule. Registers a daily deployment, fires it once manually to
# prove the scheduling pipeline, then pauses it so the daily cron stops
# consuming provider quota. Verified: manual fire succeeded; pause is
# confirmed via GET before the script reports success.
set -e
: "${WAVE_API_KEY:?export WAVE_API_KEY first}"
BASE=${BASE:-http://localhost:8080}
api() { curl -fsS "$BASE$1" -H "Authorization: Bearer $WAVE_API_KEY" "${@:2}"; }

A=$(api /v1/agents -H 'Content-Type: application/json' -d '{"name":"demo-digest","model":"deepseek-chat","instructions":"Reply with exactly: daily-digest-ok"}' | jq -er .id)
D=$(api /v1/deployments -H 'Content-Type: application/json' -d '{"name":"daily-site-digest","agent_id":"'"$A"'","input":"报数：回复 daily-digest-ok","cron":"0 9 * * *","timezone":"Asia/Shanghai","budget":{"max_tokens":2000,"max_tool_calls":2,"max_seconds":120}}' | jq -er .id)

# A live daily cron is a standing quota consumer, so pausing is the script's
# contract with its caller: PATCH must succeed AND a follow-up read must show
# paused=true. Any failure is surfaced as a non-zero exit, with one best-
# effort retry from the EXIT trap.
do_pause() {
  curl -fsS -X PATCH "$BASE/v1/deployments/$D" -H "Authorization: Bearer $WAVE_API_KEY" -H 'Content-Type: application/json' -d '{"paused":true}' >/dev/null || return 1
  [ "$(api "/v1/deployments/$D" | jq -r '.paused')" = "true" ]
}
PAUSED=false
on_exit() {
  if [ "$PAUSED" != true ]; then
    if do_pause; then
      PAUSED=true
      echo "(paused from exit trap)"
    else
      echo "WARNING: could not pause deployment $D — the daily cron remains active; pause it via PATCH /v1/deployments/$D" >&2
    fi
  fi
}
trap on_exit EXIT

echo "deployment=$D"
echo -n "next runs: "
api "/v1/deployments/$D/schedule" | jq -c '.times'
T=$(curl -fsS -X POST "$BASE/v1/deployments/$D/run" -H "Authorization: Bearer $WAVE_API_KEY" | jq -er '.task_id // .id')
echo "fired=$T"
sleep 12
api "/v1/tasks/$T" | jq -c '{state, result: (.result // .error)}'

do_pause || { echo "FAIL: pause failed after manual trigger" >&2; exit 1; }
PAUSED=true
trap - EXIT
echo -n "paused: "
api "/v1/deployments/$D" | jq -c '{paused, next_at}'
