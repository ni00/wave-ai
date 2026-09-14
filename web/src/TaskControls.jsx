import React, { useState } from "react";
import { useChange } from "./Management.jsx";
import { Dialog } from "./Operations.jsx";
export function TaskControls({ task: t }) {
  const [mode, setMode] = useState(""),
    [text, setText] = useState("");
  const { busy, error, change } = useChange();
  const terminal = ["succeeded", "partial", "failed", "canceled"].includes(
    t.state,
  );
  return (
    <div className="task-controls">
      {!terminal && (
        <>
          <button
            className="button"
            disabled={busy}
            onClick={() => setMode("input")}
          >
            发送输入
          </button>
          <button
            className="button"
            disabled={busy || t.cancel_requested}
            onClick={() => {
              if (confirm("取消当前任务？"))
                change("executionCancelTask", { path: { id: t.id } });
            }}
          >
            {t.cancel_requested ? "正在取消" : "取消任务"}
          </button>
        </>
      )}
      {terminal && t.parent_id && (
        <button
          className="button"
          disabled={busy}
          onClick={() => setMode("resume")}
        >
          追加任务
        </button>
      )}
      {t.state === "unknown" && (
        <button
          className="button"
          disabled={busy}
          onClick={() => {
            if (confirm("确认执行进程已经停止？请先核查 Sandbox 和工具状态。"))
              change("executionReconcile", {
                path: { id: t.id },
                body: { confirm_stopped: true },
              });
          }}
        >
          确认执行已停止
        </button>
      )}
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      {mode && (
        <Dialog
          title={mode === "input" ? "发送输入" : "追加任务"}
          busy={busy}
          close={() => setMode("")}
        >
          <form
            onSubmit={(e) => {
              e.preventDefault();
              change(
                mode === "input" ? "executionAddInput" : "executionResumeTask",
                { path: { id: t.id }, body: { text } },
                () => {
                  setText("");
                  setMode("");
                },
              );
            }}
          >
            <label>
              内容
              <textarea
                autoFocus
                required
                rows={5}
                value={text}
                onChange={(e) => setText(e.target.value)}
              />
            </label>
            {error && <div className="error">{error}</div>}
            <div className="editor-actions">
              <button type="button" disabled={busy} onClick={() => setMode("")}>
                取消
              </button>
              <button className="primary" disabled={busy}>
                发送
              </button>
            </div>
          </form>
        </Dialog>
      )}
    </div>
  );
}
export function ToolControls({ taskID, call: c }) {
  const { busy, error, change } = useChange();
  const [open, setOpen] = useState(false),
    [result, setResult] = useState(""),
    [isError, setIsError] = useState(false);
  const options = (body) => ({ path: { id: taskID, call: c.id }, body });
  return (
    <>
      <div className="actions">
        {c.status === "approval" && (
          <>
            <button
              className="button"
              disabled={busy}
              onClick={() =>
                change("executionResolveToolResult", options({ approve: true }))
              }
            >
              批准
            </button>
            <button
              className="button"
              disabled={busy}
              onClick={() =>
                change(
                  "executionResolveToolResult",
                  options({ approve: false }),
                )
              }
            >
              拒绝
            </button>
          </>
        )}
        {["custom", "unknown"].includes(c.status) && (
          <button
            className="button"
            disabled={busy}
            onClick={() => setOpen(true)}
          >
            提交实际结果
          </button>
        )}
      </div>
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      {open && (
        <Dialog title="提交工具结果" busy={busy} close={() => setOpen(false)}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              change(
                "executionResolveToolResult",
                options({ result, is_error: isError }),
                () => setOpen(false),
              );
            }}
          >
            <label>
              结果
              <textarea
                rows={5}
                value={result}
                onChange={(e) => setResult(e.target.value)}
              />
            </label>
            <label className="check-label">
              <input
                type="checkbox"
                checked={isError}
                onChange={(e) => setIsError(e.target.checked)}
              />
              执行失败
            </label>
            {error && <div className="error">{error}</div>}
            <div className="editor-actions">
              <button type="button" onClick={() => setOpen(false)}>
                取消
              </button>
              <button className="primary" disabled={busy}>
                提交
              </button>
            </div>
          </form>
        </Dialog>
      )}
    </>
  );
}
