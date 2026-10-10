#!/bin/bash
# Demo: code review. Uploads a Go file with three planted issues; a read-only
# agent reviews it with line-level findings. Verified: all three found, plus
# an unplanned divide-by-zero on empty slices.
set -e
: "${WAVE_API_KEY:?export WAVE_API_KEY first}"
BASE=${BASE:-http://localhost:8080}
api() { curl -fsS "$BASE$1" -H "Authorization: Bearer $WAVE_API_KEY" "${@:2}"; }

cat > /tmp/paycalc.go <<'GO'
package paycalc

// Calc pays salaries: base + bonus, 20% above the 100k threshold.
func Calc(base, bonus int) int {
	total := base + bonus
	if total > 100000 {
		total = total * 0.8
	}
	return total
}

// Average mean salary.
func Average(pays []int) int {
	sum := 0
	for i := 0; i <= len(pays); i++ {
		sum += pays[i]
	}
	return sum / len(pays)
}
GO

E=$(api /v1/environments -H 'Content-Type: application/json' -d '{"name":"demo-plain","packages":{}}' | jq -er .id)
F=$(curl -fsS $BASE/v1/files -H "Authorization: Bearer $WAVE_API_KEY" -F file=@/tmp/paycalc.go | jq -er .id)
A=$(api /v1/agents -H 'Content-Type: application/json' -d '{"name":"demo-reviewer","model":"deepseek-chat","instructions":"你是资深 Go 代码评审。读用户上传的代码，按严重程度（bug/风险/风格）列出问题，每条给出行级说明和修复建议。只列真实存在的问题，不要凑数。","tools":[{"name":"read","kind":"builtin"},{"name":"ls","kind":"builtin"}]}' | jq -er .id)
S=$(api /v1/sessions -H 'Content-Type: application/json' -d "{\"title\":\"demo-review\",\"environment_id\":\"$E\",\"file_ids\":[\"$F\"]}" | jq -er .id)
T=$(api "/v1/sessions/$S/tasks" -H 'Content-Type: application/json' -d '{"agent_id":"'"$A"'","budget":{"max_tokens":8000,"max_tool_calls":10,"max_seconds":240},"input":"评审 /mnt/session/uploads/ 下上传的 paycalc.go，列出所有问题。"}' | jq -er .id)
echo "task=$T"
for i in $(seq 1 60); do
  R=$(api "/v1/tasks/$T"); ST=$(jq -r .state <<<"$R")
  case $ST in succeeded|failed|partial|canceled) break;; esac
  sleep 3
done
echo "state=$ST tokens=$(jq -r .used_tokens <<<"$R")"
jq -r '.result // .error // ""' <<<"$R"
