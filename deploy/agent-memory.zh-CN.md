# 配置 Agent 记忆

[English](agent-memory.md) | 简体中文

## 启用记忆

1. 通过 API 或 [CLI](../cli/README.zh-CN.md) 创建记忆库。
2. 创建会话时，通过 `memory_store_ids` 绑定记忆库 ID。其他会话需要使用这些信息时，绑定同一个记忆库即可。无需绑定环境。
3. 在 Agent 的工具列表中添加 `memory_read`、`memory_write` 和 `memory_delete` 内置工具，只启用 Agent 需要的操作。

工具只能访问当前调用者拥有且已绑定到当前会话的记忆库。

## 读取和修改条目

| 工具 | 必需参数 | 行为 |
| --- | --- | --- |
| `memory_read` | `store_id` | 省略 `path` 时列出条目，提供 `path` 时读取单条内容。 |
| `memory_write` | `store_id`、`path`、`content`、`version` | 版本匹配时保存内容。 |
| `memory_delete` | `store_id`、`path`、`version` | 版本匹配时将条目标记为已删除。 |

路径必须是规范的相对路径，例如 `preferences.md`。列表每页返回最多 100 条路径、版本和删除标记，不包含内容。将返回的 `next_offset` 作为 `offset` 继续读取；`next_offset: 0` 表示末页。

修改前先读取条目，再将返回的 `version` 传给写入或删除工具。从未创建过的路径返回版本 0。已删除的条目保留版本号，恢复时使用该版本。工具读写的单条内容上限为 1 MiB。

修改成功即持久化，之后任务失败也不会回滚。版本冲突时，当前条目保持不变，尝试写入的内容保留在冲突历史中。重新读取最新条目后再决定是否写入。
