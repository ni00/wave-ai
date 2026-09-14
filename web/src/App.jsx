import React, {
  useEffect,
  useMemo,
  useRef,
  useState,
  lazy,
  Suspense,
} from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  IconWaveSine,
  IconActivity,
  IconLayoutDashboard,
  IconListTree,
  IconTerminal2,
  IconBox,
  IconFiles,
  IconBrain,
  IconMessages,
  IconChecklist,
  IconChartHistogram,
  IconSearch,
  IconChevronRight,
  IconChevronLeft,
  IconRefresh,
  IconLogout,
  IconBook2,
  IconLayoutSidebar,
  IconX,
  IconArrowUp,
  IconArrowDown,
  IconUpload,
  IconArrowRight,
  IconKey,
  IconExternalLink,
} from "@tabler/icons-react";
import { Client } from "../../sdks/typescript/src/client.ts";
import {
  APIContext,
  routeFilters,
  readRoute,
  route,
  short,
  date,
  duration,
  number,
} from "./data.js";
import { IconButton, Badge, Copy, Load, Empty, DetailBoundary } from "./ui.jsx";
const Trace = lazy(() => import("./Trace.jsx"));
const Resource = lazy(() => import("./Resource.jsx"));
const Monitor = lazy(() => import("./Monitor.jsx"));
const Logs = lazy(() => import("./Logs.jsx"));
const navigation = [
  ["overview", "Overview", IconLayoutDashboard, "Monitoring"],
  ["metrics", "Metrics", IconActivity, "Monitoring"],
  ["traces", "Traces", IconListTree, "Monitoring"],
  ["logs", "Logs", IconTerminal2, "Monitoring"],
  ["environments", "Environments", IconBox, "运行环境"],
  ["files", "Files", IconFiles, "文件与产物"],
  ["memory", "Memory", IconBrain, "持久记忆"],
  ["sessions", "Sessions", IconMessages, "会话"],
  ["tasks", "Tasks", IconChecklist, "任务"],
  ["benchmarks", "Benchmarks", IconChartHistogram, "性能压测"],
];
export default function App() {
  const [apiKey, setAPIKey] = useState("");
  const queryClient = useQueryClient();
  const api = useMemo(
    () => new Client({ baseURL: location.origin, apiKey, maxRetries: 1 }),
    [apiKey],
  );
  if (!apiKey)
    return (
      <Login
        connect={(key) => {
          queryClient.clear();
          setAPIKey(key);
        }}
      />
    );
  return (
    <APIContext.Provider value={api}>
      <Console
        api={api}
        logout={() => {
          queryClient.cancelQueries();
          queryClient.clear();
          setAPIKey("");
        }}
      />
    </APIContext.Provider>
  );
}
function Login({ connect }) {
  const [key, setKey] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  return (
    <main className="login">
      <div className="login-card">
        <div className="brand">
          <span className="brand-icon">
            <IconWaveSine />
          </span>
          Wave<span className="brand-light">AI</span>
        </div>
        <h1>登录</h1>
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setError("");
            try {
              const client = new Client({
                baseURL: location.origin,
                apiKey: key,
              });
              await client.call("consoleBrowse", {
                path: { kind: "sessions" },
                query: { limit: 1 },
              });
              connect(key.trim());
            } catch (e) {
              setError(e.message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <label htmlFor="key">Wave API Key</label>
          <div className="key-input">
            <IconKey size={18} />
            <input
              autoFocus
              id="key"
              type="password"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="输入 API Key"
              autoComplete="off"
              required
            />
          </div>
          {error && (
            <div className="error" role="alert">
              {error}
            </div>
          )}
          <button className="primary" disabled={busy || !key.trim()}>
            {busy ? "正在连接…" : "连接"}
            <IconArrowRight size={17} />
          </button>
        </form>
        <small>刷新后需重新登录。</small>
      </div>
    </main>
  );
}
function Console({ api, logout }) {
  const [location, setLocation] = useState(readRoute),
    [search, setSearch] = useState(""),
    [debounced, setDebounced] = useState(""),
    [state, setState] = useState(location.state || ""),
    [period, setPeriod] = useState(""),
    [cursor, setCursor] = useState(""),
    [previous, setPrevious] = useState([]),
    [live, setLive] = useState(false),
    [side, setSide] = useState(true),
    [uploadError, setUploadError] = useState(""),
    [uploading, setUploading] = useState(false);
  const input = useRef(),
    upload = useRef(),
    qc = useQueryClient();
  const nav = navigation.find((n) => n[0] === location.kind) || navigation[0],
    kind = nav[0],
    NavIcon = nav[2],
    monitoring = ["overview", "metrics", "logs"].includes(kind);
  useEffect(() => {
    const fn = () => setLocation(readRoute());
    window.addEventListener("hashchange", fn);
    return () => window.removeEventListener("hashchange", fn);
  }, []);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(search), 250);
    return () => clearTimeout(t);
  }, [search]);
  useEffect(() => {
    setCursor("");
    setPrevious([]);
  }, [
    debounced,
    state,
    period,
    location.session,
    location.task,
    location.after,
    location.before,
    location.state,
  ]);
  useEffect(() => {
    setSearch("");
    setDebounced("");
    setState(location.state || "");
    setPeriod("");
    setCursor("");
    setPrevious([]);
    setUploadError("");
  }, [kind, location.state]);
  const filters = useMemo(
    () => ({
      q: debounced || undefined,
      state: state || undefined,
      after: period
        ? new Date(Date.now() - Number(period) * 3600000).toISOString()
        : location.after || undefined,
      before: location.before || undefined,
      time_field: location.time_field || undefined,
      session_id: location.session || undefined,
      task_id: location.task || undefined,
    }),
    [
      debounced,
      state,
      period,
      location.session,
      location.task,
      location.after,
      location.before,
      location.state,
      location.time_field,
    ],
  );
  const list = useQuery({
    queryKey: ["browse", kind, filters, cursor],
    enabled: !monitoring,
    queryFn: ({ signal }) =>
      api
        .call("consoleBrowse", {
          path: { kind },
          query: { ...filters, cursor: cursor || undefined, limit: 50 },
          signal,
        })
        .then((r) => r.data),
    refetchInterval: live ? 5000 : false,
    refetchIntervalInBackground: false,
  });
  const rows = list.data?.data || [],
    selected = location.id || rows[0]?.id,
    record = rows.find((r) => r.id === selected) || {
      id: selected,
      name: selected,
      meta: {},
    };
  const index = rows.findIndex((r) => r.id === selected);
  function select(id) {
    const extra = routeFilters(location);
    delete extra.span;
    route(kind, id, extra);
  }
  useEffect(() => {
    const key = (e) => {
      if (
        ["INPUT", "TEXTAREA", "SELECT"].includes(
          document.activeElement?.tagName,
        ) ||
        e.metaKey ||
        e.ctrlKey ||
        e.altKey
      )
        return;
      if (monitoring) return;
      if (e.key === "/") {
        e.preventDefault();
        input.current?.focus();
      }
      if (e.key === "ArrowDown" && index < rows.length - 1) {
        e.preventDefault();
        select(rows[index + 1].id);
      }
      if (e.key === "ArrowUp" && index > 0) {
        e.preventDefault();
        select(rows[index - 1].id);
      }
      if (e.key === "Escape") route(kind);
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [rows, index, kind, location, monitoring]);
  async function importFile(file) {
    if (!file) return;
    setUploadError("");
    if (file.size > 16 * 1048576) {
      setUploadError("报告最大支持 16 MiB");
      return;
    }
    setUploading(true);
    try {
      const result = await api.upload("consoleImportBench", file.name, file);
      await qc.invalidateQueries({ queryKey: ["browse", "benchmarks"] });
      select(result.data.id);
    } catch (e) {
      setUploadError(e.message);
    } finally {
      setUploading(false);
      upload.current.value = "";
    }
  }
  return (
    <div className={"shell " + (!side ? "collapsed" : "")}>
      <aside className="sidebar">
        <a className="brand" href="#/overview">
          <span className="brand-icon">
            <IconWaveSine size={25} />
          </span>
          <span>
            Wave<span className="brand-light"> AI</span>
          </span>
        </a>
        <nav aria-label="主导航">
          {navigation.map(([id, label, Icon]) => (
            <React.Fragment key={id}>
              {["overview", "environments", "benchmarks"].includes(id) && (
                <div className="nav-caption">
                  {id === "overview"
                    ? "Monitoring"
                    : id === "environments"
                      ? "资源与执行"
                      : "性能测试"}
                </div>
              )}
              <a
                title={label}
                key={id}
                href={"#/" + id}
                aria-current={kind === id ? "page" : undefined}
                className={kind === id ? "active" : ""}
              >
                <Icon size={19} stroke={1.6} />
                <span>{label}</span>
                {kind === id && <span className="nav-indicator" />}
              </a>
            </React.Fragment>
          ))}
        </nav>
        <div className="side-bottom">
          <a href="/swagger/index.html" target="_blank" rel="noreferrer">
            <IconBook2 size={18} />
            <span>API 文档</span>
            <IconExternalLink size={13} />
          </a>
          <button onClick={logout}>
            <IconLogout size={18} />
            <span>断开连接</span>
          </button>
          <div className="account">
            <span className="avatar">W</span>
            <div>
              <b>Wave workspace</b>
            </div>
          </div>
        </div>
      </aside>
      <main className="workspace">
        <header className="workspace-header">
          <IconButton title="切换侧栏" onClick={() => setSide(!side)}>
            <IconLayoutSidebar size={19} />
          </IconButton>
          <strong>Workspace</strong>
          <span className="slash">/</span>
          <span>{nav[1]}</span>
          <span className="header-spacer" />
          <span className="connection">
            <i className="online-dot" />
            已连接
          </span>
          <button
            className={"live-button " + (live ? "on" : "")}
            onClick={() => setLive(!live)}
            title="仅在当前页面可见时每 5 秒刷新列表"
          >
            <i /> {live ? "实时刷新 · 5s" : "实时刷新"}
          </button>
          <IconButton title="刷新数据" onClick={() => qc.invalidateQueries()}>
            <IconRefresh size={17} className={list.isFetching ? "spin" : ""} />
          </IconButton>
          <span className="mobile-only">
            <IconButton title="断开连接" onClick={logout}>
              <IconLogout size={17} />
            </IconButton>
          </span>
        </header>
        {monitoring ? (
          <Suspense fallback={<div className="loading">正在加载…</div>}>
            {kind === "logs" ? (
              <Logs location={location} live={live} />
            ) : (
              <Monitor key={kind} overview={kind === "overview"} live={live} />
            )}
          </Suspense>
        ) : (
          <div className={"workbody " + (location.id ? "has-selection" : "")}>
            <section className="collection" aria-label={nav[1] + " 列表"}>
              <div className="collection-title">
                <NavIcon size={19} />
                <h1>{nav[1]}</h1>
                <span className="count">
                  {rows.length}
                  {list.data?.next_cursor ? "+" : ""}
                </span>
                {kind === "benchmarks" && (
                  <IconButton
                    title="导入报告"
                    disabled={uploading}
                    onClick={() => upload.current.click()}
                  >
                    <IconUpload size={18} />
                  </IconButton>
                )}
              </div>
              <div className="list-tools">
                {(location.after || location.before) && (
                  <div className="range-filter">
                    <span
                      title={`${location.time_field === "finished_at" ? "完成时间：" : ""}${date(location.after)} — ${date(location.before)}`}
                    >
                      {date(location.after)} — {date(location.before)}
                    </span>
                    <button
                      onClick={() => route(kind)}
                      aria-label="清除时间范围"
                    >
                      ×
                    </button>
                  </div>
                )}
                <label className="search">
                  <IconSearch size={17} />
                  <input
                    ref={input}
                    aria-label="搜索资源"
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                    placeholder="搜索名称或 ID…"
                  />
                  <kbd>/</kbd>
                </label>
                <div className="filter-row">
                  <select
                    aria-label="时间范围"
                    value={period}
                    onChange={(e) => {
                      setPeriod(e.target.value);
                      const extra = routeFilters(location);
                      delete extra.after;
                      delete extra.before;
                      route(kind, "", extra);
                    }}
                  >
                    <option value="">
                      {location.after ? "指定时段" : "全部时间"}
                    </option>
                    <option value="1">最近 1 小时</option>
                    <option value="24">最近 24 小时</option>
                    <option value="168">最近 7 天</option>
                    <option value="720">最近 30 天</option>
                  </select>
                  {["tasks", "traces", "sessions", "environments"].includes(
                    kind,
                  ) && (
                    <select
                      aria-label="状态筛选"
                      value={state}
                      onChange={(e) => setState(e.target.value)}
                    >
                      <option value="">全部状态</option>
                      {(["tasks", "traces"].includes(kind)
                        ? [
                            "queued",
                            "running",
                            "waiting",
                            "unknown",
                            "succeeded",
                            "partial",
                            "failed",
                            "canceled",
                          ]
                        : ["active", "archived"]
                      ).map((s) => (
                        <option key={s}>{s}</option>
                      ))}
                    </select>
                  )}
                  {kind === "logs" && (
                    <input
                      aria-label="事件类型"
                      placeholder="事件类型…"
                      value={state}
                      onChange={(e) => setState(e.target.value)}
                    />
                  )}
                </div>
                {(location.session || location.task) && (
                  <button className="filter-chip" onClick={() => route(kind)}>
                    关联 {short(location.task || location.session)}
                    <IconX size={12} />
                  </button>
                )}
              </div>
              {uploadError && (
                <div className="error" role="alert">
                  {uploadError}
                </div>
              )}
              {uploading && <div className="list-note">正在导入报告…</div>}
              <input
                className="hidden"
                ref={upload}
                type="file"
                accept=".json,application/json"
                onChange={(e) => importFile(e.target.files[0])}
              />
              <div className="list-column-labels">
                <span>{kind === "logs" ? "Event" : "Name"}</span>
                <span>
                  {["tasks", "traces"].includes(kind) ? "Latency" : "Status"}
                </span>
              </div>
              <div className="record-list">
                <Load query={list}>
                  {() =>
                    rows.length ? (
                      rows.map((r) => (
                        <button
                          key={r.id}
                          className={
                            "record " + (r.id === selected ? "selected" : "")
                          }
                          onClick={() => select(r.id)}
                          aria-current={r.id === selected ? "true" : undefined}
                        >
                          <div className="record-top">
                            <span className="record-name" title={r.name}>
                              {r.name || short(r.id)}
                            </span>
                            {["tasks", "traces"].includes(kind) ? (
                              <span className="duration">
                                {duration(r.meta.duration_ms)}
                              </span>
                            ) : (
                              <span className={"mini-status " + r.state}>
                                {r.state}
                              </span>
                            )}
                          </div>
                          <div className="record-bottom">
                            <time>{date(r.created_at)}</time>
                            {["tasks", "traces"].includes(kind) ? (
                              <span
                                className={"state-dot " + r.state}
                                title={r.state}
                              />
                            ) : (
                              <span className="record-id">
                                {r.id.slice(-7)}
                              </span>
                            )}
                          </div>
                        </button>
                      ))
                    ) : (
                      <Empty
                        title={
                          debounced || state
                            ? "没有匹配的记录"
                            : "暂无" + nav[1]
                        }
                      >
                        {kind === "benchmarks"
                          ? "点击右上角导入 Wave bench JSON 报告。"
                          : undefined}
                      </Empty>
                    )
                  }
                </Load>
              </div>
              <footer className="pagination">
                <span>每页 50 条</span>
                <IconButton
                  title="上一页"
                  disabled={!previous.length || list.isFetching}
                  onClick={() => {
                    setCursor(previous.at(-1));
                    setPrevious(previous.slice(0, -1));
                  }}
                >
                  <IconChevronLeft size={15} />
                </IconButton>
                <span>{previous.length + 1}</span>
                <IconButton
                  title="下一页"
                  disabled={!list.data?.next_cursor || list.isFetching}
                  onClick={() => {
                    setPrevious([...previous, cursor]);
                    setCursor(list.data.next_cursor);
                    route(kind, "", {
                      ...routeFilters(location),
                    });
                  }}
                >
                  <IconChevronRight size={15} />
                </IconButton>
              </footer>
            </section>
            <section className="detail" aria-label="资源详情">
              {selected ? (
                <>
                  <header className="detail-header">
                    <NavIcon size={17} />
                    <strong>{kind === "traces" ? "Trace" : nav[1]}</strong>
                    <span className="mono truncate" title={selected}>
                      {selected}
                    </span>
                    <Copy text={selected} />
                    <span className="header-spacer" />
                    <IconButton
                      title="上一条"
                      disabled={index <= 0}
                      onClick={() => select(rows[index - 1].id)}
                    >
                      <IconArrowUp size={16} />
                    </IconButton>
                    <IconButton
                      title="下一条"
                      disabled={index < 0 || index >= rows.length - 1}
                      onClick={() => select(rows[index + 1].id)}
                    >
                      <IconArrowDown size={16} />
                    </IconButton>
                    <IconButton title="返回列表" onClick={() => route(kind)}>
                      <IconX size={16} />
                    </IconButton>
                  </header>
                  <DetailBoundary key={kind + selected}>
                    <Suspense
                      fallback={<div className="loading">正在加载详情…</div>}
                    >
                      {kind === "traces" ? (
                        <Trace
                          key={selected + location.span}
                          id={selected}
                          live={live}
                          initialSpan={location.span}
                        />
                      ) : (
                        <Resource
                          key={kind + selected}
                          kind={kind}
                          record={record}
                          id={selected}
                        />
                      )}
                    </Suspense>
                  </DetailBoundary>
                </>
              ) : (
                <Empty title="选择一条记录" />
              )}
            </section>
          </div>
        )}
      </main>
    </div>
  );
}
