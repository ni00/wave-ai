package command

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	wave "github.com/ni00/wave-ai/sdks/go"
	"github.com/spf13/cobra"
)

type parameterFlag struct {
	parameter wave.Parameter
	names     []string
}
type operationInput struct {
	op                                             wave.Operation
	parameters                                     []parameterFlag
	fields                                         []bodyField
	body                                           string
	all, dryRun, wait                              bool
	file, filename, output, taskFilter, cursorFile string
	reconnects                                     int
}

func operationCommand(s *settings, op wave.Operation, name string) *cobra.Command {
	input := &operationInput{op: op}
	cmd := &cobra.Command{Use: name, Short: op.Summary,
		Long:        op.Summary + "\n\nInspect the request with `wavectl schema " + op.Command + " --request`.\nUse --dry-run to validate and preview a request without sending it.",
		Annotations: map[string]string{"operationId": op.ID, "method": op.Method, "path": op.Path},
	}
	input.parameters = addParameterFlags(cmd, op)
	pathCount := 0
	for _, p := range op.Parameters {
		if p.In == "path" {
			pathCount++
			cmd.Use += " [" + strings.ToUpper(p.Name) + "]"
		}
	}
	cmd.Args = cobra.MaximumNArgs(pathCount)
	if op.RequestBody != nil && op.Transport != "upload" {
		cmd.Flags().StringVar(&input.body, "body", "", "complete JSON request: inline, @file.json, or - for stdin")
		input.fields = addBodyFlags(cmd, op)
	}
	cmd.Flags().BoolVar(&input.dryRun, "dry-run", false, "validate and preview a redacted request; do not contact the service")
	input.addWorkflowFlags(cmd)
	cmd.Example = operationExample(op)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		options, err := input.options(cmd, args)
		if err != nil {
			return err
		}
		if err = input.validate(cmd, options); err != nil {
			return err
		}
		if input.dryRun {
			return input.preview(cmd, s, options)
		}
		c, err := s.client()
		if err != nil {
			return err
		}
		ctx, cancel := s.context(cmd)
		defer cancel()
		return input.run(cmd, ctx, c, options)
	}
	return cmd
}

func addParameterFlags(cmd *cobra.Command, op wave.Operation) []parameterFlag {
	bindings := []parameterFlag{}
	for _, p := range op.Parameters {
		name := flagName(p.Name)
		help := p.Description
		if p.Required {
			help += " (required)"
		}
		if value, ok := p.Schema["default"]; ok {
			help += fmt.Sprintf("; server default %v", value)
		}
		switch p.Schema["type"] {
		case "integer":
			cmd.Flags().Int64(name, 0, help)
		case "boolean":
			cmd.Flags().Bool(name, false, help)
		default:
			cmd.Flags().String(name, "", help)
		}
		binding := parameterFlag{parameter: p, names: []string{name}}
		if p.In == "path" && p.Name == "id" {
			parts := strings.Split(strings.TrimPrefix(op.Path, "/v1/"), "/")
			if len(parts) > 0 {
				alias := strings.TrimSuffix(parts[0], "s")
				if alias != name && cmd.Flags().Lookup(alias) == nil {
					cmd.Flags().String(alias, "", "alias for --"+name)
					binding.names = append(binding.names, alias)
				}
			}
		}
		bindings = append(bindings, binding)
	}
	return bindings
}
func (i *operationInput) addWorkflowFlags(cmd *cobra.Command) {
	if i.op.Pagination != nil {
		cmd.Flags().BoolVar(&i.all, "all", false, "iterate all pages; --format jsonl emits items incrementally")
	}
	if i.op.ID == "executionCreateTask" {
		cmd.Flags().BoolVar(&i.wait, "wait", false, "wait for task completion or required actions after creation")
	}
	switch i.op.Transport {
	case "upload":
		cmd.Flags().StringVar(&i.file, "file", "", "file to upload, or - for streaming stdin")
		cmd.Flags().StringVar(&i.filename, "filename", "", "upload filename; required with --file -")
	case "download":
		cmd.Flags().StringVar(&i.output, "output", "", "destination path or - for binary stdout")
	case "sse":
		cmd.Flags().StringVar(&i.taskFilter, "task", "", "only emit events for this task")
		cmd.Flags().IntVar(&i.reconnects, "reconnects", 0, "maximum reconnect attempts")
		cmd.Flags().StringVar(&i.cursorFile, "cursor-file", "", "persist the session and last emitted event ID for later continuation")
	}
}
func (i *operationInput) options(cmd *cobra.Command, args []string) (wave.Options, error) {
	options := wave.Options{Path: map[string]string{}, Query: make(url.Values), Headers: make(http.Header)}
	position := 0
	for _, binding := range i.parameters {
		p := binding.parameter
		chosen := ""
		value := ""
		for _, name := range binding.names {
			if cmd.Flags().Changed(name) {
				if chosen != "" {
					return options, usage("choose one of --%s and --%s", chosen, name)
				}
				chosen = name
				value = cmd.Flags().Lookup(name).Value.String()
			}
		}
		if p.In == "path" && position < len(args) {
			if chosen != "" {
				return options, usage("provide %s as an argument or a flag, not both", p.Name)
			}
			value = args[position]
			chosen = p.Name
			position++
		}
		if chosen == "" {
			if p.Required {
				return options, usage("%s is required", p.Name)
			}
			continue
		}
		if value == "" && p.In == "path" {
			return options, usage("%s cannot be empty", p.Name)
		}
		var typed any = value
		switch p.Schema["type"] {
		case "integer":
			typed = json.Number(value)
		case "boolean":
			typed = value == "true"
		}
		if err := validateValue(schema(p.Schema), typed, "--"+binding.names[0]); err != nil {
			return options, err
		}
		if p.Name == "Idempotency-Key" && len(value) > 128 {
			return options, usage("Idempotency-Key must be at most 128 bytes")
		}
		switch p.In {
		case "path":
			options.Path[p.Name] = value
		case "query":
			options.Query.Set(p.Name, value)
		case "header":
			options.Headers.Set(p.Name, value)
		}
	}
	body, err := i.requestBody(cmd)
	options.Body = body
	return options, err
}
func (i *operationInput) requestBody(cmd *cobra.Command) (any, error) {
	stdinFields := 0
	for _, field := range i.fields {
		chosen := ""
		for _, name := range append([]string{field.flag, field.fileFlag, field.repeatFlag}, field.aliases...) {
			if name == "" || !cmd.Flags().Changed(name) {
				continue
			}
			if cmd.Flags().Changed("body") {
				return nil, usage("--body cannot be combined with request-field flags")
			}
			if chosen != "" {
				return nil, usage("use only one of --%s and --%s", chosen, name)
			}
			chosen = name
			if cmd.Flags().Lookup(name).Value.String() == "-" && (name == field.fileFlag || field.spec["type"] == "object" || field.spec["type"] == "array") {
				stdinFields++
			}
		}
	}
	if stdinFields > 1 {
		return nil, usage("stdin can supply only one request field; use files for the other fields")
	}
	fields := map[string]any{}
	for _, field := range i.fields {
		value, present, err := field.value(cmd)
		if err != nil {
			return nil, err
		}
		if present {
			fields[field.name] = value
		}
	}
	if cmd.Flags().Changed("body") {
		if len(fields) > 0 {
			return nil, usage("--body cannot be combined with request-field flags")
		}
		return bodyInput(cmd, i.body)
	}
	if len(fields) > 0 {
		return fields, nil
	}
	return nil, nil
}
func (i *operationInput) validate(cmd *cobra.Command, options wave.Options) error {
	if i.op.Transport == "upload" && i.file == "" {
		return usage("--file is required")
	}
	if i.op.Transport == "upload" && i.file == "-" && i.filename == "" {
		return usage("--filename is required for stdin uploads")
	}
	if i.op.Transport == "download" && i.output == "" {
		return usage("--output is required")
	}
	if i.op.Transport == "download" && i.output == "-" && !i.dryRun {
		for _, name := range []string{"format", "pretty", "select", "raw"} {
			if cmd.Flags().Changed(name) {
				return usage("binary stdout cannot be combined with --%s", name)
			}
		}
	}
	if i.op.Transport == "sse" {
		if i.reconnects < 0 {
			return usage("--reconnects cannot be negative")
		}
		if options.Query.Get("after") != "" && options.Headers.Get("Last-Event-ID") != "" {
			return usage("provide --after or --last-event-id, not both")
		}
		if value := options.Headers.Get("Last-Event-ID"); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				return usage("Last-Event-ID must be a nonnegative int64")
			}
		}
		if i.cursorFile != "" && (cmd.Flags().Changed("after") || cmd.Flags().Changed("last-event-id")) {
			return usage("--cursor-file cannot be combined with explicit cursors")
		}
		format, _ := cmd.Flags().GetString("format")
		pretty, _ := cmd.Flags().GetBool("pretty")
		if format == "table" || pretty {
			return usage("event streams use JSONL; remove --format table or --pretty")
		}
	}
	if i.op.Transport != "upload" {
		return validateBody(i.op, options.Body)
	}
	return nil
}

func (i *operationInput) run(cmd *cobra.Command, ctx context.Context, c *wave.Client, options wave.Options) error {
	switch i.op.Transport {
	case "sse":
		return i.watch(cmd, ctx, c, options)
	case "upload":
		return upload(cmd, ctx, c, i.op.ID, i.file, i.filename)
	case "download":
		return download(cmd, ctx, c, i.op.ID, options.Path["id"], options.Headers, i.output)
	}
	if i.all {
		return allPages(cmd, ctx, c, i.op.ID, options)
	}
	response, err := c.Call(ctx, i.op.ID, options)
	if err != nil {
		return err
	}
	if i.wait {
		return waitCreated(cmd, ctx, c, response.Data)
	}
	if len(response.Data) == 0 {
		return emit(cmd, map[string]any{"status": response.StatusCode})
	}
	return emit(cmd, response.Data)
}
func allPages(cmd *cobra.Command, ctx context.Context, c *wave.Client, op string, options wave.Options) error {
	format, _ := cmd.Flags().GetString("format")
	items := []json.RawMessage{}
	if err := c.Each(ctx, op, options, func(item json.RawMessage) error {
		if format == "jsonl" {
			return emit(cmd, item)
		}
		items = append(items, item)
		return nil
	}); err != nil {
		return err
	}
	if format == "jsonl" {
		return nil
	}
	return emit(cmd, items)
}

func redact(value any) any {
	switch value := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, child := range value {
			switch strings.ToLower(key) {
			case "token", "api_key", "password", "secret", "client_secret", "authorization":
				out[key] = "[REDACTED]"
			default:
				out[key] = redact(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(value))
		for n, child := range value {
			out[n] = redact(child)
		}
		return out
	}
	return value
}
func (i *operationInput) preview(cmd *cobra.Command, s *settings, options wave.Options) error {
	connection, err := s.connection()
	if err != nil {
		return err
	}
	path := i.op.Path
	for name, value := range options.Path {
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
	}
	target := strings.TrimRight(connection.URL, "/") + path
	if query := options.Query.Encode(); query != "" {
		target += "?" + query
	}
	headers := options.Headers.Clone()
	if connection.Key != "" && i.op.Authenticated {
		headers.Set("Authorization", "[REDACTED]")
	}
	result := map[string]any{"dry_run": true, "operation_id": i.op.ID, "method": i.op.Method, "url": target, "headers": headers, "body": redact(options.Body)}
	if i.file != "" {
		result["file"] = i.file
		result["filename"] = i.filename
	}
	if i.output != "" {
		result["output"] = i.output
	}
	if i.wait {
		result["wait"] = true
	}
	return emit(cmd, result)
}
func operationExample(op wave.Operation) string {
	examples := map[string]string{
		"agentsCreate":               "wavectl agents create --name assistant --model your-model --instructions-file instructions.txt",
		"executionCreateSession":     "wavectl sessions create --title research",
		"executionCreateTask":        "wavectl tasks create --session SESSION --agent AGENT --input-file prompt.txt --idempotency-key request-001 --wait",
		"executionResolveToolResult": "wavectl tools resolve TASK CALL --approve=false",
		"deploymentsPause":           "wavectl deployments pause DEPLOYMENT --paused=false",
		"filesUpload":                "cat report.txt | wavectl files upload --file - --filename report.txt",
		"filesContent":               "wavectl files download FILE --output report.txt",
		"executionStreamEvents":      "wavectl events watch SESSION --task TASK --cursor-file .wave/events.json --reconnects 3",
	}
	if value := examples[op.ID]; value != "" {
		return "  " + value
	}
	flags := []string{}
	for _, p := range op.Parameters {
		if p.Required {
			flags = append(flags, "--"+flagName(p.Name)+" "+strings.ToUpper(p.Name))
		}
	}
	sort.Strings(flags)
	if op.RequestBody != nil {
		flags = append(flags, "--body @request.json --dry-run")
	}
	return "  wavectl " + op.Command + " " + strings.Join(flags, " ")
}
