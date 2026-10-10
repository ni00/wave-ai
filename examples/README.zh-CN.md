# 示例

针对已部署 Wave 实例的可执行端到端示例。每个脚本自行创建 agent、环境、文件与任务，
等待终态并打印结果。

## 前置条件

1. 已运行的部署（`make setup`、配置 `deploy/.env`，然后
   `docker compose --project-directory deploy up -d --build`）。
2. 宿主机已安装 gVisor（沙箱工具必需）：Ubuntu 上执行
   `sudo bash deploy/install-sandbox-host.sh`（见[部署文档](../docs/deployment.md)）。
3. 通过 `wave bootstrap` 创建的 API key。
4. PATH 中有 `curl` 和 `jq`。

## 运行

```bash
export WAVE_API_KEY=wa-xxxxxxxx            # wave bootstrap 生成
export BASE=http://localhost:8080          # 你的 Wave 地址
bash examples/01-data-analysis.sh
```

| 脚本 | 演示内容 |
| --- | --- |
| [01-data-analysis.sh](01-data-analysis.sh) | 环境绑定、文件上传、bash 工具、python 标准库分析、输出产物 |
| [02-code-review.sh](02-code-review.sh) | 只读评审 agent 审查上传的 Go 文件 |
| [03-writing-team.sh](03-writing-team.sh) | 多智能体委派：协调者通过 `expert_ids` 调度写手与编辑 |
| [04-schedule.sh](04-schedule.sh) | cron 定时任务：注册、手动触发、验证、暂停 |

## 注意

- 脚本幂等：每次运行都会创建新资源。
- 模型调用消耗 provider 配额；脚本为每个任务设置了显式预算
  （`max_tokens`、`max_tool_calls`、`max_seconds`）。
- 沙箱默认无网络。请使用标准库，或把依赖烘进自定义镜像（`WAVE_CONTAINER_IMAGE`）。
- 配置详见[部署文档](../docs/deployment.md)，本页英文版见
  [README.md](README.md)。
