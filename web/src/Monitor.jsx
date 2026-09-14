import React, { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { IconArrowUpRight, IconChartLine } from "@tabler/icons-react";
import { useAPI, route, date, duration, number } from "./data.js";
import { Empty, Load, Metric, PageHeader } from "./ui.jsx";

const sum = (v, k) => v[k]?.sum || 0;
const percentile = (v, k, p = "p95") => v[k]?.[p] ?? null;
const percent = (v) => (v == null ? "—" : `${v.toFixed(1)}%`);
const success = (v) =>
  sum(v, "tasks.completed")
    ? (100 * sum(v, "tasks.succeeded")) / sum(v, "tasks.completed")
    : null;
const tokens = (v) => sum(v, "tokens.input") + sum(v, "tokens.output");
const localTime = (d) =>
  new Date(d - new Date(d).getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
const relativeRange = (hours) => {
  const now = Date.now();
  return {
    from: new Date(now - Number(hours) * 3600000).toISOString(),
    to: new Date(now).toISOString(),
  };
};
const colors = ["#635bba", "#218b78", "#cc8a2e"];
export default function Monitor({ overview, live, title, icon }) {
  const api = useAPI();
  const [hours, setHours] = useState("1"),
    [range, setRange] = useState(() => ({
      from: new Date(Date.now() - 3600000).toISOString(),
      to: new Date().toISOString(),
    })),
    [start, setStart] = useState(localTime(Date.now() - 3600000)),
    [end, setEnd] = useState(localTime(Date.now())),
    [error, setError] = useState("");
  useEffect(() => {
    if (!live || hours === "custom") return;
    const t = setInterval(() => {
      if (document.visibilityState === "visible")
        setRange(relativeRange(hours));
    }, 5000);
    return () => clearInterval(t);
  }, [live, hours]);
  const q = useQuery({
    queryKey: ["metrics", range],
    queryFn: ({ signal }) =>
      api.call("consoleMetrics", { query: range, signal }).then((r) => r.data),
    refetchInterval: live ? 5000 : false,
    refetchIntervalInBackground: false,
  });
  function change(value) {
    setHours(value);
    setError("");
    if (value !== "custom") setRange(relativeRange(value));
  }
  function apply(e) {
    e.preventDefault();
    const from = new Date(start),
      to = new Date(end);
    if (!Number.isFinite(+from) || !Number.isFinite(+to) || from >= to) {
      setError("请选择有效的时间范围");
      return;
    }
    setRange({ from: from.toISOString(), to: to.toISOString() });
    setError("");
  }
  return (
    <div className="monitor-page">
      <PageHeader title={title} icon={icon}>
        <select
          aria-label="指标时间范围"
          value={hours}
          onChange={(e) => change(e.target.value)}
        >
          {[
            ["1", "最近 1 小时"],
            ["6", "最近 6 小时"],
            ["24", "最近 24 小时"],
            ["168", "最近 7 天"],
            ["720", "最近 30 天"],
            ["custom", "自定义"],
          ].map(([v, t]) => (
            <option key={v} value={v}>
              {t}
            </option>
          ))}
        </select>
      </PageHeader>
      {hours === "custom" && (
        <form className="metric-range" onSubmit={apply}>
          <input
            type="datetime-local"
            aria-label="开始时间"
            value={start}
            onChange={(e) => setStart(e.target.value)}
            required
          />
          <span>—</span>
          <input
            type="datetime-local"
            aria-label="结束时间"
            value={end}
            onChange={(e) => setEnd(e.target.value)}
            required
          />
          <button>应用</button>
        </form>
      )}
      {error && <div className="error">{error}</div>}
      <Load query={q}>
        {(data) => (
          <Dashboard
            key={`${data.from}/${data.step_seconds}`}
            data={data}
            overview={overview}
          />
        )}
      </Load>
    </div>
  );
}
function Dashboard({ data, overview }) {
  const totals = data.totals,
    points = data.points;
  const latest = points.findLastIndex((p) => Object.keys(p.values).length > 0);
  const [selected, setSelected] = useState(Math.max(0, latest));
  const active = points[Math.min(selected, points.length - 1)],
    step = data.step_seconds;
  const from = active?.at || data.from,
    to = new Date(Date.parse(from) + step * 1000).toISOString();
  const interval = { after: from, before: to };
  const tasks = sum(totals, "tasks.completed");
  const specs = [
    {
      title: "任务吞吐",
      format: (v) => `${number(v)}/min`,
      series: [
        {
          label: "完成",
          value: (v) => (sum(v, "tasks.completed") * 60) / step,
        },
        {
          label: "失败",
          color: "#bc5650",
          value: (v) => (sum(v, "tasks.failed") * 60) / step,
        },
      ],
      kind: "rate",
    },
    {
      title: "任务耗时",
      format: duration,
      series: ["p50", "p95", "p99"].map((p) => ({
        label: p.toUpperCase(),
        value: (v) => percentile(v, "task.duration", p),
      })),
    },
    {
      title: "模型首 Token 延迟",
      format: duration,
      series: [
        {
          label: "P50",
          value: (v) => percentile(v, "model.first_token", "p50"),
        },
        { label: "P95", value: (v) => percentile(v, "model.first_token") },
      ],
    },
    {
      title: "Token 用量",
      format: number,
      series: [
        { label: "Input", value: (v) => sum(v, "tokens.input") },
        { label: "Output", value: (v) => sum(v, "tokens.output") },
        { label: "Cached input", value: (v) => sum(v, "tokens.cached") },
      ],
    },
    {
      title: "任务成功率",
      format: percent,
      series: [{ label: "成功率", value: success }],
      ceiling: 100,
    },
    {
      title: "首次队列等待",
      format: duration,
      series: [
        { label: "P50", value: (v) => percentile(v, "queue.wait", "p50") },
        { label: "P95", value: (v) => percentile(v, "queue.wait") },
      ],
    },
    {
      title: "沙箱启动耗时",
      format: duration,
      series: [
        { label: "P50", value: (v) => percentile(v, "sandbox.startup", "p50") },
        { label: "P95", value: (v) => percentile(v, "sandbox.startup") },
      ],
    },
    {
      title: "模型调用耗时",
      format: duration,
      series: [
        { label: "P50", value: (v) => percentile(v, "model.duration", "p50") },
        { label: "P95", value: (v) => percentile(v, "model.duration") },
      ],
    },
  ];
  return (
    <>
      <div className="monitor-cards">
        <Metric label="完成任务" value={number(tasks)} />
        <Metric label="成功率" value={percent(success(totals))} />
        <Metric
          label="任务 P95"
          value={duration(percentile(totals, "task.duration"))}
        />
        <Metric label="Tokens" value={number(tokens(totals))} />
      </div>
      <div className="metric-summary">
        <span>
          失败 <b>{number(sum(totals, "tasks.failed"))}</b>
        </span>
        <span>
          取消 <b>{number(sum(totals, "tasks.canceled"))}</b>
        </span>
        <span>
          部分完成 <b>{number(sum(totals, "tasks.partial"))}</b>
        </span>
        <span>
          模型调用 <b>{number(sum(totals, "model.calls"))}</b>
        </span>
        <span>
          模型失败 <b>{number(sum(totals, "model.failed"))}</b>
        </span>
        <span>
          沙箱失败 <b>{number(sum(totals, "sandbox.failed"))}</b>
        </span>
      </div>
      {data.pending_since &&
        Date.now() - Date.parse(data.pending_since) > 5000 && (
          <div className="notice">指标待聚合：{date(data.pending_since)}</div>
        )}
      {sum(totals, "model.usage_unknown") > 0 && (
        <div className="notice">
          {number(sum(totals, "model.usage_unknown"))} 次模型调用的 Token
          用量不完整。
        </div>
      )}
      {!Object.keys(totals).length && <Empty title="此时段暂无指标" />}
      <div className="metric-selection">
        <IconChartLine size={17} />
        <time>
          {date(from)} — {date(to)}
        </time>
        <span className="header-spacer" />
        <button
          onClick={() =>
            route("traces", "", { ...interval, time_field: "finished_at" })
          }
        >
          Traces
          <IconArrowUpRight size={14} />
        </button>
        <button
          onClick={() =>
            route("tasks", "", {
              ...interval,
              state: "failed",
              time_field: "finished_at",
            })
          }
        >
          失败任务
          <IconArrowUpRight size={14} />
        </button>
        <button onClick={() => route("logs", "", interval)}>
          Logs
          <IconArrowUpRight size={14} />
        </button>
      </div>
      <div className="charts-grid">
        {(overview ? specs.slice(0, 4) : specs).map((spec) => (
          <Chart
            key={spec.title}
            spec={spec}
            points={points}
            selected={selected}
            onSelect={setSelected}
          />
        ))}
      </div>
      <div className="metric-footnote">
        <span>
          {date(data.from)} — {date(data.to)} · {step / 60} min
        </span>
        <span title="延迟分位数使用可合并的对数直方图，桶宽 2%；小样本下分位数不代表稳定性能。">
          分位数 ≈ · 样本 {number(totals["task.duration"]?.count || 0)}
        </span>
        {overview && (
          <button onClick={() => route("metrics")}>
            全部指标
            <IconArrowUpRight size={14} />
          </button>
        )}
      </div>
    </>
  );
}
function Chart({ spec, points, selected, onSelect }) {
  const width = 600,
    height = 140,
    pad = 12;
  const datasets = useMemo(
    () => spec.series.map((s) => points.map((p) => s.value(p.values))),
    [spec, points],
  );
  const max =
    spec.ceiling || Math.max(1, ...datasets.flat().filter((v) => v != null));
  const x = (i) =>
      pad + (i * (width - pad * 2)) / Math.max(1, points.length - 1),
    y = (v) => height - pad - (v / max) * (height - pad * 2);
  const at = Math.min(selected, points.length - 1);
  function pick(e) {
    const rect = e.currentTarget.getBoundingClientRect();
    onSelect(
      Math.max(
        0,
        Math.min(
          points.length - 1,
          Math.round(
            ((((e.clientX - rect.left) / rect.width) * width - pad) /
              (width - pad * 2)) *
              (points.length - 1),
          ),
        ),
      ),
    );
  }
  function keyboard(e) {
    let n = selected;
    if (e.key === "ArrowRight") n++;
    else if (e.key === "ArrowLeft") n--;
    else if (e.key === "Home") n = 0;
    else if (e.key === "End") n = points.length - 1;
    else return;
    e.preventDefault();
    onSelect(Math.max(0, Math.min(points.length - 1, n)));
  }
  return (
    <section className="chart-card">
      <header>
        <h2>{spec.title}</h2>
        <span>{spec.format(max)}</span>
      </header>
      <div className="chart-legend">
        {spec.series.map((s, i) => (
          <span key={s.label}>
            <i style={{ background: s.color || colors[i] }} />
            {s.label}
            <b>{spec.format(datasets[i][at])}</b>
          </span>
        ))}
      </div>
      <div
        className="chart-plot"
        role="slider"
        aria-label={`${spec.title} 时间点`}
        aria-valuemin={0}
        aria-valuemax={Math.max(0, points.length - 1)}
        aria-valuenow={at}
        aria-valuetext={date(points[at]?.at)}
        tabIndex={0}
        onKeyDown={keyboard}
        onClick={pick}
      >
        <svg
          viewBox={`0 0 ${width} ${height}`}
          preserveAspectRatio="none"
          aria-hidden="true"
        >
          {[0, 0.5, 1].map((v) => (
            <line
              key={v}
              x1={pad}
              x2={width - pad}
              y1={y(max * v)}
              y2={y(max * v)}
              stroke="#ececf0"
              strokeDasharray={v ? "3 4" : undefined}
            />
          ))}
          {datasets.map((values, j) => {
            let path = "",
              previous = false;
            values.forEach((v, i) => {
              if (v == null) {
                previous = false;
                return;
              }
              path += `${previous ? "L" : "M"}${x(i)},${y(v)} `;
              previous = true;
            });
            return (
              <g key={j}>
                <path
                  d={path}
                  fill="none"
                  stroke={spec.series[j].color || colors[j]}
                  strokeWidth="2"
                  vectorEffect="non-scaling-stroke"
                />
                {values.map((v, i) =>
                  v != null &&
                  (i === at ||
                    (values[i - 1] == null && values[i + 1] == null)) ? (
                    <circle
                      key={i}
                      cx={x(i)}
                      cy={y(v)}
                      r="3"
                      fill={spec.series[j].color || colors[j]}
                    />
                  ) : null,
                )}
              </g>
            );
          })}
          <line
            x1={x(at)}
            x2={x(at)}
            y1={pad}
            y2={height - pad}
            stroke="#b9b5cd"
            strokeDasharray="3 3"
          />
        </svg>
      </div>
      <footer>
        <time>{date(points[0]?.at)}</time>
        <time>{date(points.at(-1)?.at)}</time>
      </footer>
    </section>
  );
}
