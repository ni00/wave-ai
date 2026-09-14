# Wave AI Console

Wave 原生后台，入口是 `/console/`。提供 Traces、Logs、Environments、Files、Memory、Sessions、Tasks 和 Bench 八类资源的查询与详情。

## 运行

发布的 Wave 二进制和 Docker 镜像内置压缩后的前端资源，启动 `wave serve` 后即可访问 `http://localhost:8080/console/`。使用 `wave bootstrap` 签发的 Wave API Key 登录；密钥只留在页面内存，刷新或断开连接后需重新输入。模型供应商 API Key 不能用于登录后台。

后台 API 与现有 API 使用相同认证和所有者范围，同组织其他用户的资源也不可见。建议通过 HTTPS 反向代理或 SSH 隧道访问。现有 VPS 的服务仍绑定回环地址，可使用：

```sh
ssh -N -L 18080:127.0.0.1:8080 ubuntu@${VPS_HOST}
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
- Logs 展示持久化执行事件，排除 `message.delta`。它不是容器 stdout 聚合器；进程 JSON 日志继续由部署日志系统保存。
- Environment 展示沙箱 backend、profile 与包配置。Memory 可查看条目、历史版本、冲突；Session 可查看任务、消息及关联资源。
- File 提供文本预览和下载，预览最多 128 KiB。HTML 等内容仅当文本展示，不执行。浏览器内存下载上限 64 MiB，更大文件使用 `wavectl files download` 流式下载。
- 在 Bench 右上角导入 `wave bench` / `wave bench live` 生成的 **v2 JSON**（最大 16 MiB）。报告作为带专用 MIME 的文件保存，重启后仍可查询，不需要新数据库表。CLI 报告不会自动上传；同一报告重复导入会得到新的文件记录。
- Bench 展示各并发档位的吞吐量、成功率、P50/P95/P99 与任务 Trace；可选择最近 200 份报告作为基线。仅在工作负载、负载配置、模型和 worker 档位一致时计算差异。少量样本或失败/不完整采集不能作为优化通过的依据，正式回归判定使用 `wave bench compare`。

## 性能与验证

列表每页 50 条，服务端最多 200 条，使用游标而非深 offset；列表省略提示词、快照、工具输出与日志 payload，详情按需加载。搜索有 250 ms 防抖，查询支持取消，缓存默认 15 秒；实时刷新由用户开启，仅页面可见时每 5 秒刷新。Trace 使用虚拟列表，20,000 层的调用树整理测试不依赖递归。

静态资源预压缩 gzip，带内容 hash 的资源缓存一年，HTML 使用 ETag 重新验证。页面使用本地字体，不请求第三方字体或遥测服务。首屏 JS gzip 约 91 KiB，Trace 与资源详情拆包加载。

验证命令：`make web-test`、`make integration`、`make clients-check clients-test`、`make check`。集成测试使用真实 PostgreSQL 和对象存储，覆盖同组织跨用户隔离、非法参数、同时间戳日志分页、日志 payload 隔离、报告上传下载和专用 MIME 查询。`design-qa.md` 记录浏览器视觉与交互验证。
