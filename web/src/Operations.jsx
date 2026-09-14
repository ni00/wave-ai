import React, { useState, useRef, useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { IconPlus, IconX } from "@tabler/icons-react";
import { useAPI, useResource, route, date } from "./data.js";
import {
  Load,
  Section,
  Fields,
  Code,
  Badge,
  Empty,
  IconButton,
} from "./ui.jsx";
import { useChange } from "./Management.jsx";
export function Dialog({ title, close, busy, children }) {
  const ref = useRef();
  useEffect(() => {
    const d = ref.current;
    d.showModal();
    return () => d.close();
  }, []);
  return (
    <dialog
      className="management-dialog"
      ref={ref}
      onCancel={(e) => {
        e.preventDefault();
        if (!busy) close();
      }}
      aria-labelledby="operation-title"
    >
      <div className="editor-heading">
        <h2 id="operation-title">{title}</h2>
        <IconButton title="关闭" disabled={busy} onClick={close}>
          <IconX size={18} />
        </IconButton>
      </div>
      {children}
    </dialog>
  );
}
export function CreateAction({ kind }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <IconButton
        title={kind === "vaults" ? "新建 Vault" : "新建 Webhook"}
        onClick={() => setOpen(true)}
      >
        <IconPlus size={18} />
      </IconButton>
      {open && <Create kind={kind} close={() => setOpen(false)} />}
    </>
  );
}
function Create({ kind, close, vaultID, credential }) {
  const [name, setName] = useState(""),
    [target, setTarget] = useState(""),
    [secret, setSecret] = useState(""),
    [events, setEvents] = useState([
      "task.finished",
      "task.unknown",
      "deployment.run",
    ]);
  const { busy, error, change } = useChange();
  const isVault = kind === "vaults",
    isCredential = kind === "credentials";
  return (
    <Dialog
      title={
        credential
          ? "轮换凭据"
          : isVault
            ? "新建 Vault"
            : isCredential
              ? "新建凭据"
              : "新建 Webhook"
      }
      close={close}
      busy={busy}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const op = credential
            ? "vaultRotate"
            : isVault
              ? "vaultsCreate"
              : isCredential
                ? "vaultCreate"
                : "webhooksCreate";
          const body = credential
            ? { token: secret, version: credential.version }
            : isVault
              ? { name }
              : isCredential
                ? { name, host: target, token: secret, vault_id: vaultID }
                : { name, url: target, secret, events };
          change(
            op,
            { path: credential ? { id: credential.id } : undefined, body },
            (r) => {
              setSecret("");
              close();
              if (!isCredential) route(kind, r.id);
            },
          );
        }}
      >
        <fieldset disabled={busy}>
          {!credential && (
            <label>
              名称
              <input
                required
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
          )}
          {!isVault && !credential && (
            <label>
              {isCredential ? "Host" : "URL"}
              <input
                required
                type={isCredential ? "text" : "url"}
                placeholder={
                  isCredential
                    ? "api.example.com"
                    : "https://example.com/webhook"
                }
                value={target}
                onChange={(e) => setTarget(e.target.value)}
              />
            </label>
          )}
          {!isVault && (
            <label>
              {isCredential ? "Token" : "签名密钥"}
              <input
                required
                type="password"
                autoComplete="new-password"
                minLength={isCredential ? 1 : 32}
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
              />
            </label>
          )}
          {!isVault && !isCredential && (
            <div className="checkbox-grid">
              {[
                "task.created",
                "task.finished",
                "task.unknown",
                "tool.created",
                "budget.exhausted",
                "deployment.run",
              ].map((type) => (
                <label className="check-label" key={type}>
                  <input
                    type="checkbox"
                    checked={events.includes(type)}
                    onChange={(e) =>
                      setEvents(
                        e.target.checked
                          ? [...events, type]
                          : events.filter((x) => x !== type),
                      )
                    }
                  />
                  {type}
                </label>
              ))}
            </div>
          )}
        </fieldset>
        {error && (
          <div className="error" role="alert">
            {error}
          </div>
        )}
        <div className="editor-actions">
          <button type="button" disabled={busy} onClick={close}>
            取消
          </button>
          <button className="primary" disabled={busy}>
            {busy ? "保存中…" : "保存"}
          </button>
        </div>
      </form>
    </Dialog>
  );
}
export default function Operations({ kind, id }) {
  const q = useResource(kind === "vaults" ? "vaultsGet" : "webhooksGet", id);
  const [open, setOpen] = useState(false);
  const { busy, error, change } = useChange();
  return (
    <div className="resource-scroll">
      <Load query={q}>
        {(r) => (
          <>
            <div className="resource-heading">
              <h2>{r.name}</h2>
              <Badge
                state={r.archived ? "archived" : r.paused ? "paused" : "active"}
              />
              <p>{date(r.created_at)}</p>
              <div className="actions">
                {kind === "vaults" ? (
                  <>
                    <button
                      className="button"
                      disabled={r.archived}
                      onClick={() => setOpen(true)}
                    >
                      新建凭据
                    </button>
                    <button
                      className="button"
                      disabled={busy || r.archived}
                      onClick={() => {
                        if (
                          confirm("归档此 Vault？绑定的凭据将不可用于新调用。")
                        )
                          change("vaultsArchive", { path: { id } });
                      }}
                    >
                      归档
                    </button>
                  </>
                ) : (
                  <button
                    className="button"
                    disabled={busy}
                    onClick={() =>
                      change("webhooksUpdate", {
                        path: { id },
                        body: { paused: !r.paused },
                      })
                    }
                  >
                    {r.paused ? "恢复" : "暂停"}
                  </button>
                )}
              </div>
              {error && (
                <div className="error" role="alert">
                  {error}
                </div>
              )}
            </div>
            {kind === "vaults" ? (
              <Credentials id={id} archived={r.archived} />
            ) : (
              <>
                <Section title="订阅">
                  <Fields
                    values={{ URL: r.url, Events: r.events.join(", ") }}
                  />
                </Section>
                <Deliveries id={id} paused={r.paused} />
              </>
            )}
            {open && (
              <Create
                kind="credentials"
                vaultID={id}
                close={() => setOpen(false)}
              />
            )}
          </>
        )}
      </Load>
    </div>
  );
}
function Pagination({ offset, setOffset, next }) {
  return (
    <div className="page-controls">
      <button
        disabled={!offset}
        onClick={() => setOffset(Math.max(0, offset - 20))}
      >
        上一页
      </button>
      <button disabled={!next} onClick={() => setOffset(next)}>
        下一页
      </button>
    </div>
  );
}
function Credentials({ id, archived }) {
  const api = useAPI(),
    [offset, setOffset] = useState(0),
    [rotating, setRotating] = useState(null),
    [valid, setValid] = useState("");
  const { busy, error, change } = useChange();
  const q = useQuery({
    queryKey: ["vaultList", id, offset],
    queryFn: ({ signal }) =>
      api
        .call("vaultList", {
          query: { vault_id: id, offset, limit: 20 },
          signal,
        })
        .then((r) => r.data),
  });
  return (
    <>
      <Load query={q}>
        {(d) => (
          <>
            {d.data.length ? (
              d.data.map((c) => (
                <Section key={c.id} title={c.name}>
                  <Fields
                    values={{
                      ID: c.id,
                      Host: c.host,
                      Version: c.version,
                      State: c.revoked ? "revoked" : "active",
                    }}
                  />
                  <div className="actions">
                    <button
                      className="button"
                      disabled={busy || c.revoked || archived}
                      onClick={() => setRotating(c)}
                    >
                      轮换
                    </button>
                    <button
                      className="button"
                      disabled={busy || c.revoked || archived}
                      onClick={() =>
                        change(
                          "vaultValidate",
                          {
                            path: { id: c.id },
                            body: { target: "https://" + c.host },
                          },
                          () => setValid(c.id),
                        )
                      }
                    >
                      校验绑定
                    </button>
                    <button
                      className="button"
                      disabled={busy || c.revoked}
                      onClick={() => {
                        if (confirm("吊销此凭据？"))
                          change("vaultRevoke", { path: { id: c.id } });
                      }}
                    >
                      吊销
                    </button>
                    {valid === c.id && <span>绑定与加密校验通过</span>}
                  </div>
                </Section>
              ))
            ) : (
              <Empty title="尚无凭据" />
            )}
            <Pagination
              offset={offset}
              setOffset={setOffset}
              next={d.next_offset}
            />
          </>
        )}
      </Load>
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      {rotating && (
        <Create
          kind="credentials"
          credential={rotating}
          close={() => setRotating(null)}
        />
      )}
    </>
  );
}
function Deliveries({ id, paused }) {
  const [offset, setOffset] = useState(0),
    q = useResource("webhooksDeliveries", id, { offset, limit: 20 });
  const { busy, error, change } = useChange();
  return (
    <>
      <Load query={q}>
        {(d) => (
          <>
            {d.data.length ? (
              d.data.map((r) => (
                <Section key={r.id} title={`${r.event_type} · ${r.status}`}>
                  <Fields
                    values={{
                      ID: r.id,
                      "Event ID": r.event_id,
                      Attempts: r.attempts,
                      "HTTP status": r.http_status || "—",
                      Error: r.error,
                      Created: date(r.created_at),
                      Delivered: date(r.delivered_at),
                    }}
                  />
                  {["failed", "paused"].includes(r.status) && (
                    <button
                      className="button"
                      disabled={busy || paused}
                      onClick={() =>
                        change("webhooksRetry", {
                          path: { id, delivery: r.id },
                        })
                      }
                    >
                      重试
                    </button>
                  )}
                </Section>
              ))
            ) : (
              <Empty title="尚无投递记录" />
            )}
            <Pagination
              offset={offset}
              setOffset={setOffset}
              next={d.next_offset}
            />
          </>
        )}
      </Load>
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
    </>
  );
}
