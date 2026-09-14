import React, { useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  IconListTree,
  IconChartBar,
  IconRobot,
  IconSparkles,
  IconTool,
  IconClock,
  IconBox,
  IconChevronRight,
  IconChevronDown,
  IconArrowUpRight,
  IconSearch,
} from "@tabler/icons-react";
import {
  useAPI,
  useResource,
  duration,
  date,
  number,
  route,
  short,
  flattenSpans,
} from "./data.js";
import {
  Load,
  Section,
  Code,
  Badge,
  Metric,
  Fields,
  Tabs,
  Copy,
  Empty,
} from "./ui.jsx";
const icons = {
  task: IconRobot,
  child: IconRobot,
  model: IconSparkles,
  tool: IconTool,
  tool_wait: IconClock,
  queue: IconClock,
  phase: IconBox,
};
export default function Trace({ id, live }) {
  const api = useAPI();
  const q = useQuery({
    queryKey: ["executionTrace", id],
    queryFn: ({ signal }) =>
      api.call("executionTrace", { path: { id }, signal }).then((r) => r.data),
    refetchInterval: live ? 5000 : false,
    refetchIntervalInBackground: false,
  });
  return <Load query={q}>{(data) => <TraceView trace={data} />}</Load>;
}
function TraceView({ trace }) {
  const [tab, setTab] = useState("tree"),
    [active, setActive] = useState(""),
    [collapsed, setCollapsed] = useState(new Set()),
    [search, setSearch] = useState("");
  const spans = trace.spans || [],
    summary = trace.summary,
    span = spans.find((s) => s.id === active) || spans[0];
  const rows = useMemo(
    () =>
      flattenSpans(spans, search ? new Set() : collapsed).filter(
        (s) =>
          !search ||
          `${s.name} ${s.kind} ${s.state}`
            .toLowerCase()
            .includes(search.toLowerCase()),
      ),
    [spans, collapsed, search],
  );
  const scroll = useRef();
  const virtual = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroll.current,
    estimateSize: () => 37,
    overscan: 8,
  });
  const start = Date.parse(spans[0]?.started_at),
    wall =
      summary.wall_ms ||
      Math.max(
        1,
        ...spans.map(
          (s) => Date.parse(s.started_at) - start + (s.duration_ms || 0),
        ),
      );
  const longest = spans
    .filter((s) => ["model", "tool", "phase"].includes(s.kind))
    .reduce(
      (a, b) => (!a || (b.duration_ms || 0) > (a.duration_ms || 0) ? b : a),
      null,
    );
  function toggle(id) {
    setCollapsed((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }
  return (
    <>
      <div className="trace-toolbar">
        <Tabs
          items={[
            ["tree", "Trace", IconListTree],
            ["timeline", "Timeline", IconChartBar],
            ["summary", "Overview", IconChartBar],
          ]}
          value={tab}
          onChange={setTab}
        />
        <Badge state={trace.state} />
        <span className="subtle">{spans.length} spans</span>
      </div>
      {(trace.incomplete || trace.truncated) && (
        <div className="notice">
          {trace.truncated
            ? "此 Trace 超出采集上限，展示部分记录。"
            : "此 Trace 尚不完整；运行中的任务或早期任务可能缺少部分阶段。"}
        </div>
      )}
      <div
        className={
          "trace-layout " +
          (tab === "timeline"
            ? "timeline-mode"
            : tab === "summary"
              ? "summary-mode"
              : "")
        }
      >
        <section className="trace-tree">
          <div className="pane-label">
            <span>{tab === "tree" ? "Trace tree" : "Execution timeline"}</span>
            <span>{duration(summary.wall_ms)}</span>
          </div>
          <label className="span-search">
            <IconSearch size={14} />
            <input
              aria-label="搜索 Span"
              placeholder="查找 span…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          {tab === "timeline" && (
            <div className="timeline-axis">
              <span>0s</span>
              <span>{duration(wall / 2)}</span>
              <span>{duration(wall)}</span>
            </div>
          )}
          <div
            className="tree-scroll"
            ref={scroll}
            role="tree"
            aria-label="调用树"
          >
            <div
              style={{ height: virtual.getTotalSize(), position: "relative" }}
            >
              {virtual.getVirtualItems().map((v) => {
                const s = rows[v.index],
                  Icon = icons[s.kind] || IconBox;
                return (
                  <div
                    role="treeitem"
                    aria-level={s.depth + 1}
                    aria-selected={span?.id === s.id}
                    key={s.id}
                    className={
                      "span-row " +
                      (span?.id === s.id ? "active " : "") +
                      (tab === "timeline" ? "timeline-row" : "")
                    }
                    style={{
                      position: "absolute",
                      top: 0,
                      left: 0,
                      width: "100%",
                      height: v.size,
                      transform: `translateY(${v.start}px)`,
                    }}
                  >
                    <div
                      className="span-name"
                      style={{ paddingLeft: 8 + Math.min(s.depth, 12) * 18 }}
                    >
                      {s.hasChildren ? (
                        <button
                          className="tree-toggle"
                          aria-label={
                            (collapsed.has(s.id) ? "展开" : "折叠") +
                            " " +
                            s.name
                          }
                          onClick={() => toggle(s.id)}
                        >
                          {collapsed.has(s.id) ? (
                            <IconChevronRight size={13} />
                          ) : (
                            <IconChevronDown size={13} />
                          )}
                        </button>
                      ) : (
                        <span className="tree-indent" />
                      )}
                      <button
                        className="span-select"
                        onClick={() => setActive(s.id)}
                        title={s.name}
                      >
                        <span className={"span-icon " + s.kind}>
                          <Icon size={15} />
                        </span>
                        <span className="truncate">{s.name}</span>
                        {s.state === "failed" && (
                          <span className="error-tag">ERROR</span>
                        )}
                        {tab === "tree" && (
                          <small>{duration(s.duration_ms)}</small>
                        )}
                      </button>
                    </div>
                    {tab === "timeline" && (
                      <button
                        className="timeline-track"
                        title={`${s.name}: ${duration(s.duration_ms)}`}
                        onClick={() => setActive(s.id)}
                      >
                        <i
                          className={"time-bar " + s.kind}
                          style={{
                            left:
                              Math.min(
                                99,
                                Math.max(
                                  0,
                                  ((Date.parse(s.started_at) - start) / wall) *
                                    100,
                                ),
                              ) + "%",
                            width:
                              Math.min(
                                100,
                                ((s.duration_ms || 0) / wall) * 100,
                              ) + "%",
                          }}
                        />
                      </button>
                    )}
                  </div>
                );
              })}
            </div>
            {!rows.length && <Empty title="没有匹配的 Span" />}
          </div>
          <div className="tree-legend">
            <span className="model">● 模型</span>
            <span className="tool">● 工具</span>
            <span className="phase">● 环境</span>
          </div>
        </section>
        <section className="span-detail">
          {span && <SpanDetail key={span.id} span={span} trace={trace} />}
        </section>
        <aside className="trace-summary">
          <div className="pane-label">
            <b>运行概览</b>
            <IconChartBar size={16} />
          </div>
          <div className="summary-body">
            <div className="summary-status">
              <span className="eyebrow">TOTAL DURATION</span>
              <strong>{duration(summary.wall_ms)}</strong>
              <span>{date(spans[0]?.started_at)}</span>
            </div>
            <div className="metric-grid">
              <Metric label="Model calls" value={summary.model_calls} />
              <Metric label="Tool calls" value={summary.tool_calls} />
              <Metric
                label="Input tokens"
                value={
                  summary.usage_known ? number(summary.input_tokens) : "未知"
                }
              />
              <Metric
                label="Output tokens"
                value={
                  summary.usage_known ? number(summary.output_tokens) : "未知"
                }
              />
            </div>
            <h3>耗时分布</h3>
            <div className="breakdown">
              {[
                ["Model", summary.model_ms, "model"],
                ["Tool", summary.tool_ms, "tool"],
                ["Initial queue", summary.initial_queue_ms, "queue"],
                ...Object.entries(summary.phase_ms || {}).map(([k, v]) => [
                  k,
                  v,
                  "phase",
                ]),
              ].map(([name, ms, kind]) => (
                <div key={name}>
                  <div>
                    <span>{name}</span>
                    <b>{duration(ms)}</b>
                  </div>
                  <div className="bar-bg">
                    <i
                      className={kind}
                      style={{
                        width:
                          Math.max(0, Math.min(100, ((ms || 0) / wall) * 100)) +
                          "%",
                      }}
                    />
                  </div>
                </div>
              ))}
            </div>
            <p className="hint">
              模型与工具统计为累计耗时，并行执行时可能超过总时长。
            </p>
            {longest && (
              <div className="insight">
                <span className="eyebrow">LONGEST SPAN</span>
                <button
                  onClick={() => {
                    setActive(longest.id);
                    setTab("tree");
                  }}
                >
                  {longest.name}
                  <IconArrowUpRight size={15} />
                </button>
                <strong>{duration(longest.duration_ms)}</strong>
                <p>点击查看此阶段的详情与输入输出。</p>
              </div>
            )}
            <h3>关联资源</h3>
            <button
              className="related"
              onClick={() => route("tasks", trace.task_id)}
            >
              Task <span>{short(trace.task_id)}</span>
              <IconArrowUpRight size={15} />
            </button>
            <button
              className="related"
              onClick={() => route("sessions", trace.session_id)}
            >
              Session <IconArrowUpRight size={15} />
            </button>
            <button
              className="related"
              onClick={() => route("logs", "", { task: trace.task_id })}
            >
              执行日志 <IconArrowUpRight size={15} />
            </button>
            <button
              className="related"
              onClick={() => route("files", "", { task: trace.task_id })}
            >
              产出文件 <IconArrowUpRight size={15} />
            </button>
          </div>
        </aside>
      </div>
    </>
  );
}
function SpanDetail({ span, trace }) {
  const Icon = icons[span.kind] || IconBox;
  const task = useResource(
    "executionGetTask",
    trace.task_id,
    {},
    span.kind === "task",
  );
  const tools = useResource(
    "executionListTools",
    trace.task_id,
    {},
    span.kind === "tool",
  );
  const inputs = useResource(
    "executionListInputs",
    trace.task_id,
    { limit: 100 },
    span.kind === "task",
  );
  return (
    <>
      <div className="span-heading">
        <h2>
          <Icon size={20} />
          {span.name}
          <Copy text={span.id} />
        </h2>
        <p>{date(span.started_at)}</p>
        <div className="span-tags">
          <span>
            Span kind <b>{span.kind}</b>
          </span>
          <span>
            <IconClock size={14} /> Latency <b>{duration(span.duration_ms)}</b>
          </span>
        </div>
        {span.first_delta_ms != null && (
          <div className="first-token">
            首字延迟 <b>{duration(span.first_delta_ms)}</b>
          </div>
        )}
      </div>
      <div className="span-body">
        {span.kind === "task" && (
          <>
            <Load query={inputs}>
              {(data) => (
                <Section title="Input">
                  <Code
                    green
                    value={data.data.map((r) => r.text).join("\n\n")}
                  />
                  {data.next_offset && (
                    <span className="hint">
                      前 100 条输入，更多内容请在 Task 详情分页查看。
                    </span>
                  )}
                </Section>
              )}
            </Load>
            <Load query={task}>
              {(data) => (
                <Section title="Output" copy={data.result || data.error}>
                  <Code
                    green={!data.error}
                    value={data.result || data.error || "任务尚未产生最终输出"}
                  />
                </Section>
              )}
            </Load>
          </>
        )}
        {span.kind === "tool" && (
          <Load query={tools}>
            {(data) => {
              const tool = data.data.find((t) => t.id === span.id);
              return tool ? (
                <>
                  <Section title="Input" copy={tool.arguments}>
                    <Code green value={tool.arguments} />
                  </Section>
                  <Section title="Output" copy={tool.result}>
                    <Code
                      green={!tool.is_error}
                      value={tool.result || "等待工具结果"}
                    />
                  </Section>
                </>
              ) : (
                <p className="hint">工具明细不可用。</p>
              );
            }}
          </Load>
        )}
        {span.kind === "model" && (
          <div className="model-summary">
            <IconSparkles size={24} />
            <h3>{span.attributes?.model || "Model call"}</h3>
            <p>第 {span.attributes?.attempt ?? "—"} 次模型调用</p>
            <div className="metric-grid">
              <Metric label="首字延迟" value={duration(span.first_delta_ms)} />
              <Metric label="总耗时" value={duration(span.duration_ms)} />
            </div>
            <p className="hint">
              Trace 保存调用元数据。任务输入、结果与工具参数在关联 Task 中查看。
            </p>
            <button
              className="button"
              onClick={() => route("tasks", trace.task_id)}
            >
              查看任务输入输出 <IconArrowUpRight size={15} />
            </button>
          </div>
        )}
        {span.kind === "child" && (
          <button
            className="button"
            onClick={() => route("traces", span.task_id)}
          >
            进入子任务 Trace <IconArrowUpRight size={15} />
          </button>
        )}
        <Section title="Attributes" copy={span.attributes || {}}>
          <Code value={span.attributes || {}} />
        </Section>
        <Section title="Metadata" open={false}>
          <Fields
            values={{
              "Span ID": span.id,
              "Parent ID": span.parent_id,
              "Task ID": span.task_id,
              State: <Badge state={span.state} />,
              Started: date(span.started_at),
              Finished: date(span.finished_at),
            }}
          />
        </Section>
      </div>
    </>
  );
}
