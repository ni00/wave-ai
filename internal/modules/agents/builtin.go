package agents

// BuiltinParameters describes the actual executor contract even when an agent
// only opts in by name. A configured schema cannot redefine built-in behavior.
func BuiltinParameters(name string) map[string]any {
	fields := map[string][]string{
		"read":          {"path"},
		"ls":            {"path"},
		"write":         {"path", "content"},
		"edit":          {"path", "old", "new"},
		"bash":          {"command"},
		"grep":          {"path", "pattern"},
		"find":          {"path", "pattern"},
		"memory_read":   {"store_id"},
		"memory_write":  {"store_id", "path", "content", "version"},
		"memory_delete": {"store_id", "path", "version"},
	}
	props := map[string]any{}
	for _, field := range fields[name] {
		props[field] = map[string]any{"type": "string"}
	}
	if name == "memory_read" {
		props["path"] = map[string]any{"type": "string", "description": "Omit to list entry paths and versions without content."}
		props["offset"] = map[string]any{"type": "integer", "minimum": 0, "description": "List offset; at most 100 entries per page."}
	}
	if name == "memory_write" || name == "memory_delete" {
		props["version"] = map[string]any{"type": "integer", "minimum": 0, "description": "Expected current version. Use 0 only for a new path; read again after a conflict."}
	}
	return map[string]any{"type": "object", "properties": props, "required": fields[name], "additionalProperties": false}
}
