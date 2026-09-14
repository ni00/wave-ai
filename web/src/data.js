import { createContext, useContext } from "react";
import { useQuery } from "@tanstack/react-query";
export const APIContext = createContext(null);
export const useAPI = () => useContext(APIContext);
export function useResource(op, id, query = {}, enabled = true) {
  const api = useAPI();
  return useQuery({
    queryKey: [op, id, query],
    enabled: enabled && !!id,
    queryFn: ({ signal }) =>
      api.call(op, { path: { id }, query, signal }).then((r) => r.data),
  });
}
export const short = (value) =>
  value
    ? value.length > 24
      ? value.slice(0, 13) + "…" + value.slice(-7)
      : value
    : "—";
export const number = (v) =>
  v == null
    ? "—"
    : new Intl.NumberFormat("en", {
        notation: Math.abs(v) >= 10000 ? "compact" : "standard",
        maximumFractionDigits: 1,
      }).format(v);
export const duration = (v) =>
  v == null
    ? "—"
    : v >= 60000
      ? `${Math.floor(v / 60000)}m ${((v % 60000) / 1000).toFixed(1)}s`
      : v >= 1000
        ? `${(v / 1000).toFixed(2)}s`
        : `${v.toFixed(1)}ms`;
export const date = (v) =>
  v ? new Date(v).toLocaleString("zh-CN", { hour12: false }) : "—";
export const bytes = (n) =>
  n == null
    ? "—"
    : n < 1024
      ? `${n} B`
      : n < 1048576
        ? `${(n / 1024).toFixed(1)} KB`
        : `${(n / 1048576).toFixed(1)} MB`;
export function route(kind, id = "", extra = {}) {
  const q = new URLSearchParams(extra);
  if (id) q.set("id", id);
  window.location.hash = `/${kind}${q.size ? "?" + q : ""}`;
}
export function readRoute() {
  const [path, query = ""] = window.location.hash.slice(1).split("?");
  const params = new URLSearchParams(query);
  return {
    kind: path?.slice(1) || "traces",
    id: params.get("id") || "",
    session: params.get("session") || "",
    task: params.get("task") || "",
  };
}
// Traverse once, and tolerate partial parents, cycles and out-of-order spans.
export function flattenSpans(spans, collapsed = new Set()) {
  const byID = new Map(spans.map((s) => [s.id, s])),
    children = new Map(),
    roots = [];
  for (const s of spans) {
    if (s.parent_id && s.parent_id !== s.id && byID.has(s.parent_id)) {
      if (!children.has(s.parent_id)) children.set(s.parent_id, []);
      children.get(s.parent_id).push(s);
    } else roots.push(s);
  }
  const seen = new Set(),
    out = [];
  function visit(root) {
    const stack = [{ span: root, depth: 0 }];
    while (stack.length) {
      const { span, depth } = stack.pop();
      if (seen.has(span.id)) continue;
      seen.add(span.id);
      out.push({
        ...span,
        depth,
        hasChildren: !!children.get(span.id)?.length,
      });
      if (!collapsed.has(span.id))
        for (const child of [...(children.get(span.id) || [])].reverse())
          stack.push({ span: child, depth: depth + 1 });
      else {
        const hidden = [...(children.get(span.id) || [])];
        while (hidden.length) {
          const s = hidden.pop();
          if (seen.has(s.id)) continue;
          seen.add(s.id);
          hidden.push(...(children.get(s.id) || []));
        }
      }
    }
  }
  roots.forEach(visit);
  spans.forEach((s) => {
    if (!seen.has(s.id)) visit(s);
  });
  return out;
}
export function comparable(a, b) {
  const canonical = (v) =>
    JSON.stringify(v, (_k, x) =>
      x && typeof x === "object" && !Array.isArray(x)
        ? Object.fromEntries(
            Object.entries(x).sort(([a], [b]) => a.localeCompare(b)),
          )
        : x,
    );
  const phases = (r) => r.phases?.map((p) => p.workers).sort((a, b) => a - b);
  return (
    !!a &&
    !!b &&
    a.kind === b.kind &&
    !!a.workload_sha256 &&
    a.workload_sha256 === b.workload_sha256 &&
    canonical({ ...a.options, workers: "" }) ===
      canonical({ ...b.options, workers: "" }) &&
    a.model === b.model &&
    canonical(phases(a)) === canonical(phases(b))
  );
}
