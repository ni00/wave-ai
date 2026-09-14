import React, { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  IconArrowUpRight,
  IconDownload,
  IconBox,
  IconFileText,
  IconChartHistogram,
} from "@tabler/icons-react";
import {
  useAPI,
  useResource,
  route,
  date,
  duration,
  number,
  bytes,
  comparable,
} from "./data.js";
import {
  Load,
  Section,
  Code,
  Fields,
  Badge,
  Tabs,
  Empty,
  Metric,
} from "./ui.jsx";
export default function Resource({ kind, id, record }) {
  if (kind === "tasks") return <Task id={id} />;
  if (kind === "sessions") return <Session id={id} />;
  if (kind === "environments") return <Environment id={id} />;
  if (kind === "files") return <File id={id} />;
  if (kind === "memory") return <Memory id={id} name={record.name} />;

  if (kind === "benchmarks") return <Bench id={id} />;
}
function Jump({ kind, id, children, extra }) {
  return (
    <button className="button" onClick={() => route(kind, id || "", extra)}>
      {children}
      <IconArrowUpRight size={15} />
    </button>
  );
}
function Paged({ op, id, render, query = {} }) {
  const [offset, setOffset] = useState(0);
  const q = useResource(op, id, { ...query, offset, limit: 20 });
  return (
    <Load query={q}>
      {(d) => (
        <>
          {d.data.length ? render(d.data) : <Empty />}
          <div className="page-controls">
            <button
              disabled={!offset}
              onClick={() => setOffset(Math.max(0, offset - 20))}
            >
              上一页
            </button>
            <span>第 {offset / 20 + 1} 页</span>
            <button
              disabled={!d.next_offset && !d.next_after}
              onClick={() => setOffset(d.next_offset)}
            >
              下一页
            </button>
          </div>
        </>
      )}
    </Load>
  );
}
function Task({ id }) {
  const [tab, setTab] = useState("overview"),
    q = useResource("executionGetTask", id);
  return (
    <>
      <Tabs
        items={[
          ["overview", "Overview"],
          ["inputs", "Inputs"],
          ["tools", "Tools"],
          ["generations", "Model calls"],
          ["events", "Events"],
        ]}
        value={tab}
        onChange={setTab}
      />
      <div className="resource-scroll">
        <Load query={q}>
          {(t) => (
            <>
              <div className="resource-heading">
                <h2>{t.agent?.name || t.agent_id}</h2>
                <Badge state={t.state} />
                <p>{date(t.created_at)}</p>
                <div className="actions">
                  <Jump kind="traces" id={id}>
                    查看 Trace
                  </Jump>
                  <Jump kind="logs" extra={{ task: id }}>
                    执行日志
                  </Jump>
                  <Jump kind="sessions" id={t.session_id}>
                    所属 Session
                  </Jump>
                  <Jump kind="files" extra={{ task: id }}>
                    产出文件
                  </Jump>
                </div>
              </div>
              {tab === "overview" && (
                <>
                  <div className="metric-grid wide">
                    <Metric
                      label="总耗时"
                      value={
                        t.finished_at
                          ? duration(
                              Date.parse(t.finished_at) -
                                Date.parse(t.created_at),
                            )
                          : "执行中"
                      }
                    />
                    <Metric
                      label="Tokens"
                      value={t.usage_known ? number(t.used_tokens) : "未知"}
                    />
                    <Metric label="Tool calls" value={t.tool_calls} />
                    <Metric
                      label="Agent version"
                      value={"v" + t.agent_version}
                    />
                  </div>
                  <Section
                    title={t.error ? "Error" : "Output"}
                    copy={t.error || t.result}
                  >
                    <Code
                      green={!t.error}
                      value={t.error || t.result || "尚无最终输出"}
                    />
                  </Section>
                  <Section title="Agent configuration" open={false}>
                    <Code value={t.agent} />
                  </Section>
                  <Section title="Budget & metadata" open={false}>
                    <Code
                      value={{
                        budget: t.budget,
                        root_id: t.root_id,
                        parent_id: t.parent_id,
                        started_at: t.started_at,
                        finished_at: t.finished_at,
                      }}
                    />
                  </Section>
                </>
              )}
              {tab === "events" && <Events session={t.session_id} task={id} />}
              {tab === "inputs" && (
                <Paged
                  op="executionListInputs"
                  id={id}
                  render={(rows) =>
                    rows.map((r) => (
                      <Section
                        key={r.id}
                        title={date(r.created_at)}
                        copy={r.text}
                      >
                        <Code green value={r.text} />
                      </Section>
                    ))
                  }
                />
              )}
              {tab === "tools" && <ToolCalls id={id} />}{" "}
              {tab === "generations" && (
                <Paged
                  op="executionGenerations"
                  id={id}
                  render={(rows) =>
                    rows.map((r) => (
                      <Section key={r.id} title={"Model call · " + r.id}>
                        <Code value={r} />
                      </Section>
                    ))
                  }
                />
              )}
            </>
          )}
        </Load>
      </div>
    </>
  );
}
function ToolCalls({ id }) {
  const q = useResource("executionListTools", id);
  return (
    <Load query={q}>
      {(d) =>
        d.data.length ? (
          d.data.map((t) => (
            <Section
              key={t.id}
              title={`${t.tool?.name || t.id} · ${t.status}`}
              open={false}
            >
              <h4>Input</h4>
              <Code green value={t.arguments} />
              <h4>Output</h4>
              <Code green={!t.is_error} value={t.result} />
            </Section>
          ))
        ) : (
          <Empty title="尚无工具调用" />
        )
      }
    </Load>
  );
}
function Session({ id }) {
  const [tab, setTab] = useState("tasks"),
    q = useResource("executionGetSession", id);
  return (
    <>
      <Tabs
        items={[
          ["tasks", "Tasks"],
          ["messages", "Messages"],
          ["resources", "Resources"],
          ["events", "Events"],
        ]}
        value={tab}
        onChange={setTab}
      />
      <div className="resource-scroll">
        <Load query={q}>
          {(s) => (
            <>
              <div className="resource-heading">
                <h2>{s.title || "Untitled session"}</h2>
                <Badge state={s.archived ? "archived" : "active"} />
                <p>{date(s.created_at)}</p>
                <div className="actions">
                  <Jump kind="logs" extra={{ session: id }}>
                    执行日志
                  </Jump>
                  <Jump kind="traces" extra={{ session: id }}>
                    全部 Traces
                  </Jump>
                </div>
              </div>
              {tab === "tasks" && (
                <Paged
                  op="executionListTasks"
                  id={id}
                  render={(rows) => (
                    <div className="data-table">
                      {rows.map((t) => (
                        <button key={t.id} onClick={() => route("tasks", t.id)}>
                          <span>
                            {t.agent?.name || t.id}
                            <small>{t.id}</small>
                          </span>
                          <Badge state={t.state} />
                          <IconArrowUpRight size={16} />
                        </button>
                      ))}
                    </div>
                  )}
                />
              )}{" "}
              {tab === "messages" && <Messages id={id} />}
              {tab === "events" && <Events session={id} />}{" "}
              {tab === "resources" && (
                <>
                  <Section title="Environment">
                    {s.environment_id ? (
                      <Jump kind="environments" id={s.environment_id}>
                        {s.environment_id}
                      </Jump>
                    ) : (
                      <span>默认环境</span>
                    )}
                  </Section>
                  <Section title="Files">
                    {s.file_ids?.length
                      ? s.file_ids.map((id) => (
                          <Jump key={id} kind="files" id={id}>
                            {id}
                          </Jump>
                        ))
                      : "没有关联文件"}
                  </Section>
                  <Section title="Memory stores">
                    {s.memory_store_ids?.length
                      ? s.memory_store_ids.map((id) => (
                          <Jump key={id} kind="memory" id={id}>
                            {id}
                          </Jump>
                        ))
                      : "没有关联记忆库"}
                  </Section>
                </>
              )}
            </>
          )}
        </Load>
      </div>
    </>
  );
}
function Messages({ id }) {
  const [cursor, setCursor] = useState(0),
    [history, setHistory] = useState([]);
  const q = useResource("executionMessages", id, { after: cursor, limit: 20 });
  return (
    <Load query={q}>
      {(d) => (
        <>
          {d.data.length ? (
            d.data.map((m) => (
              <Section
                key={m.sequence}
                title={`${m.role} · ${date(m.created_at)}`}
              >
                <Code
                  green
                  value={
                    m.text ||
                    (m.tool_calls?.length ? m.tool_calls : "（空消息）")
                  }
                />
              </Section>
            ))
          ) : (
            <Empty title="暂无消息" />
          )}
          <div className="page-controls">
            <button
              disabled={!history.length}
              onClick={() => {
                setCursor(history.at(-1));
                setHistory(history.slice(0, -1));
              }}
            >
              上一页
            </button>
            <button
              disabled={!d.next_after}
              onClick={() => {
                setHistory([...history, cursor]);
                setCursor(d.next_after);
              }}
            >
              下一页
            </button>
          </div>
        </>
      )}
    </Load>
  );
}
function Environment({ id }) {
  const q = useResource("environmentsGet", id);
  return (
    <div className="resource-scroll">
      <Load query={q}>
        {(e) => (
          <>
            <div className="resource-heading">
              <div className="resource-symbol">
                <IconBox size={26} />
              </div>
              <h2>{e.name}</h2>
              <Badge state={e.archived ? "archived" : "active"} />
            </div>
            <div className="metric-grid">
              <Metric
                label="Sandbox backend"
                value={e.sandbox_backend || "服务端默认"}
              />
              <Metric
                label="Resource profile"
                value={e.sandbox_profile || "default"}
              />
            </div>
            <Section title="Packages">
              {Object.keys(e.packages || {}).length ? (
                Object.entries(e.packages).map(([manager, packages]) => (
                  <div className="package-row" key={manager}>
                    <b>{manager}</b>
                    {packages.map((p) => (
                      <code key={p}>{p}</code>
                    ))}
                  </div>
                ))
              ) : (
                <span className="subtle">无额外依赖</span>
              )}
            </Section>
            <Section title="Metadata">
              <Fields
                values={{
                  ID: e.id,
                  Created: date(e.created_at),
                  Backend: e.sandbox_backend || "继承服务端默认",
                }}
              />
            </Section>
          </>
        )}
      </Load>
    </div>
  );
}
function useFileContent(id, limit, enabled = true) {
  const api = useAPI();
  return useQuery({
    queryKey: ["file-content", id, limit],
    enabled,
    queryFn: async ({ signal }) => {
      let total = 0,
        chunks = [];
      await api.download(
        "filesContent",
        id,
        (c) => {
          total += c.length;
          if (total > limit) throw new Error("文件超过预览上限");
          chunks.push(c);
        },
        { headers: { Range: `bytes=0-${limit - 1}` }, signal },
      );
      const out = new Uint8Array(total);
      let offset = 0;
      for (const c of chunks) {
        out.set(c, offset);
        offset += c.length;
      }
      return out;
    },
  });
}
function File({ id }) {
  const q = useResource("filesGet", id);
  return (
    <div className="resource-scroll">
      <Load query={q}>{(f) => <FileView file={f} />}</Load>
    </div>
  );
}
function FileView({ file: f }) {
  const api = useAPI(),
    [downloading, setDownloading] = useState(false),
    [error, setError] = useState("");
  const previewable =
    f.size > 0 &&
    (f.mime_type?.startsWith("text/") ||
      /\.(json|md|txt|csv|log|py|js|ts|yaml|yml|toml|sh)$/i.test(f.name));
  const content = useFileContent(f.id, 131072, previewable);
  async function download() {
    setDownloading(true);
    setError("");
    try {
      if (f.size > 64 * 1048576)
        throw new Error(
          "浏览器内存下载上限 64 MiB；请使用 wavectl files download 下载此文件。",
        );
      const chunks = [];
      await api.download("filesContent", f.id, (c) => chunks.push(c));
      const url = URL.createObjectURL(
        new Blob(chunks, { type: "application/octet-stream" }),
      );
      const a = document.createElement("a");
      a.href = url;
      a.download = f.name;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 60000);
    } catch (e) {
      setError(e.message);
    } finally {
      setDownloading(false);
    }
  }
  return (
    <>
      <div className="resource-heading">
        <div className="resource-symbol">
          <IconFileText size={26} />
        </div>
        <h2>{f.name}</h2>
        <p>
          {bytes(f.size)} · {f.mime_type}
        </p>
        <div className="actions">
          <button className="button" disabled={downloading} onClick={download}>
            <IconDownload size={16} />
            {downloading ? "下载中…" : "下载原文件"}
          </button>
          {f.task_id && (
            <Jump kind="traces" id={f.task_id}>
              关联 Trace
            </Jump>
          )}
          {f.session_id && (
            <Jump kind="sessions" id={f.session_id}>
              关联 Session
            </Jump>
          )}
        </div>
      </div>
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      <Section title="Preview">
        {previewable ? (
          <Load query={content}>
            {(value) => (
              <>
                <Code green value={new TextDecoder().decode(value)} />
                {f.size > 131072 && (
                  <p className="hint">仅预览前 128 KiB，下载查看完整文件。</p>
                )}
              </>
            )}
          </Load>
        ) : (
          <Empty title={f.size === 0 ? "空文件" : "此文件类型不支持文本预览"}>
            可以下载原文件到本地查看。
          </Empty>
        )}
      </Section>
      <Section title="Metadata" open={false}>
        <Fields
          values={{
            ID: f.id,
            Path: f.path || f.name,
            Created: date(f.created_at),
            Size: bytes(f.size),
          }}
        />
      </Section>
    </>
  );
}
function Memory({ id, name }) {
  const [tab, setTab] = useState("entries");
  const op = {
    entries: "memoryListEntries",
    revisions: "memoryListRevisions",
    conflicts: "memoryListConflicts",
  }[tab];
  return (
    <>
      <Tabs
        items={[
          ["entries", "Entries"],
          ["revisions", "History"],
          ["conflicts", "Conflicts"],
        ]}
        value={tab}
        onChange={setTab}
      />
      <div className="resource-scroll">
        <div className="resource-heading">
          <h2>{name || "Memory store"}</h2>
        </div>
        <Paged
          key={tab}
          id={id}
          op={op}
          render={(rows) =>
            rows.map((r, i) => (
              <Section
                key={r.id || r.path + i}
                title={`${r.path} · ${r.version ? "v" + r.version : r.actual_version ? "冲突 v" + r.actual_version : ""}${r.deleted ? " · deleted" : ""}`}
                open={false}
              >
                <Code green value={r.content} />
                <p className="hint">{date(r.updated_at || r.created_at)}</p>
                {r.session_id && (
                  <Jump kind="sessions" id={r.session_id}>
                    来源会话
                  </Jump>
                )}
              </Section>
            ))
          }
        />
      </div>
    </>
  );
}
function Bench({ id }) {
  const content = useFileContent(id, 16 * 1048576);
  return (
    <div className="resource-scroll">
      <Load query={content}>
        {(bytes) => {
          let report;
          try {
            report = JSON.parse(new TextDecoder().decode(bytes));
          } catch {
            return (
              <Empty title="报告解析失败">
                请重新导入有效的 Wave bench v2 JSON。
              </Empty>
            );
          }
          return <BenchReport report={report} id={id} />;
        }}
      </Load>
    </div>
  );
}
function BenchReport({ report: r, id }) {
  const api = useAPI(),
    [baseline, setBaseline] = useState("");
  const reports = useQuery({
    queryKey: ["bench-choices"],
    queryFn: ({ signal }) =>
      api
        .call("consoleBrowse", {
          path: { kind: "benchmarks" },
          query: { limit: 200 },
          signal,
        })
        .then((r) => r.data),
  });
  const content = useFileContent(baseline, 16 * 1048576, !!baseline);
  let previous;
  try {
    if (content.data)
      previous = JSON.parse(new TextDecoder().decode(content.data));
  } catch {}
  const valid = comparable(r, previous),
    phases = r.phases || [],
    total = phases.reduce((s, p) => s + p.succeeded, 0),
    failed = phases.reduce((s, p) => s + p.failed, 0);
  return (
    <>
      <div className="resource-heading">
        <div className="resource-symbol">
          <IconChartHistogram size={26} />
        </div>
        <h2>{r.case_name || r.kind + " benchmark"}</h2>
        <p>
          {date(r.started_at)} · {r.run_id}
        </p>
        <span className="tag">{r.kind}</span>{" "}
        <span className="tag">{r.model || r.go_version}</span>{" "}
        <span className="tag">
          {r.revision?.slice(0, 8) || "revision unknown"}
        </span>
      </div>
      <div className="metric-grid wide">
        <Metric label="Succeeded" value={total} />
        <Metric label="Failed" value={failed} />
        <Metric label="并发档位" value={phases.length} />
        <Metric label="Logical CPUs" value={r.logical_cpus} />
      </div>
      <div className="bench-comparison">
        <label htmlFor="baseline">对比基线</label>
        <select
          id="baseline"
          value={baseline}
          onChange={(e) => setBaseline(e.target.value)}
        >
          <option value="">选择已导入的报告…</option>
          {reports.data?.data
            .filter((p) => p.id !== id)
            .map((p) => (
              <option value={p.id} key={p.id}>
                {p.name}
              </option>
            ))}
        </select>
        <p className="hint">候选为最近 200 份报告。</p>
        {reports.error && <p className="error">{reports.error.message}</p>}
        {baseline && (
          <p className={valid ? "hint" : "notice"}>
            {content.error
              ? content.error.message
              : content.isPending
                ? "正在加载基线…"
                : valid
                  ? "工作负载与配置一致，可以对比。"
                  : "工作负载、模型或配置不一致，不计算性能变化。"}
          </p>
        )}
      </div>
      <p className="hint">
        P95 / P99 来自本次样本。样本少于 20 时仅作探索性参考；存在失败或不完整
        Trace 时，不能据此判断优化通过。
      </p>
      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              <th>Workers</th>
              <th>Success / Total</th>
              <th>P50</th>
              <th>P95</th>
              <th>P99</th>
              <th>Tasks/s</th>
              {valid && <th>P95 Δ</th>}
            </tr>
          </thead>
          <tbody>
            {phases.map((p, i) => {
              const prior = previous?.phases?.find(
                  (b) => b.workers === p.workers,
                ),
                before = prior?.end_to_end?.p95_ms,
                delta = before
                  ? (p.end_to_end?.p95_ms / before - 1) * 100
                  : null;
              return (
                <tr key={i}>
                  <td>
                    <b>{p.workers || "服务端"}</b>
                  </td>
                  <td>
                    {p.succeeded} / {p.attempted}
                  </td>
                  <td>{duration(p.end_to_end?.p50_ms)}</td>
                  <td>{duration(p.end_to_end?.p95_ms)}</td>
                  <td>{duration(p.end_to_end?.p99_ms)}</td>
                  <td>{number(p.successful_tasks_per_second)}</td>
                  {valid && (
                    <td className={delta > 0 ? "regression" : "improvement"}>
                      {delta == null
                        ? "—"
                        : `${delta > 0 ? "+" : ""}${delta.toFixed(1)}%`}
                    </td>
                  )}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {phases.map((p, i) => (
        <Section
          key={i}
          title={`${p.workers ? p.workers + " workers" : "服务端 workers"} · Trace & task results`}
          open={false}
        >
          <Fields
            values={{
              "Trace failures": p.trace_failures ?? "—",
              "Incomplete traces": p.incomplete_traces ?? "—",
              "Model calls": p.model_calls ?? "—",
              "Error rate":
                p.error_rate == null
                  ? "—"
                  : (p.error_rate * 100).toFixed(1) + "%",
            }}
          />
          {p.phase_error && <Code value={p.phase_error} />}
          <BenchTasks tasks={p.tasks || []} />
        </Section>
      ))}
      <Section title="Configuration" open={false}>
        <Code
          value={{
            options: r.options,
            workload_sha256: r.workload_sha256,
            revision: r.revision,
            go_version: r.go_version,
            os: r.os,
            arch: r.arch,
          }}
        />
      </Section>
      <Jump kind="files" id={id}>
        报告原文件
      </Jump>
    </>
  );
}
function BenchTasks({ tasks }) {
  const [page, setPage] = useState(0);
  return (
    <>
      <div className="data-table">
        {tasks.slice(page * 50, (page + 1) * 50).map((t, i) => (
          <button
            key={t.task_id || i}
            onClick={() => route("traces", t.task_id)}
            disabled={!t.task_id}
          >
            <span>{t.task_id || "无任务 ID"}</span>
            <Badge state={t.state} />
            <span>{duration(t.observed_ms)}</span>
            <IconArrowUpRight size={15} />
          </button>
        ))}
      </div>
      {tasks.length > 50 && (
        <div className="page-controls">
          <button disabled={!page} onClick={() => setPage(page - 1)}>
            上一页
          </button>
          <span>{page + 1}</span>
          <button
            disabled={(page + 1) * 50 >= tasks.length}
            onClick={() => setPage(page + 1)}
          >
            下一页
          </button>
        </div>
      )}
    </>
  );
}

function Events({ session, task }) {
  const api = useAPI();
  const [cursor, setCursor] = useState(""),
    [pages, setPages] = useState([]),
    [selected, setSelected] = useState("");
  const q = useQuery({
    queryKey: ["events", session, task, cursor],
    queryFn: ({ signal }) =>
      api
        .call("consoleBrowse", {
          path: { kind: "events" },
          query: {
            session_id: session,
            task_id: task,
            cursor: cursor || undefined,
            limit: 50,
          },
          signal,
        })
        .then((r) => r.data),
  });
  return (
    <>
      <Load query={q}>
        {(d) =>
          d.data.length ? (
            <div className="event-list">
              {d.data.map((r) => (
                <div key={r.id}>
                  <button
                    className="event-row"
                    aria-expanded={selected === r.id}
                    onClick={() => setSelected(selected === r.id ? "" : r.id)}
                  >
                    <time>{date(r.created_at)}</time>
                    <strong>{r.name}</strong>
                    <span>#{r.meta.sequence}</span>
                  </button>
                  {selected === r.id && (
                    <EventDetail session={session} sequence={r.meta.sequence} />
                  )}
                </div>
              ))}
            </div>
          ) : (
            <Empty title="暂无事件" />
          )
        }
      </Load>
      <div className="page-controls">
        <button
          disabled={!pages.length || q.isFetching}
          onClick={() => {
            setCursor(pages.at(-1));
            setPages(pages.slice(0, -1));
            setSelected("");
          }}
        >
          上一页
        </button>
        <span>{pages.length + 1}</span>
        <button
          disabled={!q.data?.next_cursor || q.isFetching}
          onClick={() => {
            setPages([...pages, cursor]);
            setCursor(q.data.next_cursor);
            setSelected("");
          }}
        >
          下一页
        </button>
      </div>
    </>
  );
}
function EventDetail({ session, sequence }) {
  const api = useAPI();
  const q = useQuery({
    queryKey: ["event", session, sequence],
    queryFn: ({ signal }) =>
      api
        .call("consoleEvent", { path: { session, sequence }, signal })
        .then((r) => r.data),
  });
  return (
    <Load query={q}>
      {(e) => (
        <Section title="Data" copy={e.data}>
          <Code value={e.data} />
        </Section>
      )}
    </Load>
  );
}
