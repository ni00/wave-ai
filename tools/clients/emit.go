package main

// Typed thin-method emitters. Each SDK keeps exactly one request
// implementation (its handwritten transport); these emitters add typed
// endpoint methods that delegate to it, replacing the generated clients.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type emitParameter struct {
	Name     string
	In       string
	Type     string
	Required bool
}

type emitOperation struct {
	ID           string
	Summary      string
	Params       []emitParameter
	Body         string
	BodyOptional bool
	Result       string
}

func (op emitOperation) paramsIn(where string) []emitParameter {
	var out []emitParameter
	for _, p := range op.Params {
		if p.In == where {
			out = append(out, p)
		}
	}
	return out
}

func (op emitOperation) models() []string {
	var out []string
	for _, name := range []string{op.Body, op.Result} {
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

var pythonKeywords = map[string]bool{"and": true, "as": true, "assert": true, "async": true, "await": true, "break": true, "class": true, "continue": true, "def": true, "del": true, "elif": true, "else": true, "except": true, "finally": true, "for": true, "from": true, "global": true, "if": true, "import": true, "in": true, "is": true, "lambda": true, "not": true, "or": true, "pass": true, "raise": true, "return": true, "try": true, "while": true, "with": true, "yield": true}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func pascalJoin(sep, name string) string {
	var b strings.Builder
	for _, part := range strings.Split(name, sep) {
		if part != "" {
			b.WriteString(upperFirst(part))
		}
	}
	return b.String()
}

// schemaClass maps "#/components/schemas/files.File" to "FilesFile",
// matching the model names OpenAPI Generator emits in every SDK.
func schemaClass(ref string) string {
	return pascalJoin(".", strings.TrimPrefix(ref, "#/components/schemas/"))
}

func pythonArg(name string) string {
	arg := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), ".", "_"))
	if pythonKeywords[arg] {
		return arg + "_"
	}
	return arg
}

func goArg(name string) string {
	if !strings.ContainsAny(name, "-._") {
		return name
	}
	return lowerFirst(pascalJoin("-", strings.NewReplacer(".", "-", "_", "-").Replace(name)))
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func tsArg(name string) string {
	if !strings.ContainsAny(name, "-.") {
		return name
	}
	parts := strings.Split(strings.ReplaceAll(name, ".", "-"), "-")
	out := strings.ToLower(parts[0])
	for _, part := range parts[1:] {
		out += upperFirst(part)
	}
	return out
}

func pythonType(schemaType string) string {
	switch schemaType {
	case "integer":
		return "int"
	case "boolean":
		return "bool"
	case "number":
		return "float"
	default:
		return "str"
	}
}

func goType(schemaType string) string {
	switch schemaType {
	case "integer":
		return "int"
	case "boolean":
		return "bool"
	case "number":
		return "float64"
	default:
		return "string"
	}
}

func goValue(name, schemaType string) string {
	switch schemaType {
	case "integer":
		return "strconv.Itoa(" + name + ")"
	case "boolean":
		return "strconv.FormatBool(" + name + ")"
	default:
		return name
	}
}

func tsType(schemaType string) string {
	switch schemaType {
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	default:
		return "string"
	}
}

func emitOperations(spec object) ([]emitOperation, error) {
	var ops []emitOperation
	seen := map[string]bool{}
	for _, path := range sortedKeys(obj(spec["paths"])) {
		methods := obj(at(spec, "paths", path))
		var shared []emitParameter
		for _, raw := range list(methods["parameters"]) {
			p := obj(raw)
			shared = append(shared, parameter(p))
		}
		for _, method := range sortedKeys(methods) {
			op := obj(methods[method])
			id := str(op["operationId"])
			if id == "" || seen[id] {
				continue
			}
			switch str(obj(op["x-wave-client"])["transport"]) {
			case "upload", "download", "sse":
				continue
			}
			seen[id] = true
			e := emitOperation{ID: id, Summary: str(op["summary"])}
			e.Params = append(e.Params, shared...)
			for _, raw := range list(op["parameters"]) {
				e.Params = append(e.Params, parameter(obj(raw)))
			}
			if ref := str(at(op, "requestBody", "content", "application/json", "schema", "$ref")); ref != "" {
				e.Body = schemaClass(ref)
				e.BodyOptional = at(op, "requestBody", "required") != true
			}
			for _, code := range sortedKeys(obj(op["responses"])) {
				if !strings.HasPrefix(code, "2") {
					continue
				}
				if ref := str(at(op, "responses", code, "content", "application/json", "schema", "$ref")); ref != "" {
					e.Result = schemaClass(ref)
				}
				break
			}
			ops = append(ops, e)
		}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no JSON operations to emit")
	}
	return ops, nil
}

func parameter(p object) emitParameter {
	return emitParameter{Name: str(p["name"]), In: str(p["in"]), Type: str(at(p, "schema", "type")), Required: p["required"] == true}
}

// Python

func pythonModelImports(ops []emitOperation) []string {
	set := map[string]bool{}
	for _, op := range ops {
		for _, name := range op.models() {
			set[name] = true
		}
	}
	var out []string
	for _, name := range sortedKeys(set) {
		out = append(out, fmt.Sprintf("from wave_ai_generated.models.%s import %s", snakeCase(name), name))
	}
	return out
}

func pythonFilterLine(target string, params []emitParameter) string {
	entries := make([]string, len(params))
	for i, p := range params {
		entries[i] = fmt.Sprintf("'%s': %s", p.Name, pythonArg(p.Name))
	}
	return fmt.Sprintf("%s = {key: value for key, value in {%s}.items() if value is not None}", target, strings.Join(entries, ", "))
}

func (op emitOperation) pythonSignature() string {
	args := []string{"self"}
	for _, p := range op.paramsIn("path") {
		args = append(args, fmt.Sprintf("%s: %s", pythonArg(p.Name), pythonType(p.Type)))
	}
	var kwargs []string
	for _, group := range [][]emitParameter{op.paramsIn("query"), op.paramsIn("header")} {
		for _, p := range group {
			if p.Required {
				kwargs = append(kwargs, fmt.Sprintf("%s: %s", pythonArg(p.Name), pythonType(p.Type)))
			}
		}
	}
	if op.Body != "" && !op.BodyOptional {
		kwargs = append(kwargs, "body: "+op.Body)
	}
	for _, group := range [][]emitParameter{op.paramsIn("query"), op.paramsIn("header")} {
		for _, p := range group {
			if !p.Required {
				kwargs = append(kwargs, fmt.Sprintf("%s: Optional[%s] = None", pythonArg(p.Name), pythonType(p.Type)))
			}
		}
	}
	if op.Body != "" && op.BodyOptional {
		kwargs = append(kwargs, fmt.Sprintf("body: Optional[%s] = None", op.Body))
	}
	if len(kwargs) > 0 {
		args = append(args, "*")
		args = append(args, kwargs...)
	}
	result := "Response"
	if op.Result != "" {
		result = op.Result
	}
	return "(" + strings.Join(args, ", ") + ") -> " + result
}

func pythonMethod(op emitOperation, asynchronous bool) string {
	var b strings.Builder
	keyword := "def"
	if asynchronous {
		keyword = "async def"
	}
	fmt.Fprintf(&b, "    %s %s%s:\n", keyword, snakeCase(op.ID), op.pythonSignature())
	fmt.Fprintf(&b, "        \"\"\"%s.\"\"\"\n", op.Summary)
	var parts []string
	if path := op.paramsIn("path"); len(path) > 0 {
		entries := make([]string, len(path))
		for i, p := range path {
			entries[i] = fmt.Sprintf("'%s': %s", p.Name, pythonArg(p.Name))
		}
		parts = append(parts, "path={"+strings.Join(entries, ", ")+"}")
	}
	if query := op.paramsIn("query"); len(query) > 0 {
		fmt.Fprintf(&b, "        %s\n", pythonFilterLine("query", query))
		parts = append(parts, "query=query")
	}
	if headers := op.paramsIn("header"); len(headers) > 0 {
		fmt.Fprintf(&b, "        %s\n", pythonFilterLine("headers", headers))
		parts = append(parts, "headers=headers")
	}
	if op.Body != "" {
		if op.BodyOptional {
			parts = append(parts, "body=body.to_dict() if body is not None else None")
		} else {
			parts = append(parts, "body=body.to_dict()")
		}
	}
	suffix := ""
	if len(parts) > 0 {
		suffix = ", " + strings.Join(parts, ", ")
	}
	if op.Result != "" {
		assign := "response = self.call"
		if asynchronous {
			assign = "response = await self.call"
		}
		fmt.Fprintf(&b, "        %s('%s'%s)\n", assign, op.ID, suffix)
		fmt.Fprintf(&b, "        return %s.from_dict(response.data)\n", op.Result)
	} else {
		verb := "return self.call"
		if asynchronous {
			verb = "return await self.call"
		}
		fmt.Fprintf(&b, "        %s('%s'%s)\n", verb, op.ID, suffix)
	}
	return b.String()
}

func (t *tool) emitPythonTyped(stage string, ops []emitOperation) error {
	var b strings.Builder
	b.WriteString("\"\"\"Generated typed endpoints over the shared transport. Do not edit.\"\"\"\nfrom __future__ import annotations\n\nfrom typing import Optional\n\n")
	for _, line := range pythonModelImports(ops) {
		b.WriteString(line + "\n")
	}
	b.WriteString("\nfrom .client import AsyncClient as _AsyncBase\nfrom .client import Client as _SyncBase\nfrom .client import Response\n\n\nclass Client(_SyncBase):\n")
	for _, op := range ops {
		b.WriteString(pythonMethod(op, false) + "\n")
	}
	b.WriteString("\nclass AsyncClient(_AsyncBase):\n")
	for _, op := range ops {
		b.WriteString(pythonMethod(op, true) + "\n")
	}
	return writeFile(filepath.Join(stage, "sdks/python/wave_ai/typed.py"), []byte(b.String()), 0644)
}

func (t *tool) emitPythonModelsInit(stage string) error {
	names, err := filepath.Glob(filepath.Join(stage, "sdks/python/wave_ai_generated/models/*.py"))
	if err != nil {
		return err
	}
	var classes []string
	for _, name := range names {
		base := strings.TrimSuffix(filepath.Base(name), ".py")
		if base == "__init__" {
			continue
		}
		classes = append(classes, pascalJoin("_", base))
	}
	if len(classes) == 0 {
		return fmt.Errorf("no generated Python models found")
	}
	sort.Strings(classes)
	var b strings.Builder
	b.WriteString("\"\"\"Generated Wave AI models. Do not edit.\"\"\"\n")
	for _, class := range classes {
		fmt.Fprintf(&b, "from wave_ai_generated.models.%s import %s\n", snakeCase(class), class)
	}
	b.WriteString("\n__all__ = [\n")
	for _, class := range classes {
		fmt.Fprintf(&b, "    %q,\n", class)
	}
	b.WriteString("]\n")
	if err = writeFile(filepath.Join(stage, "sdks/python/wave_ai_generated/__init__.py"), []byte(b.String()), 0644); err != nil {
		return err
	}
	return writeFile(filepath.Join(stage, "sdks/python/wave_ai_generated/py.typed"), []byte(""), 0644)
}

// Go

func (t *tool) emitGoTyped(stage string, ops []emitOperation) error {
	usesQuery, usesHeader, usesStrconv, usesJSON, usesGenerated := false, false, false, false, false
	for _, op := range ops {
		for _, p := range op.Params {
			if p.In == "query" {
				usesQuery = true
			}
			if p.In == "header" {
				usesHeader = true
			}
			if (p.In == "query" && p.Type != "string") || (p.In == "path" && p.Type != "string") {
				usesStrconv = true
			}
		}
		if op.Result != "" {
			usesJSON, usesGenerated = true, true
		}
		if op.Body != "" {
			usesGenerated = true
		}
	}
	var imports []string
	if usesJSON {
		imports = append(imports, "\t\"encoding/json\"")
	}
	if usesHeader {
		imports = append(imports, "\t\"net/http\"")
	}
	if usesQuery {
		imports = append(imports, "\t\"net/url\"")
	}
	if usesStrconv {
		imports = append(imports, "\t\"strconv\"")
	}
	if usesGenerated {
		imports = append(imports, "")
		imports = append(imports, "\t\""+t.config.GoModule+"/generated\"")
	}
	var b strings.Builder
	b.WriteString("// Code generated by go run ./tools/clients generate. Do not edit.\n\npackage wave\n\nimport (\n\t\"context\"\n")
	b.WriteString(strings.Join(imports, "\n"))
	b.WriteString("\n)\n\n")
	for _, op := range ops {
		b.WriteString(goMethod(op) + "\n")
	}
	return writeFile(filepath.Join(stage, "sdks/go/operations_gen.go"), []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0644)
}

func goMethod(op emitOperation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s %s\n", upperFirst(op.ID), op.Summary)
	args := []string{"ctx context.Context"}
	for _, p := range op.paramsIn("path") {
		args = append(args, fmt.Sprintf("%s %s", goArg(p.Name), goType(p.Type)))
	}
	for _, group := range [][]emitParameter{op.paramsIn("query"), op.paramsIn("header")} {
		for _, p := range group {
			if p.Required {
				args = append(args, fmt.Sprintf("%s %s", goArg(p.Name), goType(p.Type)))
			}
		}
	}
	if op.Body != "" && !op.BodyOptional {
		args = append(args, "body generated."+op.Body)
	}
	for _, group := range [][]emitParameter{op.paramsIn("query"), op.paramsIn("header")} {
		for _, p := range group {
			if !p.Required {
				args = append(args, fmt.Sprintf("%s *%s", goArg(p.Name), goType(p.Type)))
			}
		}
	}
	if op.Body != "" && op.BodyOptional {
		args = append(args, "body *generated."+op.Body)
	}
	result := "(*Response, error)"
	if op.Result != "" {
		result = "(*generated." + op.Result + ", error)"
	}
	fmt.Fprintf(&b, "func (c *Client) %s(%s) %s {\n", upperFirst(op.ID), strings.Join(args, ", "), result)
	var optionParts []string
	if path := op.paramsIn("path"); len(path) > 0 {
		entries := make([]string, len(path))
		for i, p := range path {
			entries[i] = fmt.Sprintf("%q: %s", p.Name, goValue(goArg(p.Name), p.Type))
		}
		optionParts = append(optionParts, "Path: map[string]string{"+strings.Join(entries, ", ")+"}")
	}
	if op.Body != "" && !op.BodyOptional {
		optionParts = append(optionParts, "Body: body")
	}
	if len(optionParts) > 0 {
		fmt.Fprintf(&b, "\toptions := Options{%s}\n", strings.Join(optionParts, ", "))
	} else {
		b.WriteString("\tvar options Options\n")
	}
	if query := op.paramsIn("query"); len(query) > 0 {
		b.WriteString("\tquery := make(url.Values)\n")
		for _, p := range query {
			name, arg := p.Name, goArg(p.Name)
			if p.Required {
				fmt.Fprintf(&b, "\tquery.Set(%q, %s)\n", name, goValue(arg, p.Type))
			} else {
				fmt.Fprintf(&b, "\tif %s != nil {\n\t\tquery.Set(%q, %s)\n\t}\n", arg, name, goValue("*"+arg, p.Type))
			}
		}
		b.WriteString("\toptions.Query = query\n")
	}
	if headers := op.paramsIn("header"); len(headers) > 0 {
		b.WriteString("\theaders := make(http.Header)\n")
		for _, p := range headers {
			name, arg := p.Name, goArg(p.Name)
			if p.Required {
				fmt.Fprintf(&b, "\theaders.Set(%q, %s)\n", name, goValue(arg, p.Type))
			} else {
				fmt.Fprintf(&b, "\tif %s != nil {\n\t\theaders.Set(%q, %s)\n\t}\n", arg, name, goValue("*"+arg, p.Type))
			}
		}
		b.WriteString("\toptions.Headers = headers\n")
	}
	if op.Body != "" && op.BodyOptional {
		b.WriteString("\tif body != nil {\n\t\toptions.Body = body\n\t}\n")
	}
	if op.Result == "" {
		fmt.Fprintf(&b, "\treturn c.Call(ctx, %q, options)\n}\n", op.ID)
		return b.String()
	}
	fmt.Fprintf(&b, "\tresponse, err := c.Call(ctx, %q, options)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n", op.ID)
	fmt.Fprintf(&b, "\tvar result generated.%s\n\tif err := json.Unmarshal(response.Data, &result); err != nil {\n\t\treturn nil, err\n\t}\n\treturn &result, nil\n}\n", op.Result)
	return b.String()
}

// TypeScript

func (op emitOperation) tsSignature() (string, string) {
	var args []string
	for _, p := range op.paramsIn("path") {
		args = append(args, fmt.Sprintf("%s: %s", tsArg(p.Name), tsType(p.Type)))
	}
	if op.Body != "" && !op.BodyOptional {
		args = append(args, "body: "+op.Body)
	}
	var pattern, fields []string
	addField := func(name, tsTypeValue string, optional bool) {
		pattern = append(pattern, name)
		if optional {
			fields = append(fields, fmt.Sprintf("%s?: %s", name, tsTypeValue))
		} else {
			fields = append(fields, fmt.Sprintf("%s: %s", name, tsTypeValue))
		}
	}
	for _, group := range [][]emitParameter{op.paramsIn("query"), op.paramsIn("header")} {
		for _, p := range group {
			addField(tsArg(p.Name), tsType(p.Type), !p.Required)
		}
	}
	if op.Body != "" && op.BodyOptional {
		addField("body", op.Body, true)
	}
	if len(fields) > 0 {
		args = append(args, "{ "+strings.Join(pattern, ", ")+" }: { "+strings.Join(fields, "; ")+" } = {}")
	}
	signature := strings.Join(args, ", ")
	result := "APIResponse<unknown>"
	if op.Result != "" {
		result = op.Result
	}
	return signature, result
}

func tsMethod(op emitOperation) string {
	var b strings.Builder
	signature, result := op.tsSignature()
	fmt.Fprintf(&b, "  /** %s. */\n  async %s(%s): Promise<%s> {\n", op.Summary, op.ID, signature, result)
	var optionParts []string
	if path := op.paramsIn("path"); len(path) > 0 {
		entries := make([]string, len(path))
		for i, p := range path {
			value := tsArg(p.Name)
			if p.Type != "string" {
				value = "String(" + value + ")"
			}
			entries[i] = fmt.Sprintf("%s: %s", p.Name, value)
		}
		optionParts = append(optionParts, "path: { "+strings.Join(entries, ", ")+" }")
	}
	if query := op.paramsIn("query"); len(query) > 0 {
		b.WriteString("    const query: Record<string, string> = {};\n")
		for _, p := range query {
			fmt.Fprintf(&b, "    if (%s !== undefined) {\n      query['%s'] = String(%s);\n    }\n", tsArg(p.Name), p.Name, tsArg(p.Name))
		}
		optionParts = append(optionParts, "query")
	}
	if headers := op.paramsIn("header"); len(headers) > 0 {
		b.WriteString("    const headers: Record<string, string> = {};\n")
		for _, p := range headers {
			fmt.Fprintf(&b, "    if (%s !== undefined) {\n      headers['%s'] = %s;\n    }\n", tsArg(p.Name), p.Name, tsArg(p.Name))
		}
		optionParts = append(optionParts, "headers")
	}
	if op.Body != "" {
		// Generated interfaces are camelCase; the wire contract is snake_case,
		// so request bodies and responses must go through the generated
		// converters (which also map Date fields).
		if op.BodyOptional {
			optionParts = append(optionParts, "body: body === undefined ? undefined : "+op.Body+"ToJSON(body)")
		} else {
			optionParts = append(optionParts, "body: "+op.Body+"ToJSON(body)")
		}
	}
	if len(optionParts) > 0 {
		fmt.Fprintf(&b, "    const response = await this.call('%s', { %s });\n", op.ID, strings.Join(optionParts, ", "))
	} else {
		fmt.Fprintf(&b, "    const response = await this.call('%s');\n", op.ID)
	}
	if op.Result != "" {
		fmt.Fprintf(&b, "    return %sFromJSON(response.data);\n", op.Result)
	} else {
		b.WriteString("    return response;\n")
	}
	b.WriteString("  }\n")
	return b.String()
}

func (t *tool) emitTSTyped(stage string, ops []emitOperation) error {
	set := map[string]bool{}
	for _, op := range ops {
		for _, name := range op.models() {
			set[name] = true
		}
	}
	var b strings.Builder
	b.WriteString("// Code generated by go run ./tools/clients generate. Do not edit.\n")
	converters := map[string]map[string]bool{}
	for _, op := range ops {
		for _, entry := range []struct{ model, fn string }{{op.Body, op.Body + "ToJSON"}, {op.Result, op.Result + "FromJSON"}} {
			if entry.model == "" {
				continue
			}
			if converters[entry.model] == nil {
				converters[entry.model] = map[string]bool{}
			}
			converters[entry.model][entry.fn] = true
		}
	}
	for _, name := range sortedKeys(set) {
		var needed []string
		for _, fn := range sortedKeys(converters[name]) {
			needed = append(needed, fn)
		}
		fmt.Fprintf(&b, "import { %s, type %s } from './generated/models/%s.js';\n", strings.Join(needed, ", "), name, name)
	}
	b.WriteString("\nimport { Client as BaseClient } from './client.js';\nimport type { APIResponse } from './client.js';\n\nexport { APIError, parseJSON } from './client.js';\nexport type { APIResponse, Event, Options, StreamOptions, WaitResult } from './client.js';\n\nexport class Client extends BaseClient {\n")
	for _, op := range ops {
		b.WriteString(tsMethod(op) + "\n")
	}
	content := strings.TrimRight(b.String(), "\n") + "\n}\n"
	return writeFile(filepath.Join(stage, "sdks/typescript/src/typed.ts"), []byte(content), 0644)
}

func (t *tool) emitTSModelsIndex(stage string) error {
	names, err := filepath.Glob(filepath.Join(stage, "sdks/typescript/src/generated/models/*.ts"))
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("// Code generated by go run ./tools/clients generate. Do not edit.\n")
	count := 0
	for _, name := range names {
		base := strings.TrimSuffix(filepath.Base(name), ".ts")
		if base == "index" {
			continue
		}
		fmt.Fprintf(&b, "export * from './models/%s.js';\n", base)
		count++
	}
	if count == 0 {
		return fmt.Errorf("no generated TypeScript models found")
	}
	if err = writeFile(filepath.Join(stage, "sdks/typescript/src/generated/index.ts"), []byte(b.String()), 0644); err != nil {
		return err
	}
	return writeFile(filepath.Join(stage, "sdks/typescript/src/generated/runtime.ts"), []byte(tsRuntimeShim), 0644)
}

// tsRuntimeShim keeps only the serialization helpers generated models rely on.
const tsRuntimeShim = `// Code generated by go run ./tools/clients generate. Do not edit.
// Serialization helpers retained for generated models.

export function serializeDateTime(value: Date): string {
    return value.toISOString();
}

export function serializeDate(value: Date): string {
    if (isNaN(value.getTime())) {
        throw new RangeError('Invalid time value');
    }
    const year = ('000' + value.getFullYear()).slice(-4);
    const month = ('0' + (value.getMonth() + 1)).slice(-2);
    const day = ('0' + value.getDate()).slice(-2);
    return year + '-' + month + '-' + day;
}

export function parseDate(value: Date | string): Date {
    if (value instanceof Date) {
        return value;
    }
    // 'new Date("2026-08-05")' would parse as UTC midnight: a different day west of UTC.
    // Local midnight is the stated day everywhere. setFullYear avoids the 1900 offset the
    // multi-argument constructor applies to years 0-99.
    const fullDate = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(value));
    if (fullDate) {
        const year = Number(fullDate[1]);
        const month = Number(fullDate[2]) - 1;
        const day = Number(fullDate[3]);
        const date = new Date(0);
        date.setFullYear(year, month, day);
        date.setHours(0, 0, 0, 0);
        // Out-of-range components (or a day the local zone skipped) silently roll over,
        // which would hand back a date the server never sent.
        if (date.getFullYear() !== year || date.getMonth() !== month || date.getDate() !== day) {
            return new Date(NaN);
        }
        return date;
    }
    return new Date(value);
}

export function parseDateTime(value: any): Date {
    return new Date(value);
}

export function mapValues(data: any, fn: (item: any) => any) {
    const result: { [key: string]: any } = {};
    for (const key of Object.keys(data)) {
        result[key] = fn(data[key]);
    }
    return result;
}
`

func (t *tool) emitTypedClients(stage string, spec object) error {
	ops, err := emitOperations(spec)
	if err != nil {
		return err
	}
	for _, emit := range []func(string, []emitOperation) error{t.emitPythonTyped, t.emitGoTyped, t.emitTSTyped} {
		if err = emit(stage, ops); err != nil {
			return err
		}
	}
	if err = t.emitPythonModelsInit(stage); err != nil {
		return err
	}
	return t.emitTSModelsIndex(stage)
}
