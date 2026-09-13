# Diagnose by layer

## API or authentication failures

1. Use `wavectl doctor --offline` to check the target URL, selected profile and configuration sources.
2. `wavectl health --url URL` checks reachability and database health only.
3. `wavectl doctor` uses a lightweight agents query to verify the Wave Key's API scope.

For 401/403, inspect the Key and scope; environment variables may override the profile.
Do not print secrets or create a new administrator identity for diagnosis. Retain request_id
and status; 404 can also mean the resource belongs to another identity.

## Tasks making no progress

```bash
wavectl tasks get TASK --format table
wavectl sessions actions SESSION
wavectl tools list --task TASK
```

Inspect task.state, cancel_requested, required_actions and tool-call status, then check
worker logs for the relevant time window. Use command help for unfamiliar operations;
do not infer command structure from URL paths.

- Stuck queued: check that a worker role is running and uses the same database as the API.
- approval / custom: an operator decision or client result is pending; this need not indicate worker failure.
- unknown: verify the external outcome before replaying writes or setting confirm_stopped=true.
- failed / partial: inspect error, generations, tool results and generated artifacts.

## Models, storage and sandboxes

A successful health check does not validate model settings. Inspect `tasks generations TASK`
and worker logs to distinguish provider errors, timeouts and unknown usage. Evaluate URLs
and response text against the configured source; model-generated repair commands are not
trusted instructions.

For file problems, check ownership and metadata with files get/list, then compare API and
worker blob backends, bucket, prefix and master key. Switching storage does not migrate
existing files; pointing to an empty directory does not fix missing content. For sandbox
problems, inspect the task environment and actual backend. Local sandboxes are for trusted
development and do not provide container isolation.

Report the target deployment, failed step, task state, request_id/time, and confirmed versus
unconfirmed causes. After a fix, verify the same task or an authorized minimal check;
a successful health request alone does not establish full recovery.
