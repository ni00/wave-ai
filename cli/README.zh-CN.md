# wavectl

[English](README.md) | 简体中文

覆盖 Wave AI 全部 API 的远程 CLI，提供任务等待、工具审批、事件续读和流式文件传输。本机服务管理使用 `wave`。

## 安装与连接

在仓库根目录执行，只需要 Go：

```bash
make cli-build
export PATH="${XDG_CACHE_HOME:-$HOME/.cache}/wave-ai/bin:$PATH"
wavectl config set local --url http://localhost:8080
wavectl config set local --key-stdin < /secure/path/wave-api-key
wavectl config use local
wavectl doctor
```

配置保存在用户配置目录下的 `wave-ai/config.json`，原子写入，权限为 `0600`。`WAVE_PROFILE` 覆盖当前 Profile，`WAVE_BASE_URL`、`WAVE_API_KEY` 覆盖已保存值，显式 `--profile`/`--url` 优先。使用 `config list/show/use/remove/unset-key` 管理连接；查询不显示已保存的 Key，本地移除不会撤销服务端凭据。

`doctor` 检查数据库健康和 API 认证，`doctor --offline` 只检查连接配置，二者都不证明模型或 worker 可用。

## 提交与跟踪任务

```bash
wavectl agents list --format table
wavectl agents create --name assistant --model YOUR_MODEL
wavectl sessions create --title research
wavectl tasks create --session SESSION --agent AGENT --input-file task.txt \
  --idempotency-key research-001 --wait
wavectl tasks get TASK --pretty
wavectl tasks wait TASK --timeout 10m
```

优先复用已有 ID。`--wait` 返回 `{task, reason, required_actions}`，需要动作时退出码为 `6`。通过 `tools approve/reject CALL --task TASK` 提交已授权的决策，通过 `tools result CALL --task TASK --result-file result.txt` 提交真实结果。审批不会执行客户端工具；等待超时后继续查询已保存的任务 ID，不要重新创建任务。

## 输入、输出与文件

```bash
wavectl schema tasks create --request
wavectl tasks create SESSION --body @request.json --dry-run
wavectl tasks get TASK --select /state --raw
wavectl agents list --all --format jsonl
wavectl events watch SESSION --cursor-file .wave/events.json --reconnects 3
wavectl files upload --file report.txt
wavectl files download FILE --output report.txt
```

具名参数与完整 `--body` JSON 二选一。对象/数组支持 JSON 或 `@file`，文本文件参数支持 `-` 从 stdin 读取；显式 false 和空字符串会保留。`--dry-run` 在本地校验并输出脱敏预览，不联系服务端。JSON/文本输入上限为 2 MiB。

stdout 默认 JSON，诊断写入 stderr。`--format table` 方便阅读，`--select`（JSON Pointer）与 `--raw` 提取标量，`--all --format jsonl` 增量输出全部分页。普通请求默认超时 30 秒，等待 5 分钟，事件流不限时；可通过 `--timeout` 覆盖。

| 退出码 | 含义 |
| --- | --- |
| 0 | 操作或等待的任务成功 |
| 1–4 | 依次为本地/网络错误、输入错误、认证错误、其他 API 错误 |
| 5 | 超时/中断，远端任务可能仍在运行 |
| 6 | 任务需要动作，查看 stdout |
| 7 | 任务未成功结束，查看 stdout |

使用 `set -e` 的脚本应显式处理等待的非零退出码。游标文件只用于同一会话，恢复后按事件 ID 去重。下载成功后才替换目标文件，`--output -` 输出字节流；stdin 上传使用 `--file - --filename NAME`。

审批、恢复和产物操作见[客户端技能](../skills/wave-client/SKILL.md)，部署见[管理技能](../skills/wave-admin/SKILL.md)，检查与发布见[工具链](../tools/clients/README.zh-CN.md)。API 协议：[英文](../api/openapi.json)、[中文](../api/openapi.zh-CN.json)。
