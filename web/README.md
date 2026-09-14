# Wave AI Console

Wave 原生后台，入口是 `/console/`。按 Monitoring（Overview、Metrics、Traces、Logs）、资源与执行（Environment、File、Memory、Session、Task）、性能测试（Bench）分组。

## 运行

发布的 Wave 二进制和 Docker 镜像内置压缩后的前端资源，启动 `wave serve` 后即可访问 `http://localhost:8080/console/`。使用 `wave bootstrap` 签发的 Wave API Key 登录；密钥只留在页面内存，刷新或断开连接后需重新输入。模型供应商 API Key 不能用于登录后台。

后台 API 与现有 API 使用相同认证和所有者范围，同组织其他用户的资源也不可见。建议通过 HTTPS 反向代理或 SSH 隧道访问。服务绑定回环地址时，可通过隧道访问（在本地设置 `VPS_USER` 和 `VPS_HOST`）：

```sh
ssh -N -L 18080:127.0.0.1:8080 "${VPS_USER}@${VPS_HOST}"
# 浏览器打开 http://localhost:18080/console/
```

前端开发（先启动本地 Wave API）：

```sh
npm ci --ignore-scripts --prefix web
npm run dev --prefix web
# 默认 API http://127.0.0.1:8080；可用 WAVE_WEB_API_URL 覆盖代理目标
```

重建内置资源：`make web-build`。构建输出的 `internal/platform/webui/static/*.gz` 纳入版本控制，使普通 `go build` 不依赖 Node.js。修改前端后务必同时提交重建的资源。Docker 多阶段构建会从源码重新构建前端。`web/dist` 不纳入版本控制；Sites 打包兼容文件保留，但生产部署由 Wave 自身提供服务。

## 使用

- 左侧切换资源，列表按时间范围、状态、名称或 ID 搜索；`/` 聚焦搜索，↑/↓ 切换记录，Escape 返回列表。资源 ID 保存在 URL hash，可复制地址定位。桌面可拖动列表右下角改变宽度。
- Trace 提供虚拟调用树、时间线、耗时分布、首字延迟、token 使用量和工具输入输出。根任务关联子任务的 Trace；任务、日志、文件和 Session 可以相互跳转。Trace 不保存模型的完整请求体，相关任务输入及最终结果通过现有授权接口读取。
- Logs 是独立的结构化应用日志：按时间、级别、模块和消息/错误/ID 搜索，携带 Request、Session、Task、Trace、Span 关联。点击日志的 Trace 定位到对应 Span，Span 的 Logs 按钮只查该 Span。Task/Session 的 Events 页查看原始执行事件与 payload（排除逐 token 的 `message.delta`）。
- Overview 展示完成任务、成功率、任务 P95、Token 用量与趋势。Metrics 还提供 P50/P95/P99、模型首 Token 延迟、首次队列等待、沙箱启动和模型调用耗时。选择图表时间点（也支持左右方向键），可跳到该时段完成的任务、失败任务或日志。支持最近 1 小时至 30 天和自定义时间范围。
- Environment 展示沙箱 backend、profile 与包配置。Memory 可查看条目、历史版本、冲突；Session 可查看任务、消息及关联资源。
- File 提供文本预览和下载，预览最多 128 KiB。HTML 等内容仅当文本展示，不执行。浏览器内存下载上限 64 MiB，更大文件使用 `wavectl files download` 流式下载。
- 在 Bench 右上角导入 `wave bench` / `wave bench live` 生成的 **v2 JSON**（最大 16 MiB）。报告作为带专用 MIME 的文件保存，重启后仍可查询，不需要新数据库表。CLI 报告不会自动上传；同一报告重复导入会得到新的文件记录。
- Bench 展示各并发档位的吞吐量、成功率、P50/P95/P99 与任务 Trace；可选择最近 200 份报告作为基线。仅在工作负载、负载配置、模型和 worker 档位一致时计算差异。少量样本或失败/不完整采集不能作为优化通过的依据，正式回归判定使用 `wave bench compare`。

## 性能与验证

资源列表每页 50 条、日志每页 100 条，服务端最多 200 条，使用游标而非深 offset；列表省略提示词、快照、工具输出与日志 payload，详情按需加载。搜索有 250 ms 防抖，查询支持取消，缓存默认 15 秒；实时刷新由用户开启，仅页面可见时每 5 秒刷新。Trace 使用虚拟列表，20,000 层的调用树整理测试不依赖递归。

静态资源预压缩 gzip，带内容 hash 的资源缓存一年，HTML 使用 ETag 重新验证。页面使用本地字体，不请求第三方字体或遥测服务。首屏 JS gzip 约 92 KiB，Metrics、Logs、Trace 与资源详情分别拆包加载。

验证命令：`make web-test`、`make integration`、`make clients-check clients-test`、`make check`。集成测试使用真实 PostgreSQL 和对象存储，覆盖同组织跨用户隔离、非法参数、同时间戳日志分页、日志 payload 隔离、报告上传下载和专用 MIME 查询。`design-qa.md` 记录浏览器视觉与交互验证。

## 采集与指标口径

- 日志捕获已认证 API 请求和 Worker 任务上下文的 `slog` 记录；不会把 `trace.span` 事件翻译成日志。级别来自实际记录，模型/工具/阶段失败使用 error。没有所有者上下文的启动、调度维护等进程日志仍在 stderr，不向其他用户暴露。成功的监控查询仅写 stderr，防止 Logs 自动刷新制造自己的日志流。
- 持久化内容是消息名称、关联 ID 和允许的元数据。错误详情保存 `error_kind` / `error_code`，不复制供应商完整错误正文、请求头、提示词或工具内容。完整授权的执行结果仍在 Task/Events 中读取。
- 日志通过 4096 条有界队列异步批量写入，每批最多 128 条、最长等待 250 ms；批次失败会重试，队列饱和时新记录丢弃并输出 `monitor.logs_dropped`。日志属于 best effort，进程崩溃可能损失未落库记录，不作为审计账本。
- 指标样本与任务/模型状态事务一起提交；沙箱阶段沿用 Trace 的有界 best-effort 采集。所有服务角色运行增量聚合器，支持多进程竞争消费。更新分钟和小时桶、删除已消费样本在同一事务内完成；失败重试不会重复计数。没有基于 ID 水位跳过晚提交记录的问题。
- 指标查询只读 `monitor_buckets`，不扫描历史 Trace、Task 或 Generation。序列只有固定指标名与所有者范围，不含 Task ID、Session ID、模型名称等动态标签。图表最多约 721 点，底层一次最多读取 1442 个已聚合桶。
- 任务吞吐按完成时间分桶，根任务与子任务各计一次；成功率为 succeeded / 全部完成任务，分母包括 failed、partial、canceled。任务耗时从创建到完成；队列等待只记录首次开始前的等待；沙箱启动耗时是 `sandbox.ensure` 调用时长（包含已有沙箱检查）。指标中的模型调用包括重试、压缩与被中断调用；中断调用的未知耗时不会被计为零。
- Token 逐模型调用累计，避免根/子任务重复计数；Cached input 是 Input 的子集，不再次加到总量。未知用量不会补零，并显示不完整调用数。
- 延迟分位数由可合并对数直方图估计，桶宽 2%，不是对各分钟 P95 求平均。没有样本的延迟和成功率显示 `—`。API 返回实际向外对齐的时间范围；从图表进入任务列表使用 `finished_at`，不会错用创建时间。
- 日志保留 7 天、分钟桶 48 小时、小时桶 31 天；前台最多查最近 30 天。后台每分钟分批清理，单次最多 10 秒。未消费指标样本保留到成功聚合。指标页显示超过 5 秒的待聚合样本时间。
- 新表 `monitor_logs`、`monitor_samples`、`monitor_buckets` 纳入最终 GORM 模型。`wave init-db` 仍只初始化空库，不做自动升级；没有迁移 SQL 文件。指标从启用采集时开始，旧执行事件不回填为日志或指标。
