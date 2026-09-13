# Submit, wait and resume events

## Prepare resources

Honor existing IDs and context. Use `agents list` and `sessions list` to find resources.
Model names must come from the deployment's supported models or the user's choice;
do not assume an example model name will work.

When new resources are needed:

```bash
AGENT_ID=$(wavectl agents create --name researcher --model "$MODEL" --select /id --raw)
SESSION_ID=$(wavectl sessions create --title research --select /id --raw)
```

For skills, mounted files or memory stores, inspect the session's environment constraints first:

```bash
wavectl schema sessions create --request
wavectl sessions create --body @session.json --dry-run
```

## Retain submission identity

Prepare prompt.txt and a unique key for this submission, such as a caller-generated UUID
that is saved for retries:

```bash
TASK_ID=$(wavectl tasks create --session "$SESSION_ID" --agent "$AGENT_ID" \
  --input-file prompt.txt --idempotency-key "$REQUEST_KEY" --select /id --raw)
wavectl tasks wait "$TASK_ID" --timeout 5m
```

If the creation request's network outcome is unknown, retry with the same REQUEST_KEY,
session and request body. Do not change the key to recreate work or treat a wait timeout
as a reason to resubmit. For shorter tasks, `tasks create --wait` returns
`{task, required_actions, reason}` instead of a standalone Task. Wait errors retain the
created task_id and a command for continuing inspection.

Handle nonzero wait outcomes explicitly in scripts using `set -e`:

```bash
if wavectl tasks wait "$TASK_ID" --timeout 5m > task-state.json; then
  cat task-state.json
else
  status=$?
  case "$status" in
    6|7) cat task-state.json ;; # Actions or unsuccessful terminal state; stdout still contains results.
    5) wavectl tasks get "$TASK_ID" ;; # Inspect the existing task after timeout/interruption.
    *) exit "$status" ;;
  esac
fi
```

## Incremental events

```bash
wavectl events watch "$SESSION_ID" --task "$TASK_ID" \
  --cursor-file .wave/task-events.json --reconnects 3
```

The cursor file atomically records session_id and the last successfully emitted event ID.
Restart the same command to continue. Do not share the file between sessions or combine
it with `--after` / `--last-event-id`. A missing cursor file starts from the beginning.
The last frame may replay after interruption; deduplicate by event ID.

A session contains events from multiple tasks. `--task` filters output only; another
child task's finished event does not establish that the target task has ended. Use
`tasks wait/get` to determine terminal state and retain raw data for unknown event types.

While a task runs, `tasks input TASK --text-file steering.txt` appends user input. This
does not modify the original request body; do not reuse the creation key for a different operation.
