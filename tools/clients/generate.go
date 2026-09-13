package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var generatedPaths = []string{"api/openapi.json", "api/openapi.zh-CN.json", "sdks/go/generated", "sdks/go/operations.json", "sdks/go/openapi.json", "sdks/python/wave_ai_generated", "sdks/python/wave_ai/operations.json", "sdks/typescript/src/generated", "sdks/typescript/src/operations.ts", "skills/wave-client/references/commands.md", "tools/clients/manifest.json"}

func operationMap(spec, semantics object) (object, []string, error) {
	operations := object{}
	var order []string
	commands := map[string]bool{}
	for _, path := range sortedKeys(obj(spec["paths"])) {
		methods := obj(at(spec, "paths", path))
		for _, method := range sortedKeys(methods) {
			op := obj(methods[method])
			id := str(op["operationId"])
			if id == "" {
				return nil, nil, fmt.Errorf("missing operationId: %s %s", method, path)
			}
			if _, exists := operations[id]; exists {
				return nil, nil, fmt.Errorf("duplicate operationId: %s", id)
			}
			metadata, exists := semantics[id]
			if !exists {
				return nil, nil, fmt.Errorf("missing operation metadata: %s", id)
			}
			item := object{}
			for key, value := range obj(metadata) {
				item[key] = value
			}
			command := str(item["command"])
			if command == "" || commands[command] {
				return nil, nil, fmt.Errorf("missing/duplicate CLI command for %s", id)
			}
			commands[command] = true
			item["id"] = id
			item["method"] = strings.ToUpper(method)
			item["path"] = path
			item["summary"] = op["summary"]
			params := op["parameters"]
			if params == nil {
				params = []any{}
			}
			item["parameters"] = params
			item["requestBody"] = op["requestBody"]
			item["responses"] = op["responses"]
			security, exists := op["security"]
			if !exists {
				security = spec["security"]
			}
			item["authenticated"] = len(list(security)) > 0
			operations[id] = item
			order = append(order, id)
		}
	}
	for id := range semantics {
		if _, exists := operations[id]; !exists {
			return nil, nil, fmt.Errorf("stale operation metadata: %s", id)
		}
	}
	return operations, order, nil
}

func commandReference(spec, operations object, order []string) string {
	var b strings.Builder
	b.WriteString("# API command reference (generated)\n\nFind API commands by resource. Use `wavectl <resource> <command> --help` for installed flags,\n`wavectl schema <resource> <command> --request` for request fields, or `--example` for a template.\nUse `--dry-run` to validate and preview a request. For workflow commands, follow the task and tool references linked from SKILL.md.\n\n")
	groups := map[string][]object{}
	for _, id := range order {
		op := obj(operations[id])
		group := strings.Fields(str(op["command"]))[0]
		groups[group] = append(groups[group], op)
	}
	flags := func(values []string) string {
		if len(values) == 0 {
			return "—"
		}
		return strings.Join(values, ", ")
	}
	flag := func(name string) string { return "`--" + strings.ToLower(strings.ReplaceAll(name, "_", "-")) + "`" }
	for _, group := range sortedKeys(groups) {
		fmt.Fprintf(&b, "## %s\n\n| Command | Required path flags | Required body fields | Description |\n| --- | --- | --- | --- |\n", group)
		for _, op := range groups[group] {
			var paths, body []string
			for _, p := range list(op["parameters"]) {
				if at(p, "in") == "path" {
					paths = append(paths, flag(str(at(p, "name"))))
				}
			}
			request := obj(at(op, "requestBody", "content", "application/json", "schema"))
			if ref := str(request["$ref"]); ref != "" {
				request = obj(at(spec, "components", "schemas", strings.TrimPrefix(ref, "#/components/schemas/")))
			}
			for _, name := range list(request["required"]) {
				body = append(body, flag(str(name)))
			}
			switch op["transport"] {
			case "upload":
				body = []string{"`--file`"}
			case "download":
				body = []string{"`--output`"}
			}
			fmt.Fprintf(&b, "| `wavectl %s` | %s | %s | %s |\n", op["command"], flags(paths), flags(body), strings.ReplaceAll(str(op["summary"]), "|", "/"))
		}
		b.WriteString("\n")
	}
	b.WriteString("Path IDs also accept positional arguments in the order shown by help; session/task IDs support `--session` / `--task` aliases.\nChoose field flags or full `--body @file.json`; do not combine them.\nPaginated commands support `--all --format jsonl`; uploads support `--file - --filename NAME`.\n")
	return b.String()
}

func (t *tool) generate(ctx context.Context, checkOnly bool) error {
	stage, err := os.MkdirTemp("", "wave-generate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = os.Chmod(stage, 0755); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(stage, "api"), 0755); err != nil {
		return err
	}
	for _, locale := range []struct{ file, language string }{{"openapi.json", "en"}, {"openapi.zh-CN.json", "zh-CN"}} {
		if err = t.run(ctx, t.root, nil, "node", "tools/clients/convert.cjs", "internal/platform/apidocs/swagger.json", filepath.Join(stage, "api", locale.file), locale.language); err != nil {
			return err
		}
	}
	var spec, semantics object
	if err = readJSON(filepath.Join(stage, "api/openapi.json"), &spec); err != nil {
		return err
	}
	if err = readJSON(t.path("tools/clients/operations.json"), &semantics); err != nil {
		return err
	}
	operations, order, err := operationMap(spec, semantics)
	if err != nil {
		return err
	}
	if err = t.generateSDKs(ctx, stage); err != nil {
		return err
	}
	if err = copyFile(filepath.Join(stage, "api/openapi.json"), filepath.Join(stage, "sdks/go/openapi.json")); err != nil {
		return err
	}
	for _, dest := range []string{"sdks/go/operations.json", "sdks/python/wave_ai/operations.json"} {
		if err = writeJSON(filepath.Join(stage, dest), operations); err != nil {
			return err
		}
	}
	data, err := jsonBytes(operations)
	if err != nil {
		return err
	}
	ts := "// Generated by go run ./tools/clients generate. Do not edit.\nexport const operations = " + strings.TrimSuffix(string(data), "\n") + " as const;\nexport type OperationID = keyof typeof operations;\n"
	if err = writeFile(filepath.Join(stage, "sdks/typescript/src/operations.ts"), []byte(ts), 0644); err != nil {
		return err
	}
	if err = writeFile(filepath.Join(stage, "skills/wave-client/references/commands.md"), []byte(commandReference(spec, operations, order)), 0644); err != nil {
		return err
	}
	manifest := object{"version": t.config.Version, "defaultLanguage": "en", "generatorImage": t.config.GeneratorImage, "operations": len(operations)}
	for key, path := range map[string]string{"sourceSHA256": t.path("internal/platform/apidocs/swagger.json"), "openapiSHA256": filepath.Join(stage, "api/openapi.json"), "openapiChineseSHA256": filepath.Join(stage, "api/openapi.zh-CN.json")} {
		sum, err := hashFile(path)
		if err != nil {
			return err
		}
		manifest[key] = sum
	}
	if err = writeJSON(filepath.Join(stage, "tools/clients/manifest.json"), manifest); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = t.publishGenerated(stage, checkOnly); err != nil {
		return err
	}
	fmt.Fprintf(t.out, "%s %d operations across three SDKs\n", map[bool]string{true: "Checked", false: "Generated"}[checkOnly], len(operations))
	return nil
}

func (t *tool) generateSDKs(ctx context.Context, stage string) error {
	docker := []string{"docker", "run", "--rm", "--network", "none"}
	if os.Getuid() >= 0 {
		docker = append(docker, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()))
	}
	docker = append(docker, "-v", stage+":/local", t.config.GeneratorImage)
	run := func(args ...string) error {
		return t.run(ctx, t.root, nil, append(append([]string{}, docker...), args...)...)
	}
	for _, file := range []string{"openapi.json", "openapi.zh-CN.json"} {
		if err := run("validate", "-i", "/local/api/"+file); err != nil {
			return err
		}
	}
	targets := []struct {
		name     string
		settings object
	}{{"go", object{"packageName": "generated", "isGoSubmodule": true}}, {"python", object{"packageName": "wave_ai_generated", "packageVersion": t.config.Version}}, {"typescript-fetch", object{"npmName": t.config.NPMPackage, "npmVersion": t.config.Version}}}
	for _, target := range targets {
		target.settings["hideGenerationTimestamp"] = true
		target.settings["disallowAdditionalPropertiesIfNotPresent"] = false
		if err := writeJSON(filepath.Join(stage, target.name+".json"), target.settings); err != nil {
			return err
		}
		if err := run("generate", "-i", "/local/api/openapi.json", "-g", target.name, "-o", "/local/raw/"+target.name, "-c", "/local/"+target.name+".json", "--global-property", "apiTests=false,modelTests=false,apiDocs=false,modelDocs=false"); err != nil {
			return err
		}
	}
	files, err := filepath.Glob(filepath.Join(stage, "raw/go/*.go"))
	if err != nil {
		return err
	}
	for _, name := range files {
		if err = copyFile(name, filepath.Join(stage, "sdks/go/generated", filepath.Base(name))); err != nil {
			return err
		}
	}
	if err = copyTree(filepath.Join(stage, "raw/python/wave_ai_generated"), filepath.Join(stage, "sdks/python/wave_ai_generated")); err != nil {
		return err
	}
	ts := filepath.Join(stage, "sdks/typescript/src/generated")
	if err = copyTree(filepath.Join(stage, "raw/typescript-fetch/src"), ts); err != nil {
		return err
	}
	files, err = treeFiles(ts)
	if err != nil {
		return err
	}
	imports := regexp.MustCompile(`(from\s+['"])(\.[^'"]+)(['"])`)
	for _, file := range files {
		if filepath.Ext(file) != ".ts" {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		data = imports.ReplaceAllFunc(data, func(match []byte) []byte {
			parts := imports.FindSubmatch(match)
			if strings.HasSuffix(string(parts[2]), ".js") {
				return match
			}
			return []byte(string(parts[1]) + string(parts[2]) + ".js" + string(parts[3]))
		})
		if err = writeFile(file, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func (t *tool) publishGenerated(stage string, checkOnly bool) error {
	var changed []string
	for _, name := range generatedPaths {
		if _, err := os.Stat(filepath.Join(stage, name)); err != nil {
			return fmt.Errorf("incomplete generation: %w", err)
		}
		same, err := samePath(filepath.Join(stage, name), t.path(name))
		if err != nil {
			return err
		}
		if !same {
			changed = append(changed, name)
		}
	}
	if checkOnly && len(changed) > 0 {
		return fmt.Errorf("out of date: %s; run make clients-generate", strings.Join(changed, ", "))
	}
	if !checkOnly {
		for _, name := range changed {
			if err := replacePath(filepath.Join(stage, name), t.path(name)); err != nil {
				return err
			}
		}
	}
	return nil
}
