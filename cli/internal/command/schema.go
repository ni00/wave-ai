package command

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	wave "github.com/ni00/wave-ai/sdks/go"
	"github.com/spf13/cobra"
)

type schema map[string]any
type contract struct {
	Components struct {
		Schemas map[string]schema `json:"schemas"`
	} `json:"components"`
}

var apiContract = loadContract()

func loadContract() contract {
	var doc contract
	if err := json.Unmarshal(wave.Specification(), &doc); err != nil {
		panic(err)
	}
	return doc
}
func resolve(s schema) schema {
	if ref, ok := s["$ref"].(string); ok {
		return apiContract.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
	return s
}
func object(v any) schema {
	if s, ok := v.(map[string]any); ok {
		return schema(s)
	}
	if s, ok := v.(schema); ok {
		return s
	}
	return nil
}
func requestSchema(op wave.Operation) schema {
	content := object(op.RequestBody["content"])
	media := object(content["application/json"])
	return resolve(object(media["schema"]))
}
func flagName(value string) string { return strings.ToLower(strings.ReplaceAll(value, "_", "-")) }
func required(s schema, name string) bool {
	items, _ := s["required"].([]any)
	for _, item := range items {
		if item == name {
			return true
		}
	}
	return false
}

func validateValue(s schema, value any, path string) error {
	s = resolve(s)
	if value == nil {
		if s["nullable"] == true || len(s) == 0 {
			return nil
		}
		return usage("%s cannot be null", path)
	}
	if values, ok := s["enum"].([]any); ok {
		found := false
		for _, candidate := range values {
			found = found || reflect.DeepEqual(candidate, value)
		}
		if !found {
			return usage("%s must be one of %v", path, values)
		}
	}
	switch s["type"] {
	case "object":
		return validateObject(s, value, path)
	case "array":
		items, ok := value.([]any)
		if !ok {
			return usage("%s must be an array", path)
		}
		for i, item := range items {
			if err := validateValue(object(s["items"]), item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return usage("%s must be a string", path)
		}
		for key, tooSmall := range map[string]bool{"minLength": true, "maxLength": false} {
			if limit, ok := s[key].(float64); ok {
				n := utf8.RuneCountInString(text)
				if tooSmall && n < int(limit) || !tooSmall && n > int(limit) {
					return usage("%s violates %s=%d", path, key, int(limit))
				}
			}
		}
	case "integer", "number":
		var n float64
		switch v := value.(type) {
		case json.Number:
			var e error
			n, e = v.Float64()
			if e != nil {
				return usage("%s must be numeric", path)
			}
			if s["type"] == "integer" {
				if _, e = v.Int64(); e != nil {
					return usage("%s must be a signed 64-bit integer", path)
				}
			}
		case int64:
			n = float64(v)
		default:
			return usage("%s must be numeric", path)
		}
		if min, ok := s["minimum"].(float64); ok && n < min {
			return usage("%s must be at least %v", path, min)
		}
		if max, ok := s["maximum"].(float64); ok && n > max {
			return usage("%s must be at most %v", path, max)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return usage("%s must be a boolean", path)
		}
	}
	return nil
}
func validateObject(s schema, value any, path string) error {
	fields, ok := value.(map[string]any)
	if !ok {
		return usage("%s must be an object", path)
	}
	properties := object(s["properties"])
	for name := range properties {
		if required(s, name) {
			if _, ok := fields[name]; !ok {
				return usage("%s.%s is required", path, name)
			}
		}
	}
	for name, value := range fields {
		child, exists := properties[name]
		if !exists {
			if additional, ok := s["additionalProperties"].(map[string]any); ok {
				child = additional
			} else if s["additionalProperties"] == true {
				continue
			} else if properties != nil {
				return usage("unknown field %s.%s", path, name)
			} else {
				continue
			}
		}
		if err := validateValue(object(child), value, path+"."+name); err != nil {
			return err
		}
	}
	return nil
}
func validateBody(op wave.Operation, body any) error {
	if op.RequestBody["required"] == true && body == nil {
		return usage("a request body is required; use body flags or --body @file.json")
	}
	if body == nil {
		return nil
	}
	if err := validateValue(requestSchema(op), body, "body"); err != nil {
		return err
	}
	if op.ID == "executionResolveToolResult" {
		fields := body.(map[string]any)
		a, r := fields["approve"], fields["result"]
		if (a == nil) == (r == nil) {
			return usage("submit exactly one non-null approve or result")
		}
		if fields["error_code"] != nil && fields["error_code"] != "" && fields["is_error"] != true {
			return usage("error-code requires is-error=true")
		}
		if a != nil && (fields["is_error"] == true || fields["error_code"] != nil && fields["error_code"] != "") {
			return usage("approval cannot include result error details")
		}
	}
	return requireBodySize(body)
}

func schemaCommand(version string) *cobra.Command {
	var requestOnly, example bool
	cmd := &cobra.Command{Use: "schema [OPERATION_ID | RESOURCE COMMAND]", Short: "Inspect one command's contract, or export the full specification",
		Example: "  wavectl schema tasks create --request\n  wavectl schema agents create --example\n  wavectl schema executionResolveToolResult --request",
		Args:    cobra.MaximumNArgs(3), RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if requestOnly || example {
					return usage("choose an operation with --request or --example")
				}
				return emit(cmd, map[string]any{"openapi": wave.Specification(), "operations": wave.Operations(), "version": version})
			}
			op, err := findOperation(args)
			if err != nil {
				return err
			}
			if example {
				return emit(cmd, exampleValue(requestSchema(op), 0))
			}
			target := any(op)
			if requestOnly {
				target = op.RequestBody
			}
			return emit(cmd, map[string]any{"operation": target, "schemas": referencedSchemas(target)})
		}}
	cmd.Flags().BoolVar(&requestOnly, "request", false, "show only the request contract and referenced models")
	cmd.Flags().BoolVar(&example, "example", false, "show a minimal request template; replace example values before sending")
	cmd.MarkFlagsMutuallyExclusive("request", "example")
	return cmd
}
func findOperation(args []string) (wave.Operation, error) {
	name := strings.Join(args, " ")
	for id, op := range wave.Operations() {
		if id == name || op.Command == name {
			return op, nil
		}
	}
	return wave.Operation{}, usage("no API operation %q; use the resource command or stable operationId", name)
}
func referencedSchemas(value any) map[string]schema {
	result := map[string]schema{}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				if _, seen := result[name]; !seen {
					result[name] = apiContract.Components.Schemas[name]
					walk(map[string]any(result[name]))
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	data, _ := json.Marshal(value)
	var tree any
	_ = json.Unmarshal(data, &tree)
	walk(tree)
	return result
}
func exampleValue(s schema, depth int) any {
	s = resolve(s)
	if depth > 8 {
		return nil
	}
	if value, ok := s["example"]; ok {
		return value
	}
	if value, ok := s["default"]; ok {
		return value
	}
	if values, ok := s["enum"].([]any); ok && len(values) > 0 {
		return values[0]
	}
	switch s["type"] {
	case "object":
		out := map[string]any{}
		for name, child := range object(s["properties"]) {
			if required(s, name) {
				out[name] = exampleValue(object(child), depth+1)
			}
		}
		return out
	case "array":
		return []any{}
	case "integer", "number":
		if min, ok := s["minimum"]; ok {
			return min
		}
		return 0
	case "boolean":
		return false
	case "string":
		return "REPLACE_ME"
	}
	return nil
}

type bodyField struct {
	name, flag, fileFlag, repeatFlag string
	spec                             schema
	aliases                          []string
}

func addBodyFlags(cmd *cobra.Command, op wave.Operation) []bodyField {
	spec := requestSchema(op)
	props := object(spec["properties"])
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]bodyField, 0, len(names))
	for _, name := range names {
		property := resolve(object(props[name]))
		flag := flagName(name)
		if cmd.Flags().Lookup(flag) != nil {
			flag = "body-" + flag
		}
		help, _ := property["description"].(string)
		if help == "" {
			help = "request field " + name
		}
		if required(spec, name) {
			help += " (required)"
		}
		field := bodyField{name: name, flag: flag, spec: property}
		if name == "agent_id" {
			field.aliases = []string{"agent"}
		}
		switch property["type"] {
		case "boolean":
			cmd.Flags().Bool(flag, false, help)
		case "integer":
			cmd.Flags().Int64(flag, 0, help)
		default:
			cmd.Flags().String(flag, "", help)
		}
		for _, alias := range field.aliases {
			cmd.Flags().String(alias, "", "alias for --"+flag)
		}
		fileFields := map[string]bool{"input": true, "text": true, "result": true, "instructions": true, "token": true, "content": true}
		if property["type"] == "string" && fileFields[name] {
			field.fileFlag = flag + "-file"
			cmd.Flags().String(field.fileFlag, "", "read "+name+" from UTF-8 file; - reads stdin")
		}
		if property["type"] == "array" && object(property["items"])["type"] == "string" && strings.HasSuffix(flag, "-ids") {
			field.repeatFlag = strings.TrimSuffix(flag, "s")
			cmd.Flags().StringArray(field.repeatFlag, nil, "append one ID; may be repeated")
		}
		if property["type"] == "array" || property["type"] == "object" {
			cmd.Flags().Lookup(flag).Usage += "; JSON or @file.json"
		}
		fields = append(fields, field)
	}
	return fields
}
func (f bodyField) value(cmd *cobra.Command) (any, bool, error) {
	names := append([]string{f.flag, f.fileFlag, f.repeatFlag}, f.aliases...)
	chosen := ""
	for _, name := range names {
		if name != "" && cmd.Flags().Changed(name) {
			if chosen != "" {
				return nil, false, usage("use only one of --%s and --%s", chosen, name)
			}
			chosen = name
		}
	}
	if chosen == "" {
		return nil, false, nil
	}
	if chosen == f.fileFlag {
		name, _ := cmd.Flags().GetString(chosen)
		data, err := readInput(cmd, name)
		return string(data), true, err
	}
	if chosen == f.repeatFlag {
		values, _ := cmd.Flags().GetStringArray(chosen)
		items := make([]any, len(values))
		for i, v := range values {
			items[i] = v
		}
		return items, true, nil
	}
	switch f.spec["type"] {
	case "boolean":
		v, e := cmd.Flags().GetBool(chosen)
		return v, true, e
	case "integer":
		v, e := cmd.Flags().GetInt64(chosen)
		return v, true, e
	case "array", "object":
		text, _ := cmd.Flags().GetString(chosen)
		v, e := bodyInput(cmd, text)
		return v, true, e
	case "number":
		text, _ := cmd.Flags().GetString(chosen)
		if _, e := strconv.ParseFloat(text, 64); e != nil {
			return nil, false, usage("--%s must be numeric", chosen)
		}
		return json.Number(text), true, nil
	default:
		v, e := cmd.Flags().GetString(chosen)
		return v, true, e
	}
}
