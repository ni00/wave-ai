package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

var han = regexp.MustCompile(`[\x{3400}-\x{9fff}]`)
var markdownLink = regexp.MustCompile(`\]\(([^)]+)\)`)

func withoutDocumentation(value any) any {
	switch value := value.(type) {
	case []any:
		out := make([]any, len(value))
		for i, v := range value {
			out[i] = withoutDocumentation(v)
		}
		return out
	case map[string]any:
		out := object{}
		for key, child := range value {
			if _, ok := child.(string); ok && (key == "description" || key == "summary") {
				continue
			}
			switch key {
			case "example", "examples", "default", "enum", "const":
				out[key] = child
			default:
				out[key] = withoutDocumentation(child)
			}
		}
		return out
	}
	return value
}
func (t *tool) check() error {
	var spec, chinese object
	if err := readJSON(t.path("api/openapi.json"), &spec); err != nil {
		return err
	}
	if err := readJSON(t.path("api/openapi.zh-CN.json"), &chinese); err != nil {
		return err
	}
	if !reflect.DeepEqual(withoutDocumentation(spec), withoutDocumentation(chinese)) {
		return fmt.Errorf("language versions changed the wire contract")
	}
	if !reflect.DeepEqual(at(spec, "components", "securitySchemes", "BearerAuth"), object{"type": "http", "scheme": "bearer"}) {
		return fmt.Errorf("invalid bearer security scheme")
	}
	for path, methods := range obj(spec["paths"]) {
		for method, raw := range obj(methods) {
			op := obj(raw)
			summary := str(op["summary"])
			if summary == "" || han.MatchString(summary) || strings.Contains(summary, " || ") {
				return fmt.Errorf("untranslated summary: %s %s", method, path)
			}
			for _, parameter := range list(op["parameters"]) {
				if at(parameter, "name") == "after" {
					if _, exists := obj(at(parameter, "schema"))["default"]; exists {
						return fmt.Errorf("after must not inject a default: %s", path)
					}
				}
			}
			media := func(status, want string) bool {
				content := obj(at(op, "responses", status, "content"))
				_, ok := content[want]
				return len(content) == 1 && ok
			}
			switch op["operationId"] {
			case "filesContent", "skillsContent":
				if !media("200", "application/octet-stream") || !media("401", "application/json") {
					return fmt.Errorf("invalid download media: %s", path)
				}
			case "executionStreamEvents":
				if !media("200", "text/event-stream") {
					return fmt.Errorf("invalid event media: %s", path)
				}
			}
		}
	}
	if err := t.checkMetadata(); err != nil {
		return err
	}
	if err := t.checkDocumentation(); err != nil {
		return err
	}
	fmt.Fprintln(t.out, "Client metadata, contract and skill checks passed")
	return nil
}
func (t *tool) checkMetadata() error {
	for path, pattern := range map[string]string{
		"sdks/go/go.mod": `(?m)^module ` + regexp.QuoteMeta(t.config.GoModule) + `$`,
		"cli/main.go":    `var version\s*=\s*"` + regexp.QuoteMeta(t.config.Version) + `"`,
	} {
		data, err := os.ReadFile(t.path(path))
		if err != nil {
			return err
		}
		if !regexp.MustCompile(pattern).Match(data) {
			return fmt.Errorf("release metadata mismatch: %s", path)
		}
	}
	data, err := os.ReadFile(t.path("sdks/python/pyproject.toml"))
	if err != nil {
		return err
	}
	for key, want := range map[string]string{"name": t.config.PythonPackage, "version": t.config.Version} {
		if !regexp.MustCompile(`(?m)^` + key + `\s*=\s*"` + regexp.QuoteMeta(want) + `"$`).Match(data) {
			return fmt.Errorf("Python %s does not match config", key)
		}
	}
	var npm object
	if err = readJSON(t.path("sdks/typescript/package.json"), &npm); err != nil {
		return err
	}
	if npm["name"] != t.config.NPMPackage || npm["version"] != t.config.Version {
		return fmt.Errorf("npm release metadata mismatch")
	}
	return nil
}
func (t *tool) checkLinks(path string, data []byte) error {
	for _, match := range markdownLink.FindAllSubmatch(data, -1) {
		link := string(match[1])
		if strings.Contains(link, "://") || strings.HasPrefix(link, "#") {
			continue
		}
		link, _, _ = strings.Cut(link, "#")
		target := filepath.Join(filepath.Dir(path), filepath.FromSlash(link))
		actual, err := filepath.EvalSymlinks(target)
		if err != nil {
			return fmt.Errorf("broken link in %s: %s", path, link)
		}
		rel, err := filepath.Rel(t.root, actual)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("link escapes repository in %s: %s", path, link)
		}
	}
	return nil
}
func (t *tool) checkDocumentation() error {
	for _, dir := range []string{".", "cli", "tools/clients", "sdks/go", "sdks/python", "sdks/typescript"} {
		for file, peer := range map[string]string{"README.md": "README.zh-CN.md", "README.zh-CN.md": "README.md"} {
			path := t.path(filepath.Join(dir, file))
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !strings.Contains(string(data), "]("+peer+")") {
				return fmt.Errorf("missing language switch: %s", path)
			}
			if err = t.checkLinks(path, data); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"wave-client", "wave-admin"} {
		dir := t.path("skills/" + name)
		data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(data), "---\nname: "+name+"\ndescription: ") || strings.Contains(string(data), "TODO") || len(strings.Split(strings.TrimSpace(string(data)), "\n")) >= 150 {
			return fmt.Errorf("invalid skill entrypoint: %s", name)
		}
		files, err := treeFiles(dir)
		if err != nil {
			return err
		}
		for _, file := range files {
			ext := filepath.Ext(file)
			if ext != ".md" && ext != ".yaml" {
				continue
			}
			data, err = os.ReadFile(file)
			if err != nil {
				return err
			}
			if han.Match(data) {
				return fmt.Errorf("skill instructions must be English: %s", file)
			}
			if ext == ".md" {
				if err = t.checkLinks(file, data); err != nil {
					return err
				}
			}
		}
		ui, err := os.ReadFile(filepath.Join(dir, "agents/openai.yaml"))
		if err != nil {
			return err
		}
		for _, field := range []string{"display_name:", "short_description:", "default_prompt:", "$" + name} {
			if !strings.Contains(string(ui), field) {
				return fmt.Errorf("missing %s in %s interface", field, name)
			}
		}
	}
	var asset any
	return readJSON(t.path("skills/wave-client/assets/client-tool.json"), &asset)
}
func (t *tool) compat(ctx context.Context, specPath, operationsPath string) error {
	var old, current object
	if err := readJSON(operationsPath, &old); err != nil {
		return err
	}
	if err := readJSON(t.path("tools/clients/operations.json"), &current); err != nil {
		return err
	}
	for _, id := range sortedKeys(old) {
		op, exists := current[id]
		if !exists {
			return fmt.Errorf("removed operation %s", id)
		}
		if at(op, "command") != at(old[id], "command") {
			return fmt.Errorf("renamed CLI command %s", at(old[id], "command"))
		}
	}
	if err := t.run(ctx, t.root, nil, "go", "run", "github.com/oasdiff/oasdiff@v1.31.0", "breaking", specPath, t.path("api/openapi.json"), "--fail-on", "WARN"); err != nil {
		return err
	}
	fmt.Fprintln(t.out, "HTTP contract and CLI operation compatibility passed")
	return nil
}
