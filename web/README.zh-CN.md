# Wave AI 后台

[English](README.md) | 简体中文

## 打开后台

启动 Wave 后，打开[后台](http://localhost:8080/console/)，使用 `wave bootstrap` 签发的 Wave API 密钥登录。密钥只保存在页面内存中，刷新后需重新登录。资源按所有者隔离，同组织的其他用户也不可见。

远程访问使用 HTTPS 或 SSH 隧道。在本地设置 `VPS_USER` 和 `VPS_HOST`：

```sh
ssh -N -L 18080:127.0.0.1:8080 "${VPS_USER}@${VPS_HOST}"
```

打开[隧道入口](http://localhost:18080/console/)。

## 浏览资源

| 分组 | 页面 |
| --- | --- |
| 监控 | Overview、Metrics、Traces、Logs |
| 资源与执行 | Agents、Skills、Deployments、Environments、Sandboxes、Files、Memory、Sessions、Tasks |
| 性能测试 | Benchmarks |

搜索、筛选列表，选择记录查看详情。`/` 聚焦搜索，↑/↓ 选择列表项，Escape 关闭详情。复制 URL 可分享当前资源。列表使用游标分页，详情按需加载；开启实时刷新后，仅在页面可见时每五秒刷新。

- **Agents：**创建、编辑模型、指令、工具和技能绑定，查看版本与关联任务。
- **Skills：**上传 ZIP、预览 `SKILL.md`、下载技能包，查看引用它的 Agent。
- **Deployments：**创建定时或手动运行配置，暂停、恢复调度，查看运行记录。
- **Environments** 是可复用的依赖和沙箱配置；**Sandboxes** 展示会话对应的实例、已记录状态和 CPU/内存配额。
- **Metrics**：选择 1 小时至 30 天或自定义范围。选择图表时间点，可打开该时段的 Trace、失败任务或日志；方向键可切换时间点。
- **Traces**：查看调用树、时间线和 Span 详情，跳转到关联任务、子任务或日志。
- **Logs**：按时间、级别、模块、消息、错误或 ID 筛选。Trace 链接定位到对应 Span。执行事件位于 **Tasks/Sessions → Events**。
- **Files**：以文本预览前 128 KiB。浏览器下载上限为 64 MiB，更大文件使用 `wavectl files download`。
- **Memory**：查看条目、版本和冲突。

指标口径、采集限制和保留时间见[可观测性](../tools/bench/OBSERVABILITY.zh-CN.md)。

## 对比压测结果

在 **Benchmarks** 中导入 `wave bench` 或 `wave bench live` 的 v2 JSON 报告，上限 16 MiB。报告作为文件持久保存。CLI 不自动上传；重复导入会创建多条记录。

从最近 200 份报告中选择基线，对比吞吐、成功率和 P50/P95/P99。工作负载、负载配置、模型及 worker 数须一致。回归检查使用 `wave bench compare`，参见[压测指南](../tools/bench/README.zh-CN.md)。

## 开发前端

启动本地 Wave API 后，在仓库根目录运行：

```sh
npm ci --ignore-scripts --prefix web
npm run dev --prefix web
```

代理默认指向 `http://127.0.0.1:8080`，可用 `WAVE_WEB_API_URL` 覆盖。

```sh
make web-build
make web-test
```
