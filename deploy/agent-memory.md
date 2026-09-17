# Configure agent memory

English | [简体中文](agent-memory.zh-CN.md)

## Enable memory

1. Create a memory store through the API or [CLI](../cli/README.md).
2. Bind its ID with `memory_store_ids` when creating a session. Bind the same store to other sessions that need the saved information. No environment is required.
3. Add the built-in tools `memory_read`, `memory_write`, and `memory_delete` to the agent's tool list. Enable only the operations the agent needs.

Tools can access only stores owned by the caller and bound to the current session.

## Read and update entries

| Tool | Required arguments | Behavior |
| --- | --- | --- |
| `memory_read` | `store_id` | Omit `path` to list entries; provide `path` to read one entry. |
| `memory_write` | `store_id`, `path`, `content`, `version` | Save content if the version matches. |
| `memory_delete` | `store_id`, `path`, `version` | Mark an entry as deleted if the version matches. |

Paths must be clean relative paths, such as `preferences.md`. Listing returns up
to 100 paths, versions, and deletion flags without content. Pass the returned
`next_offset` as `offset` to continue; `next_offset: 0` means the last page.

Read an entry before changing it, then pass its returned `version` to the write
or delete tool. A path that has never existed returns version 0. Deleted entries
retain their version; use that version to restore them. Tool reads and writes
limit entry content to 1 MiB.

Successful changes persist immediately, even if the task later fails. A version
conflict leaves the current entry unchanged and retains the attempted content in
the conflict history. Read the latest entry before deciding whether to write again.
