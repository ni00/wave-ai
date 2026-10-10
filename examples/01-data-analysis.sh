#!/bin/bash
# Demo: offline data analysis. Uploads a sales CSV, then an agent analyzes it
# in the sandbox with the python3 standard library and writes a summary
# artifact. Verified end-to-end: result totals match hand computation.
set -e
: "${WAVE_API_KEY:?export WAVE_API_KEY first}"
BASE=${BASE:-http://localhost:8080}
api() { curl -fsS "$BASE$1" -H "Authorization: Bearer $WAVE_API_KEY" "${@:2}"; }

cat > /tmp/sales.csv <<'CSV'
date,region,product,units,revenue
2026-09-01,华东,旗舰机,120,599600
2026-09-01,华南,入门机,300,297000
2026-09-02,华东,入门机,210,207900
2026-09-02,华北,旗舰机,80,399760
2026-09-03,华东,旗舰机,95,474010
2026-09-03,西南,入门机,150,148500
2026-09-04,华南,旗舰机,60,299800
2026-09-04,华东,入门机,180,178200
2026-09-05,华北,入门机,240,237600
2026-09-05,华东,旗舰机,110,548900
CSV

E=$(api /v1/environments -H 'Content-Type: application/json' -d '{"name":"demo-offline","packages":{}}' | jq -er .id)
F=$(curl -fsS $BASE/v1/files -H "Authorization: Bearer $WAVE_API_KEY" -F file=@/tmp/sales.csv | jq -er .id)
A=$(api /v1/agents -H 'Content-Type: application/json' -d '{"name":"demo-analyst2","model":"deepseek-chat","instructions":"你是数据分析助手。用 bash 运行 python3（只用标准库 csv）完成分析，结论里的数字必须来自计算输出。","tools":[{"name":"bash","kind":"builtin"}]}' | jq -er .id)
S=$(api /v1/sessions -H 'Content-Type: application/json' -d "{\"title\":\"demo-sales\",\"environment_id\":\"$E\",\"file_ids\":[\"$F\"]}" | jq -er .id)
T=$(api "/v1/sessions/$S/tasks" -H 'Content-Type: application/json' -d '{"agent_id":"'"$A"'","budget":{"max_tokens":16000,"max_tool_calls":15,"max_seconds":300},"input":"上传的销售数据在 /mnt/session/uploads/ 下（sales.csv）。用 python3 标准库 csv 分析：1) 总营收 2) 分产品营收占比 3) 营收最高的一天。把汇总写进 /mnt/session/outputs/summary.md，并回答三个结论。"}' | jq -er .id)
echo "task=$T"
for i in $(seq 1 80); do
  R=$(api "/v1/tasks/$T"); ST=$(jq -r .state <<<"$R")
  case $ST in succeeded|failed|partial|canceled) break;; esac
  sleep 3
done
echo "state=$ST tokens=$(jq -r .used_tokens <<<"$R")"
jq -r '.result // .error // ""' <<<"$R"
