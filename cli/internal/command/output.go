package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/spf13/cobra"
)

func (s *settings) validateOutput() error {
	if s.format != "json" && s.format != "jsonl" && s.format != "table" {
		return usage("format must be json, jsonl or table")
	}
	if s.timeout < 0 {
		return usage("timeout cannot be negative")
	}
	if s.raw && (s.selection == "" || s.format != "json") {
		return usage("--raw requires --select and --format json")
	}
	if s.pretty && s.format != "json" {
		return usage("--pretty requires --format json")
	}
	if s.selection != "" && !strings.HasPrefix(s.selection, "/") {
		return usage("--select must be a JSON Pointer beginning with /")
	}
	return nil
}

func emit(cmd *cobra.Command, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err = decoder.Decode(&decoded); err != nil {
		return err
	}
	selection, _ := cmd.Flags().GetString("select")
	if selection != "" {
		decoded, err = selectValue(decoded, selection)
		if err != nil {
			return err
		}
	}
	raw, _ := cmd.Flags().GetBool("raw")
	if raw {
		switch v := decoded.(type) {
		case string, json.Number, bool:
			_, err = fmt.Fprintln(cmd.OutOrStdout(), v)
			return err
		default:
			return usage("--raw selection must be a string, number or boolean")
		}
	}
	format, _ := cmd.Flags().GetString("format")
	if format == "table" {
		return table(cmd, decoded)
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	pretty, _ := cmd.Flags().GetBool("pretty")
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(decoded)
}

func selectValue(value any, pointer string) (any, error) {
	for _, segment := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		switch v := value.(type) {
		case map[string]any:
			child, ok := v[segment]
			if !ok {
				return nil, usage("selection %q does not exist", pointer)
			}
			value = child
		case []any:
			i, e := strconv.Atoi(segment)
			if e != nil || i < 0 || i >= len(v) {
				return nil, usage("selection %q has an invalid array index", pointer)
			}
			value = v[i]
		default:
			return nil, usage("selection %q traverses a scalar", pointer)
		}
	}
	return value, nil
}

func cell(value any) string {
	var s string
	if v, ok := value.(string); ok {
		s = v
	} else {
		b, _ := json.Marshal(value)
		s = string(b)
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	runes := []rune(s)
	if len(runes) > 100 {
		s = string(runes[:97]) + "..."
	}
	return s
}

func table(cmd *cobra.Command, value any) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	if obj, ok := value.(map[string]any); ok {
		if rows, ok := obj["data"].([]any); ok {
			value = rows
		} else if task, ok := obj["task"].(map[string]any); ok {
			fmt.Fprintf(w, "TASK\tSTATE\tREASON\n%s\t%s\t%s\n", cell(task["id"]), cell(task["state"]), cell(obj["reason"]))
			if err := w.Flush(); err != nil {
				return err
			}
			if actions, ok := obj["required_actions"].([]any); ok && len(actions) > 0 {
				return table(cmd, actions)
			}
			return nil
		} else {
			keys := sortedKeys(obj)
			for _, key := range keys {
				fmt.Fprintf(w, "%s\t%s\n", key, cell(obj[key]))
			}
			return w.Flush()
		}
	}
	if rows, ok := value.([]any); ok {
		if len(rows) == 0 {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "No results.")
			return err
		}
		// Agent names and models live in config; surface them in human output.
		for index, row := range rows {
			obj, ok := row.(map[string]any)
			if !ok {
				continue
			}
			if config, ok := obj["config"].(map[string]any); ok {
				copy := make(map[string]any, len(obj)+2)
				for key, value := range obj {
					copy[key] = value
				}
				for _, key := range []string{"name", "model"} {
					if _, exists := copy[key]; !exists && config[key] != nil {
						copy[key] = config[key]
					}
				}
				rows[index] = copy
			}
		}
		columns := tableColumns(rows)
		fmt.Fprintln(w, strings.Join(columns, "\t"))
		for _, row := range rows {
			obj, ok := row.(map[string]any)
			if !ok {
				fmt.Fprintln(w, cell(row))
				continue
			}
			values := make([]string, len(columns))
			for i, col := range columns {
				if obj[col] != nil {
					values[i] = cell(obj[col])
				}
			}
			fmt.Fprintln(w, strings.Join(values, "\t"))
		}
	} else {
		fmt.Fprintln(w, cell(value))
	}
	return w.Flush()
}
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func tableColumns(rows []any) []string {
	keys := map[string]bool{}
	for _, row := range rows {
		if obj, ok := row.(map[string]any); ok {
			for k := range obj {
				keys[k] = true
			}
		}
	}
	columns := []string{}
	for _, k := range []string{"id", "name", "model", "state", "status", "task_id", "call_id", "type", "url", "active", "has_key", "created_at"} {
		if keys[k] {
			columns = append(columns, k)
		}
	}
	if len(columns) == 0 {
		for k := range keys {
			columns = append(columns, k)
		}
		sort.Strings(columns)
	}
	if len(columns) == 0 {
		columns = []string{"VALUE"}
	}
	return columns
}
