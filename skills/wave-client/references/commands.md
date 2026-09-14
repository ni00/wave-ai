# API command reference (generated)

Find API commands by resource. Use `wavectl <resource> <command> --help` for installed flags,
`wavectl schema <resource> <command> --request` for request fields, or `--example` for a template.
Use `--dry-run` to validate and preview a request. For workflow commands, follow the task and tool references linked from SKILL.md.

## agents

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl agents list` | — | — | List Agents |
| `wavectl agents create` | — | `--model`, `--name` | Create an Agent |
| `wavectl agents get` | `--id` | — | Get an Agent |
| `wavectl agents update` | `--id` | `--config`, `--version` | Update an Agent |
| `wavectl agents archive` | `--id` | — | Archive an Agent |
| `wavectl agents versions` | `--id` | — | Agent version history |

## console

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl console import-bench` | — | `--file` | Import a benchmark report |
| `wavectl console event` | `--session`, `--sequence` | — | Read an execution event |
| `wavectl console log` | `--id` | — | Read a structured log |
| `wavectl console metrics` | — | — | Query monitoring metrics |
| `wavectl console list` | `--kind` | — | Browse console resources |
| `wavectl console resource` | `--kind`, `--id` | — | Read a console resource |

## credentials

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl credentials list` | — | — | List credentials |
| `wavectl credentials create` | — | `--host`, `--name`, `--token` | Store a credential |
| `wavectl credentials revoke` | `--id` | — | Revoke a credential |

## deployments

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl deployments list` | — | — | List deployments |
| `wavectl deployments create` | — | `--agent-id`, `--input`, `--name` | Create a deployment |
| `wavectl deployments pause` | `--id` | `--paused` | Pause or resume a deployment |
| `wavectl deployments run` | `--id` | — | Trigger a deployment manually |
| `wavectl deployments runs` | `--id` | — | Deployment run records |

## environments

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl environments list` | — | — | List environments |
| `wavectl environments create` | — | `--name` | Create an environment |
| `wavectl environments get` | `--id` | — | Get an environment |
| `wavectl environments archive` | `--id` | — | Archive an environment |

## events

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl events list` | `--id` | — | List session events |
| `wavectl events watch` | `--id` | — | Subscribe to session events |

## files

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl files list` | — | — | List files |
| `wavectl files upload` | — | `--file` | Upload a file |
| `wavectl files delete` | `--id` | — | Delete a file |
| `wavectl files get` | `--id` | — | Get file metadata |
| `wavectl files download` | `--id` | `--output` | Download file content |

## health

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl health` | — | — | Health check |

## memory

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl memory list` | — | — | List memory stores |
| `wavectl memory create` | — | `--name` | Create a memory store |
| `wavectl memory conflicts` | `--id` | — | List memory conflicts |
| `wavectl memory delete` | `--id` | `--path` | Delete a memory entry |
| `wavectl memory entries` | `--id` | — | List memory entries |
| `wavectl memory write` | `--id` | `--path` | Write a memory entry |
| `wavectl memory revisions` | `--id` | — | Memory revision history |

## sessions

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl sessions list` | — | — | List sessions |
| `wavectl sessions create` | — | — | Create a session |
| `wavectl sessions get` | `--id` | — | Get a session |
| `wavectl sessions archive` | `--id` | — | Archive a session |
| `wavectl sessions messages` | `--id` | — | Session message history |
| `wavectl sessions actions` | `--id` | — | Required user actions |

## skills

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl skills list` | — | — | List skill packages |
| `wavectl skills upload` | — | `--file` | Upload a skill package |
| `wavectl skills download` | `--id` | `--output` | Download a skill package |

## tasks

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl tasks list` | `--id` | — | List session tasks |
| `wavectl tasks create` | `--id` | `--agent-id`, `--input` | Create a task |
| `wavectl tasks get` | `--id` | — | Get a task |
| `wavectl tasks delegate` | `--id` | `--agent-id`, `--input` | Delegate a child task |
| `wavectl tasks cancel` | `--id` | — | Cancel a task |
| `wavectl tasks generations` | `--id` | — | Task model-call records |
| `wavectl tasks inputs` | `--id` | — | List task inputs |
| `wavectl tasks input` | `--id` | `--text` | Steer a task with additional input |
| `wavectl tasks reconcile` | `--id` | `--confirm-stopped` | Reconcile a task |
| `wavectl tasks resume` | `--id` | `--text` | Resume a terminal child task |
| `wavectl tasks trace` | `--id` | — | Task trace and timing breakdown |

## tools

| Command | Required path flags | Required body fields | Description |
| --- | --- | --- | --- |
| `wavectl tools list` | `--id` | — | List task tool calls |
| `wavectl tools resolve` | `--id`, `--call` | — | Submit tool approval or result |

Path IDs also accept positional arguments in the order shown by help; session/task IDs support `--session` / `--task` aliases.
Choose field flags or full `--body @file.json`; do not combine them.
Paginated commands support `--all --format jsonl`; uploads support `--file - --filename NAME`.
