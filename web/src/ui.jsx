import React, { useState } from "react";
import {
  IconCopy,
  IconCheck,
  IconChevronDown,
  IconDatabaseOff,
  IconAlertCircle,
  IconLoader2,
} from "@tabler/icons-react";
export function PageHeader({ title, icon: Icon, count, children }) {
  return (
    <header className="page-header">
      <div className="page-heading">
        <Icon size={20} stroke={2} aria-hidden="true" />
        <h1>{title}</h1>
        {count != null && <span className="count">{count}</span>}
      </div>
      {children && <div className="page-header-actions">{children}</div>}
    </header>
  );
}
export function IconButton({ title, children, ...props }) {
  return (
    <button className="icon-button" title={title} aria-label={title} {...props}>
      {children}
    </button>
  );
}
export function Copy({ text }) {
  const [copied, setCopied] = useState(false),
    [error, setError] = useState(false);
  return (
    <IconButton
      title={error ? "复制失败，请手动选择文本" : copied ? "已复制" : "复制"}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setCopied(true);
          setTimeout(() => setCopied(false), 1600);
        } catch {
          setError(true);
        }
      }}
    >
      {copied ? <IconCheck size={15} /> : <IconCopy size={15} />}
    </IconButton>
  );
}
export function Badge({ state }) {
  return (
    <span className={"badge " + state}>
      <i />
      {state || "unknown"}
    </span>
  );
}
export function Empty({ title = "暂无数据", children }) {
  return (
    <div className="empty">
      <IconDatabaseOff size={28} stroke={1.3} />
      <strong>{title}</strong>
      {children && <p>{children}</p>}
    </div>
  );
}
export function Load({ query, children }) {
  if (query.isPending)
    return (
      <div className="loading">
        <IconLoader2 size={18} className="spin" />
        正在加载…
      </div>
    );
  if (query.error)
    return (
      <div className="error">
        <IconAlertCircle size={20} />
        <span>{query.error.message}</span>
        <button type="button" onClick={() => query.refetch()}>
          重试
        </button>
      </div>
    );
  return children(query.data);
}
export function Section({ title, children, copy, open = true }) {
  return (
    <details className="section" open={open}>
      <summary>
        <IconChevronDown size={14} />
        <b>{title}</b>
        {copy != null && (
          <span onClick={(e) => e.preventDefault()}>
            <Copy
              text={
                typeof copy === "string" ? copy : JSON.stringify(copy, null, 2)
              }
            />
          </span>
        )}
      </summary>
      <div className="section-body">{children}</div>
    </details>
  );
}
export function Code({ value, green = false }) {
  let text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  return (
    <pre className={green ? "code green" : "code"}>
      {text?.slice(0, 131072) || "—"}
      {text?.length > 131072
        ? "\n…预览已截断（128 KiB 字符），请下载原文件。"
        : ""}
    </pre>
  );
}
export function Fields({ values }) {
  return (
    <dl className="fields">
      {Object.entries(values).map(([k, v]) => (
        <React.Fragment key={k}>
          <dt>{k}</dt>
          <dd>{v ?? "—"}</dd>
        </React.Fragment>
      ))}
    </dl>
  );
}
export function Metric({ label, value, sub }) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
      {sub && <small>{sub}</small>}
    </div>
  );
}
export function Tabs({ items, value, onChange }) {
  return (
    <div className="tabs" role="tablist">
      {items.map(([id, label, Icon]) => (
        <button
          role="tab"
          aria-selected={id === value}
          key={id}
          onClick={() => onChange(id)}
          className={id === value ? "selected" : ""}
        >
          {Icon && <Icon size={16} />} {label}
        </button>
      ))}
    </div>
  );
}

export class DetailBoundary extends React.Component {
  state = { error: null };
  static getDerivedStateFromError(error) {
    return { error };
  }
  render() {
    return this.state.error ? (
      <div className="error" role="alert">
        此记录的数据格式无法显示。请选择另一条记录，或下载原始数据检查。
      </div>
    ) : (
      this.props.children
    );
  }
}
