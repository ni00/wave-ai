import React, { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { IconSearch, IconX, IconArrowUpRight } from "@tabler/icons-react";
import {
  useAPI,
  useResource,
  route,
  routeFilters,
  date,
  short,
} from "./data.js";
import { Badge, Code, Copy, Empty, Fields, Load, Section } from "./ui.jsx";

export default function Logs({ location, live }) {
  const api = useAPI(),
    input = useRef();
  const [search, setSearch] = useState(""),
    [term, setTerm] = useState(""),
    [level, setLevel] = useState(location.state),
    [module, setModule] = useState(""),
    [hours, setHours] = useState(location.after ? "" : "24"),
    [cursor, setCursor] = useState(""),
    [pages, setPages] = useState([]);
  useEffect(() => {
    const t = setTimeout(() => setTerm(search), 250);
    return () => clearTimeout(t);
  }, [search]);
  useEffect(() => {
    setLevel(location.state);
    if (location.after) setHours("");
  }, [location.state, location.after]);
  useEffect(() => {
    setCursor("");
    setPages([]);
  }, [
    term,
    level,
    module,
    hours,
    location.task,
    location.session,
    location.trace,
    location.span,
    location.after,
    location.before,
  ]);
  useEffect(() => {
    const key = (e) => {
      if (
        ["INPUT", "SELECT", "TEXTAREA"].includes(
          document.activeElement?.tagName,
        ) ||
        e.metaKey ||
        e.ctrlKey ||
        e.altKey
      )
        return;
      if (e.key === "/") {
        e.preventDefault();
        input.current?.focus();
      }
      if (e.key === "Escape") route("logs", "", routeFilters(location));
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [location]);
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!live) return;
    const t = setInterval(() => {
      if (document.visibilityState === "visible") setNow(Date.now());
    }, 5000);
    return () => clearInterval(t);
  }, [live]);
  const filters = {
    q: term || undefined,
    state: level || undefined,
    module: module || undefined,
    task_id: location.task || undefined,
    session_id: location.session || undefined,
    trace_id: location.trace || undefined,
    span_id: location.span || undefined,
    after:
      location.after ||
      (hours
        ? new Date(now - Number(hours) * 3600000).toISOString()
        : undefined),
    before: location.before || undefined,
  };
  const q = useQuery({
    queryKey: ["logs", filters, cursor],
    queryFn: ({ signal }) =>
      api
        .call("consoleBrowse", {
          path: { kind: "logs" },
          query: { ...filters, cursor: cursor || undefined, limit: 100 },
          signal,
        })
        .then((r) => r.data),
    refetchInterval: live ? 5000 : false,
    refetchIntervalInBackground: false,
  });
  const clear = (key) => {
    const extra = routeFilters(location);
    delete extra[key];
    route("logs", "", extra);
  };
  return (
    <div className="logs-workspace">
      <div className="logs-main">
        <div className="monitor-title">
          <h1>Logs</h1>
          <span className="count">
            {q.data?.data.length || 0}
            {q.data?.next_cursor ? "+" : ""}
          </span>
        </div>
        <div className="log-filters">
          <label className="search">
            <IconSearch size={17} />
            <input
              ref={input}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="搜索消息、错误或 ID…"
              aria-label="搜索日志"
            />
            <kbd>/</kbd>
          </label>
          <select
            aria-label="日志级别"
            value={level}
            onChange={(e) => setLevel(e.target.value)}
          >
            <option value="">全部级别</option>
            {["debug", "info", "warn", "error"].map((v) => (
              <option key={v}>{v}</option>
            ))}
          </select>
          <select
            aria-label="日志模块"
            value={module}
            onChange={(e) => setModule(e.target.value)}
          >
            <option value="">全部模块</option>
            {[
              "http",
              "task",
              "model",
              "tool",
              "span",
              "worker",
              "execution",
              "trace",
            ].map((v) => (
              <option key={v}>{v}</option>
            ))}
          </select>
          <select
            aria-label="日志时间范围"
            value={hours}
            onChange={(e) => {
              setHours(e.target.value);
              const extra = routeFilters(location);
              delete extra.after;
              delete extra.before;
              route("logs", "", extra);
            }}
          >
            <option value="">{location.after ? "指定时段" : "全部时间"}</option>
            <option value="1">最近 1 小时</option>
            <option value="24">最近 24 小时</option>
            <option value="168">最近 7 天</option>
          </select>
        </div>
        <div className="log-chips">
          {["task", "session", "trace", "span"]
            .filter((k) => location[k])
            .map((k) => (
              <button key={k} onClick={() => clear(k)} title={location[k]}>
                {k}: {short(location[k])}
                <IconX size={12} />
              </button>
            ))}
          {location.after && (
            <button
              onClick={() => {
                const e = routeFilters(location);
                delete e.after;
                delete e.before;
                route("logs", "", e);
              }}
            >
              {date(location.after)} — {date(location.before)}
              <IconX size={12} />
            </button>
          )}
        </div>
        <div className="log-table-scroll">
          <Load query={q}>
            {(d) =>
              d.data.length ? (
                <table className="log-table">
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Level</th>
                      <th>Module</th>
                      <th>Message</th>
                      <th>Task</th>
                    </tr>
                  </thead>
                  <tbody>
                    {d.data.map((r) => (
                      <tr
                        key={r.id}
                        className={location.id === r.id ? "selected" : ""}
                      >
                        <td>
                          <button
                            className="log-open"
                            onClick={() =>
                              route("logs", r.id, routeFilters(location))
                            }
                          >
                            {date(r.created_at)}
                          </button>
                        </td>
                        <td>
                          <Badge state={r.state} />
                        </td>
                        <td>{r.meta.module}</td>
                        <td>
                          <button
                            className="log-message"
                            onClick={() =>
                              route("logs", r.id, routeFilters(location))
                            }
                          >
                            {r.name}
                          </button>
                        </td>
                        <td>
                          {r.task_id ? (
                            <button
                              className="inline-link mono"
                              onClick={() =>
                                route("traces", r.task_id, {
                                  span: r.meta.span_id || "",
                                })
                              }
                              title={r.task_id}
                            >
                              {short(r.task_id)}
                              <IconArrowUpRight size={13} />
                            </button>
                          ) : (
                            "—"
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : (
                <Empty title="暂无日志" />
              )
            }
          </Load>
        </div>
        <div className="page-controls">
          <span>每页 100 条</span>
          <button
            disabled={!pages.length || q.isFetching}
            onClick={() => {
              setCursor(pages.at(-1));
              setPages(pages.slice(0, -1));
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
            }}
          >
            下一页
          </button>
        </div>
      </div>
      {location.id && (
        <aside className="log-detail">
          <header>
            <strong>Log</strong>
            <button
              aria-label="关闭日志详情"
              onClick={() => route("logs", "", routeFilters(location))}
            >
              <IconX size={17} />
            </button>
          </header>
          <LogDetail id={location.id} />
        </aside>
      )}
    </div>
  );
}

function LogDetail({ id }) {
  const q = useResource("consoleLog", id);
  return (
    <div className="resource-scroll">
      <Load query={q}>
        {(l) => (
          <>
            <div className="resource-heading">
              <Badge state={l.level} />
              <h2>{l.message}</h2>
              <p>{date(l.created_at)}</p>
            </div>
            <Fields
              values={{
                Module: l.module,
                "Request ID": l.request_id || "—",
                "Trace ID": l.trace_id || "—",
                "Task ID": l.task_id || "—",
                "Span ID": l.span_id || "—",
              }}
            />
            <div className="actions">
              {l.task_id && (
                <button
                  className="button"
                  onClick={() =>
                    route("traces", l.task_id, { span: l.span_id || "" })
                  }
                >
                  Trace
                  <IconArrowUpRight size={15} />
                </button>
              )}
              {l.task_id && (
                <button
                  className="button"
                  onClick={() => route("tasks", l.task_id)}
                >
                  Task
                  <IconArrowUpRight size={15} />
                </button>
              )}
              {l.session_id && (
                <button
                  className="button"
                  onClick={() => route("sessions", l.session_id)}
                >
                  Session
                  <IconArrowUpRight size={15} />
                </button>
              )}
              <Copy text={JSON.stringify(l, null, 2)} />
            </div>
            {l.error && (
              <Section title="Error" copy={l.error}>
                <Code value={l.error} />
              </Section>
            )}
            <Section title="Attributes" copy={l.attributes}>
              <Code value={l.attributes} />
            </Section>
          </>
        )}
      </Load>
    </div>
  );
}
