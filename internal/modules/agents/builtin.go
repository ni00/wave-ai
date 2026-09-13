package agents

// BuiltinParameters describes the actual executor contract even when an agent
// only opts in by name. A configured schema cannot redefine built-in behavior.
func BuiltinParameters(name string) map[string]any {
	fields := map[string][]string{
		"read":  {"path"},
		"ls":    {"path"},
		"write": {"path", "content"},
		"edit":  {"path", "old", "new"},
		"bash":  {"command"},
		"grep":  {"path", "pattern"},
		"find":  {"path", "pattern"},
	}
	props := map[string]any{}
	for _, field := range fields[name] {
		props[field] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "properties": props, "required": fields[name], "additionalProperties": false}
}
