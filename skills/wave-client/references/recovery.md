# Recover using evidence

| Exit code | Response |
| --- | --- |
| 1 | Check local files, configuration or transport; preserve request identity if creation has an unknown outcome |
| 2 | Use the hint, command help and focused schema to correct input before retrying |
| 3 | Check the profile, environment overrides and Wave Key; do not automatically bootstrap another Key |
| 4 | Inspect HTTP status and request_id; on 409, read the current state first |
| 5 | The client stopped waiting; inspect the existing task_id, as remote work may still be running |
| 6 | Action is required; read required_actions from stdout |
| 7 | Task is partial/failed/canceled; inspect task.result, task.error and artifacts and report the actual outcome |

Reconstruct current state with `tasks get TASK` and `sessions actions SESSION` instead of
relying only on old SSE frames. Tasks and external tools may continue changing. Cancel
with `tasks cancel TASK`, then keep checking; accepted cancellation does not prove that
external processes, writes or network operations have stopped.

For confirm_tool_outcome, inspect the affected system, files or operation records and
submit a verifiable result. If the outcome remains unclear, report missing evidence;
do not invent success, automatically replay writes or mark an uncertain operation failed.

For reconcile_task, first confirm that the external operation has stopped and each
unresolved tool result has been handled, then run:

```bash
wavectl tasks reconcile "$TASK_ID" --body '{"confirm_stopped":true}'
```

`confirm_stopped=true` is a factual operator confirmation, not a default for clearing an
error. Without evidence, retain unknown and explain which person or system must confirm.
Keep session_id, task_id and existing idempotent request identities during recovery.

`tasks resume TASK --text-file continuation.txt` supplies new continuation input. Use it
only when continuation is needed and the server allows it; it cannot replace verification
of an unknown outcome.
