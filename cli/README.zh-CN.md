# wavectl

[English](README.md) | 简体中文

`wavectl` 用于连接 Wave AI、管理 Agent 和任务。服务启动与运维使用 [`wave`](#服务管理)。

## 安装与连接

先[启动 Wave 并取得 API 密钥](../README.zh-CN.md#快速开始)。构建 CLI 需要 Go 1.23+ 和 Make，在仓库根目录执行：

```bash
make cli-build
export PATH="${XDG_CACHE_HOME:-$HOME/.cache}/wave-ai/bin:$PATH"
wavectl config set local --url http://localhost:8080
wavectl config set local --key-stdin < /secure/path/wave-api-key
wavectl config use local
wavectl doctor
```

将 `/secure/path/wave-api-key` 替换为保存 Wave API 密钥的文件。

`doctor` 检查数据库连通性和 API 认证；`doctor --offline` 只检查连接配置。模型和 worker 是否可用需通过实际任务验证。

### 连接配置

每个 profile 保存一组连接配置，文件位于用户配置目录下的 `wave-ai/config.json`。

| 配置 | 优先级，从高到低 |
| --- | --- |
| Profile | `--profile` → `WAVE_PROFILE` → 已保存的活动 profile → `default` |
| 服务地址 | `--url` → `WAVE_BASE_URL` → profile 地址 |
| API 密钥 | `WAVE_API_KEY` → profile 密钥 |

通过 `config list`、`show`、`use`、`remove` 和 `unset-key` 管理配置。本地移除密钥不会撤销服务端密钥。

## 提交与跟踪任务

```bash
wavectl agents create --name assistant --model YOUR_MODEL
wavectl sessions create --title research
wavectl tasks create --session SESSION --agent AGENT --input-file task.txt \
  --idempotency-key research-001 --wait
```

将 `YOUR_MODEL` 设为支持的模型，`AGENT`/`SESSION` 设为已有或新建资源的 ID。提交前将任务输入保存到 `task.txt`。

`--wait` 输出 `{task, reason, required_actions}`；根据退出码和 `task.state` 判断结果。超时后继续等待已有任务：

```bash
wavectl tasks get TASK --pretty
wavectl tasks wait TASK --timeout 10m
```

同一次提交重试时复用幂等键，新任务使用新键。保持同一会话可延续对话历史和固定文件。

### 处理待办动作

退出码 6 表示需要处理动作。读取 `required_actions` 中的 `type`、`task_id` 和 `call_id`，再选择命令：

| 动作 | 处理方式 |
| --- | --- |
| `approve_tool` | 确认允许执行后使用 `tools approve`，或使用 `tools reject` 拒绝 |
| `submit_tool_result` | 执行客户端工具，再使用 `tools result` 提交实际结果 |
| `confirm_tool_outcome`、`reconcile_task` | 核实外部执行情况，按[恢复流程](../skills/wave-client/references/recovery.md)处理 |

允许执行时：

```bash
wavectl tools approve CALL --task TASK
wavectl tasks wait TASK
```

出现 `submit_tool_result` 时，执行客户端工具并提交实际结果：

```bash
wavectl tools result CALL --task TASK --result-file result.txt
```

失败结果使用 `--is-error`，可附加 `--error-code`。重复决策不重复生效，冲突决策返回 HTTP 409。处理后重新等待。

## 查询命令与参数

```bash
wavectl schema                          # 契约、操作列表和版本。
wavectl schema tasks create --request   # 请求字段。
wavectl schema agents create --example  # 请求体模板。
wavectl tasks create --help             # 命令参数。
```

`completion bash|zsh|fish|powershell` 输出 shell 补全脚本。

必填参数标注 `(required)`。路径值可用具名参数或位置参数，两者不能重复指定。

## 输入、输出与文件

```bash
wavectl tasks create SESSION --body @request.json --dry-run
wavectl tasks get TASK --select /state --raw
wavectl agents list --all --format jsonl
wavectl events watch SESSION --cursor-file .wave/events.json --reconnects 3
wavectl files upload --file report.txt
wavectl files download FILE --output report.txt
```

`--body` 接受 JSON、`@file` 或 `-`，不能与字段参数混用。对象和数组字段支持 `@file`；文件参数用 `-` 读取标准输入，上传时还需 `--filename NAME`。`--dry-run` 仅在本地校验和预览请求。`--select` 使用 JSON Pointer。

JSON 与文本输入上限为 2 MiB，`false` 和空字符串均为有效值。stdout 默认输出 JSON 结果，stderr 输出诊断；表格输出使用 `--format table`。

普通请求默认超时 30 秒，任务等待 5 分钟，事件流不限时，均可用 `--timeout` 覆盖。事件游标文件只对应一个会话，重连后按事件 ID 去重。文件下载成功后才替换目标；`--output -` 输出原始字节。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| 0 | 操作或等待的任务成功 |
| 1 | 本地或网络错误 |
| 2 | 输入或用法错误 |
| 3 | 认证或授权错误 |
| 4 | 其他 API 错误 |
| 5 | 超时或中断；远端任务可能仍在运行 |
| 6 | 任务需要处理动作，查看 stdout |
| 7 | 任务未成功结束，读取 stdout |

错误以 JSON 写入 stderr，包含 `message`、`exit_code`，以及可用的 `hint`、`status` 和 `request_id`。使用 `set -e` 的脚本需显式处理任务等待的非零退出码，尤其是 6 和 7。

## 服务管理

`wave` 从环境变量读取配置。

| 命令 | 用途 |
| --- | --- |
| `wave serve` | 运行服务；`-role all\|api\|worker\|scheduler` 覆盖 `WAVE_ROLE` |
| `wave bootstrap` | 创建组织、用户和 API 密钥；`-org` 必填 |
| `wave init-db` | 在空数据库中创建当前表结构 |
| `wave config check` | 校验配置，输出不含密钥的 JSON 摘要 |
| `wave bench`、`wave bench sandbox` | 容量压测，见[压测说明](../tools/bench/README.zh-CN.md) |

`wave bootstrap -format key` 只输出原始密钥，可传给 `wavectl config set NAME --key-stdin`。完整部署、备份和诊断流程见 [wave-admin](../skills/wave-admin/SKILL.md)。

## 相关文档

[任务与工具流程](../skills/wave-client/SKILL.md) · [客户端工具链](../tools/clients/README.zh-CN.md) · [中文 API 契约](../api/openapi.zh-CN.json) · [英文 API 契约](../api/openapi.json)
