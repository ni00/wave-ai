import React, { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { IconPlus, IconX, IconDownload } from "@tabler/icons-react";
import { useAPI, useResource, route, date } from "./data.js";
import {
  Load,
  Section,
  Code,
  Fields,
  Badge,
  Tabs,
  Empty,
  IconButton,
} from "./ui.jsx";

const emptyAgent = {
  name: "",
  model: "",
  instructions: "",
  tools: [],
  skill_ids: [],
  expert_ids: [],
};
function Link({ kind, id, extra, children }) {
  return (
    <button className="button" onClick={() => route(kind, id || "", extra)}>
      {children} ↗
    </button>
  );
}
function useDetail(kind, id) {
  const api = useAPI();
  return useQuery({
    queryKey: ["consoleResource", kind, id],
    queryFn: ({ signal }) =>
      api
        .call("consoleResource", { path: { kind, id }, signal })
        .then((r) => r.data),
  });
}
function useChange() {
  const api = useAPI(),
    qc = useQueryClient();
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  async function change(op, options, done) {
    setBusy(true);
    setError("");
    try {
      const result = await api.call(op, options);
      await qc.invalidateQueries();
      done?.(result.data);
    } catch (e) {
      setError(e.message);
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, change };
}
function ErrorMessage({ error }) {
  return error ? (
    <div className="error" role="alert">
      {error}
    </div>
  ) : null;
}

// Pages and selectors share the same bounded, owner-scoped search endpoint.
function Records({ kind, filter = {}, render }) {
  const api = useAPI();
  const [cursor, setCursor] = useState(""),
    [previous, setPrevious] = useState([]);
  const key = JSON.stringify(filter);
  useEffect(() => {
    setCursor("");
    setPrevious([]);
  }, [key]);
  const query = useQuery({
    queryKey: ["browse", kind, filter, cursor, "related"],
    queryFn: ({ signal }) =>
      api
        .call("consoleBrowse", {
          path: { kind },
          query: { ...filter, cursor: cursor || undefined, limit: 20 },
          signal,
        })
        .then((r) => r.data),
  });
  return (
    <Load query={query}>
      {(d) => (
        <>
          {d.data.length ? (
            <div className="related-records">
              {d.data.map((r) => (
                <React.Fragment key={r.id}>
                  {render ? (
                    render(r)
                  ) : (
                    <button
                      type="button"
                      className="related-record"
                      onClick={() => route(kind, r.id)}
                    >
                      <span>{r.name || r.id}</span>
                      <Badge state={r.state} />
                    </button>
                  )}
                </React.Fragment>
              ))}
            </div>
          ) : (
            <Empty title="暂无记录" />
          )}
          <div className="page-controls">
            <button
              type="button"
              disabled={!previous.length || query.isFetching}
              onClick={() => {
                setCursor(previous.at(-1));
                setPrevious(previous.slice(0, -1));
              }}
            >
              上一页
            </button>
            <span>{previous.length + 1}</span>
            <button
              type="button"
              disabled={!d.next_cursor || query.isFetching}
              onClick={() => {
                setPrevious([...previous, cursor]);
                setCursor(d.next_cursor);
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
function Picker({ kind, value, onChange, multiple = false }) {
  const [search, setSearch] = useState(""),
    [term, setTerm] = useState(""),
    [open, setOpen] = useState(false);
  useEffect(() => {
    const t = setTimeout(() => setTerm(search), 250);
    return () => clearTimeout(t);
  }, [search]);
  const selected = multiple ? value : value ? [value] : [];
  return (
    <div className="resource-picker">
      <div className="selected-resources">
        {selected.map((id) => (
          <button
            type="button"
            key={id}
            title={id}
            onClick={() =>
              onChange(multiple ? selected.filter((v) => v !== id) : "")
            }
          >
            {id}
            <IconX size={12} />
          </button>
        ))}
      </div>
      <button type="button" onClick={() => setOpen(!open)}>
        {open ? "收起" : "选择 " + kind}
      </button>
      {open && (
        <div className="picker-results">
          <input
            aria-label={"搜索 " + kind}
            placeholder="搜索名称或 ID…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <Records
            kind={kind}
            filter={{
              q: term || undefined,
              state: kind === "skills" ? undefined : "active",
            }}
            render={(r) => (
              <button
                type="button"
                className="related-record"
                aria-pressed={selected.includes(r.id)}
                onClick={() => {
                  onChange(
                    multiple
                      ? selected.includes(r.id)
                        ? selected.filter((id) => id !== r.id)
                        : [...selected, r.id]
                      : r.id,
                  );
                  if (!multiple) setOpen(false);
                }}
              >
                <span>
                  {r.name}
                  <small>{r.id}</small>
                </span>
                <span>{selected.includes(r.id) ? "✓" : "+"}</span>
              </button>
            )}
          />
        </div>
      )}
    </div>
  );
}
function Editor({ kind, agent, close }) {
  const dialog = useRef();
  const [config, setConfig] = useState(agent?.config || emptyAgent);
  const [tools, setTools] = useState(
    JSON.stringify(agent?.config?.tools || [], null, 2),
  );
  const [deployment, setDeployment] = useState({
    name: "",
    agent_id: "",
    environment_id: "",
    input: "",
    cron: "",
  });
  const selectedAgent = useResource(
    "agentsGet",
    kind === "deployments" ? deployment.agent_id : "",
  );
  const environmentRequired = !!selectedAgent.data?.config?.skill_ids?.length;
  const [validation, setValidation] = useState("");
  const { busy, error, change } = useChange();
  useEffect(() => {
    const d = dialog.current;
    d.showModal();
    return () => d.close();
  }, []);
  const field = (name, value) =>
    kind === "agents"
      ? setConfig((c) => ({ ...c, [name]: value }))
      : setDeployment((c) => ({ ...c, [name]: value }));
  async function submit(e) {
    e.preventDefault();
    setValidation("");
    if (kind === "agents") {
      let parsed;
      try {
        parsed = JSON.parse(tools);
        if (!Array.isArray(parsed)) throw new Error();
      } catch {
        setValidation("Tools 必须是 JSON 数组");
        return;
      }
      const body = { ...config, tools: parsed };
      await change(
        agent ? "agentsUpdate" : "agentsCreate",
        {
          ...(agent
            ? {
                path: { id: agent.id },
                body: { version: agent.version, config: body },
              }
            : { body }),
        },
        (result) => {
          close();
          route(kind, result.id);
        },
      );
    } else {
      if (!deployment.agent_id) {
        setValidation("请选择 Agent");
        return;
      }
      if (
        !deployment.environment_id &&
        (selectedAgent.isPending || selectedAgent.error)
      ) {
        setValidation("请等待 Agent 配置加载完成后保存");
        return;
      }
      if (environmentRequired && !deployment.environment_id) {
        setValidation("此 Agent 使用 Skills，请选择 Environment");
        return;
      }
      await change("deploymentsCreate", { body: deployment }, (result) => {
        close();
        route(kind, result.id);
      });
    }
  }
  return (
    <dialog
      className="management-dialog"
      ref={dialog}
      aria-labelledby="editor-title"
      onCancel={(e) => {
        e.preventDefault();
        if (!busy) close();
      }}
    >
      <div className="editor-heading">
        <h2 id="editor-title">
          {agent
            ? "编辑 Agent"
            : kind === "agents"
              ? "新建 Agent"
              : "新建 Deployment"}
        </h2>
        <IconButton title="关闭" disabled={busy} onClick={close}>
          <IconX size={18} />
        </IconButton>
      </div>
      <form onSubmit={submit}>
        <fieldset disabled={busy}>
          <label>
            名称
            <input
              autoFocus
              required
              maxLength={128}
              value={kind === "agents" ? config.name : deployment.name}
              onChange={(e) => field("name", e.target.value)}
            />
          </label>
          {kind === "agents" ? (
            <>
              <label>
                Model
                <input
                  required
                  value={config.model}
                  onChange={(e) => field("model", e.target.value)}
                />
              </label>
              <label>
                Instructions
                <textarea
                  rows={6}
                  value={config.instructions || ""}
                  onChange={(e) => field("instructions", e.target.value)}
                />
              </label>
              <label>
                Effort
                <input
                  value={config.effort || ""}
                  onChange={(e) => field("effort", e.target.value)}
                />
              </label>
              <div className="form-field">
                <span>Skills</span>
                <Picker
                  kind="skills"
                  multiple
                  value={config.skill_ids || []}
                  onChange={(v) => field("skill_ids", v)}
                />
              </div>
              <div className="form-field">
                <span>Experts</span>
                <Picker
                  kind="agents"
                  multiple
                  value={config.expert_ids || []}
                  onChange={(v) => field("expert_ids", v)}
                />
              </div>
              <label>
                Tools · JSON
                <textarea
                  className="mono"
                  rows={8}
                  value={tools}
                  onChange={(e) => setTools(e.target.value)}
                  spellCheck={false}
                />
              </label>
              <div className="tool-presets">
                {["bash", "read", "write", "edit", "ls", "grep", "find"].map(
                  (name) => (
                    <button
                      type="button"
                      key={name}
                      onClick={() => {
                        try {
                          const current = JSON.parse(tools);
                          if (!Array.isArray(current)) throw new Error();
                          if (!current.some((t) => t.name === name))
                            setTools(
                              JSON.stringify(
                                [...current, { name, kind: "builtin" }],
                                null,
                                2,
                              ),
                            );
                        } catch {
                          setValidation("请先修正 Tools JSON");
                        }
                      }}
                    >
                      + {name}
                    </button>
                  ),
                )}
              </div>
            </>
          ) : (
            <>
              <div className="form-field">
                <span>Agent</span>
                <Picker
                  kind="agents"
                  value={deployment.agent_id}
                  onChange={(v) => field("agent_id", v)}
                />
              </div>
              <div className="form-field">
                <span>
                  {environmentRequired
                    ? "Environment（必选）"
                    : "Environment（可选）"}
                </span>
                <Picker
                  kind="environments"
                  value={deployment.environment_id}
                  onChange={(v) => field("environment_id", v)}
                />
              </div>
              <label>
                Input
                <textarea
                  required
                  rows={6}
                  value={deployment.input}
                  onChange={(e) => field("input", e.target.value)}
                />
              </label>
              <label>
                Cron
                <input
                  placeholder="留空则仅手动触发"
                  value={deployment.cron}
                  onChange={(e) => field("cron", e.target.value)}
                />
              </label>
            </>
          )}
        </fieldset>
        <ErrorMessage error={validation || error} />
        <div className="editor-actions">
          <button type="button" disabled={busy} onClick={close}>
            取消
          </button>
          <button className="primary" disabled={busy}>
            {busy ? "保存中…" : "保存"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
export function CreateAction({ kind }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <IconButton
        title={kind === "agents" ? "新建 Agent" : "新建 Deployment"}
        onClick={() => setOpen(true)}
      >
        <IconPlus size={18} />
      </IconButton>
      {open && <Editor kind={kind} close={() => setOpen(false)} />}
    </>
  );
}
export default function Management({ kind, id }) {
  return kind === "agents" ? (
    <Agent id={id} />
  ) : (
    <ManagedResource key={kind + id} kind={kind} id={id} />
  );
}
function Agent({ id }) {
  const q = useResource("agentsGet", id),
    [tab, setTab] = useState("config"),
    [editing, setEditing] = useState(false);
  const { busy, error, change } = useChange();
  return (
    <>
      <Tabs
        items={[
          ["config", "配置"],
          ["versions", "版本"],
          ["tasks", "Tasks"],
          ["deployments", "Deployments"],
        ]}
        value={tab}
        onChange={setTab}
      />
      <div className="resource-scroll">
        <Load query={q}>
          {(a) => (
            <>
              <div className="resource-heading">
                <h2>{a.config.name}</h2>
                <Badge state={a.archived ? "archived" : "active"} />
                <p>
                  {a.config.model} · v{a.version}
                </p>
                <div className="actions">
                  <button
                    className="button"
                    disabled={a.archived}
                    onClick={() => setEditing(true)}
                  >
                    编辑
                  </button>
                  <button
                    className="button"
                    disabled={busy || a.archived}
                    onClick={() => {
                      if (
                        window.confirm("归档此 Agent？归档后无法创建新任务。")
                      )
                        change("agentsArchive", { path: { id } });
                    }}
                  >
                    归档
                  </button>
                </div>
                <ErrorMessage error={error} />
              </div>
              {editing && (
                <Editor
                  kind="agents"
                  agent={a}
                  close={() => setEditing(false)}
                />
              )}
              {tab === "config" && (
                <>
                  <Section title="Instructions">
                    <Code value={a.config.instructions} />
                  </Section>
                  <Section title="Tools">
                    <Code value={a.config.tools} />
                  </Section>
                  <Section title="Skills">
                    <div className="actions">
                      {a.config.skill_ids?.map((id) => (
                        <Link key={id} kind="skills" id={id}>
                          {id}
                        </Link>
                      ))}
                    </div>
                  </Section>
                  <Section title="Experts">
                    <div className="actions">
                      {a.config.expert_ids?.map((id) => (
                        <Link key={id} kind="agents" id={id}>
                          {id}
                        </Link>
                      ))}
                    </div>
                  </Section>
                  <Section title="Metadata">
                    <Fields
                      values={{
                        ID: id,
                        Version: a.version,
                        Effort: a.config.effort,
                        创建时间: date(a.created_at),
                        更新时间: date(a.updated_at),
                      }}
                    />
                  </Section>
                </>
              )}
              {tab === "versions" && <Versions id={id} />}
              {tab === "tasks" && (
                <Records kind="tasks" filter={{ agent_id: id }} />
              )}
              {tab === "deployments" && (
                <Records kind="deployments" filter={{ agent_id: id }} />
              )}
            </>
          )}
        </Load>
      </div>
    </>
  );
}
function Versions({ id }) {
  const [offset, setOffset] = useState(0),
    q = useResource("agentsVersions", id, { limit: 20, offset });
  return (
    <Load query={q}>
      {(d) => (
        <>
          {d.data.map((v) => (
            <Section
              key={v.version}
              title={`v${v.version} · ${date(v.created_at)}`}
              open={false}
            >
              <Code value={v.config} />
            </Section>
          ))}
          <div className="page-controls">
            <button
              type="button"
              disabled={!offset}
              onClick={() => setOffset(Math.max(0, offset - 20))}
            >
              上一页
            </button>
            <button
              type="button"
              disabled={!d.next_offset}
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
function ManagedResource({ kind, id }) {
  const q = useDetail(kind, id),
    api = useAPI(),
    [tab, setTab] = useState("detail");
  const [downloading, setDownloading] = useState(false),
    [downloadError, setDownloadError] = useState("");
  const { busy, error, change } = useChange();
  async function download(name) {
    setDownloading(true);
    setDownloadError("");
    try {
      let size = 0;
      const chunks = [];
      await api.download("skillsContent", id, (chunk) => {
        size += chunk.byteLength;
        if (size > 24 * 1048576) throw new Error("技能包超过 24 MiB");
        chunks.push(chunk);
      });
      const url = URL.createObjectURL(
        new Blob(chunks, { type: "application/zip" }),
      );
      const a = document.createElement("a");
      a.href = url;
      a.download = name + ".zip";
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 60000);
    } catch (e) {
      setDownloadError(e.message);
    } finally {
      setDownloading(false);
    }
  }
  const tabs =
    kind === "skills"
      ? [
          ["detail", "SKILL.md"],
          ["files", "Files"],
          ["agents", "Agents"],
        ]
      : kind === "deployments"
        ? [
            ["detail", "配置"],
            ["runs", "运行记录"],
          ]
        : [
            ["detail", "实例"],
            ["tasks", "Tasks"],
          ];
  return (
    <>
      <Tabs items={tabs} value={tab} onChange={setTab} />
      <div className="resource-scroll">
        <Load query={q}>
          {(r) => {
            const m = r.meta;
            return (
              <>
                <div className="resource-heading">
                  <h2>{r.name || r.id}</h2>
                  <Badge state={r.state} />
                  <p>{date(r.created_at)}</p>
                  <div className="actions">
                    {kind === "skills" && (
                      <button
                        className="button"
                        disabled={downloading}
                        onClick={() => download(r.name)}
                      >
                        <IconDownload size={15} />
                        下载 ZIP
                      </button>
                    )}
                    {kind === "deployments" && (
                      <>
                        <button
                          className="button"
                          disabled={busy || m.paused}
                          onClick={() =>
                            change(
                              "deploymentsRun",
                              { path: { id } },
                              (run) => {
                                setTab("runs");
                                if (run.task_id) route("tasks", run.task_id);
                              },
                            )
                          }
                        >
                          运行
                        </button>
                        <button
                          className="button"
                          disabled={busy}
                          onClick={() =>
                            change("deploymentsPause", {
                              path: { id },
                              body: { paused: !m.paused },
                            })
                          }
                        >
                          {m.paused ? "恢复" : "暂停"}
                        </button>
                        <Link kind="agents" id={m.agent_id}>
                          Agent
                        </Link>
                        {m.environment_id && (
                          <Link kind="environments" id={m.environment_id}>
                            Environment
                          </Link>
                        )}
                      </>
                    )}
                    {kind === "sandboxes" && (
                      <>
                        <Link kind="sessions" id={r.session_id}>
                          Session
                        </Link>
                        <Link kind="traces" extra={{ session: r.session_id }}>
                          Traces
                        </Link>
                        <Link kind="logs" extra={{ session: r.session_id }}>
                          Logs
                        </Link>
                      </>
                    )}
                  </div>
                  <ErrorMessage error={error || downloadError} />
                </div>
                {kind === "skills" && (
                  <>
                    {tab === "detail" && (
                      <>
                        <Section title="Description">
                          <Code value={m.description} />
                        </Section>
                        <Section title="SKILL.md" copy={m.content}>
                          <Code value={m.content} />
                          {m.truncated && (
                            <p>预览已截断，下载 ZIP 查看完整文件。</p>
                          )}
                        </Section>
                      </>
                    )}
                    {tab === "files" && (
                      <Section title="Files">
                        {m.files.map((name) => (
                          <div className="package-row" key={name}>
                            {name}
                          </div>
                        ))}
                      </Section>
                    )}
                    {tab === "agents" && (
                      <Records kind="agents" filter={{ skill_id: id }} />
                    )}
                  </>
                )}
                {kind === "deployments" &&
                  (tab === "runs" ? (
                    <Runs id={id} />
                  ) : (
                    <>
                      <Section title="Schedule">
                        <Fields
                          values={{
                            Cron: m.cron || "手动",
                            下次运行: m.next_at ? date(m.next_at) : "—",
                          }}
                        />
                        {m.last_task_id && (
                          <Link kind="tasks" id={m.last_task_id}>
                            最近任务
                          </Link>
                        )}
                      </Section>
                      <Section title="Input">
                        <Code value={m.input} />
                      </Section>
                    </>
                  ))}
                {kind === "sandboxes" &&
                  (tab === "tasks" ? (
                    <Records
                      kind="tasks"
                      filter={{ session_id: r.session_id }}
                    />
                  ) : (
                    <>
                      <Section title="Allocation">
                        <Fields
                          values={{
                            Backend: m.backend,
                            "CPU 配额": `${m.cpus} vCPU`,
                            内存配额: `${m.memory_mib} MiB`,
                            Image: m.image,
                            "实例 ID": m.backend_id || "—",
                            状态更新时间: date(m.updated_at),
                          }}
                        />
                      </Section>
                      <SandboxEnvironment session={r.session_id} />
                    </>
                  ))}
              </>
            );
          }}
        </Load>
      </div>
    </>
  );
}
function SandboxEnvironment({ session }) {
  const q = useResource("executionGetSession", session);
  return (
    <Load query={q}>
      {(s) =>
        s.environment_id ? (
          <Section title="Environment">
            <Link kind="environments" id={s.environment_id}>
              {s.environment_id}
            </Link>
          </Section>
        ) : null
      }
    </Load>
  );
}
function Runs({ id }) {
  const [offset, setOffset] = useState(0),
    q = useResource("deploymentsRuns", id, { offset, limit: 20 });
  return (
    <Load query={q}>
      {(d) => (
        <>
          {d.data.length ? (
            d.data.map((r) => (
              <Section key={r.id} title={date(r.created_at)}>
                <Fields values={{ ID: r.id, Reason: r.reason }} />
                <div className="actions">
                  {r.task_id && (
                    <>
                      <Link kind="tasks" id={r.task_id}>
                        Task
                      </Link>
                      <Link kind="traces" id={r.task_id}>
                        Trace
                      </Link>
                    </>
                  )}
                  {r.session_id && (
                    <Link kind="sessions" id={r.session_id}>
                      Session
                    </Link>
                  )}
                </div>
              </Section>
            ))
          ) : (
            <Empty title="暂无运行记录" />
          )}
          <div className="page-controls">
            <button
              type="button"
              disabled={!offset}
              onClick={() => setOffset(Math.max(0, offset - 20))}
            >
              上一页
            </button>
            <button
              type="button"
              disabled={!d.next_offset}
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
